package main

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"
)

// GetMessageWatchRules returns the local watch rules for a canonical
// conversation ID.
func (a *App) GetMessageWatchRules(conversationID string) ([]models.MessageWatchRule, error) {
	rules := []models.MessageWatchRule{}
	conversation, err := watchConversation(db.DB, conversationID)
	if err != nil {
		return rules, err
	}
	err = db.DB.Where("conversation_id = ?", conversation.ID).Order("created_at ASC").Find(&rules).Error
	return rules, err
}

// CreateMessageWatchRule validates, persists and backfills a watch rule.
func (a *App) CreateMessageWatchRule(conversationID, pattern string, isRegex bool) (models.MessageWatchRule, error) {
	pattern = strings.TrimSpace(pattern)
	if err := validateWatchPattern(pattern, isRegex); err != nil {
		return models.MessageWatchRule{}, err
	}
	conversation, err := watchConversation(db.DB, conversationID)
	if err != nil {
		return models.MessageWatchRule{}, err
	}
	rule := models.MessageWatchRule{ConversationID: conversation.ID, Pattern: pattern, IsRegex: isRegex}
	err = db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&rule).Error; err != nil {
			return err
		}
		return reconcileWatchRule(tx, rule)
	})
	return rule, err
}

// UpdateMessageWatchRule replaces a rule and recalculates all of its matches.
func (a *App) UpdateMessageWatchRule(ruleID uint, pattern string, isRegex bool) (models.MessageWatchRule, error) {
	pattern = strings.TrimSpace(pattern)
	if err := validateWatchPattern(pattern, isRegex); err != nil {
		return models.MessageWatchRule{}, err
	}
	var rule models.MessageWatchRule
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&rule, ruleID).Error; err != nil {
			return err
		}
		rule.Pattern, rule.IsRegex = pattern, isRegex
		if err := tx.Save(&rule).Error; err != nil {
			return err
		}
		return reconcileWatchRule(tx, rule)
	})
	return rule, err
}

// DeleteMessageWatchRule removes a rule and its materialized matches.
func (a *App) DeleteMessageWatchRule(ruleID uint) error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("rule_id = ?", ruleID).Delete(&models.MessageWatchMatch{}).Error; err != nil {
			return err
		}
		result := tx.Delete(&models.MessageWatchRule{}, ruleID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func watchConversation(database *gorm.DB, conversationID string) (models.Conversation, error) {
	if database == nil {
		return models.Conversation{}, fmt.Errorf("database is not initialized")
	}
	var conversation models.Conversation
	if err := database.Where("protocol_conv_id = ?", conversationID).First(&conversation).Error; err != nil {
		return conversation, fmt.Errorf("conversation %s: %w", conversationID, err)
	}
	return conversation, nil
}

func validateWatchPattern(pattern string, isRegex bool) error {
	if pattern == "" {
		return fmt.Errorf("watch pattern cannot be empty")
	}
	if isRegex {
		if _, err := regexp.Compile("(?i)" + pattern); err != nil {
			return fmt.Errorf("invalid regular expression: %w", err)
		}
	}
	return nil
}

func watchMatcher(rule models.MessageWatchRule) (func(string) bool, error) {
	if rule.IsRegex {
		re, err := regexp.Compile("(?i)" + rule.Pattern)
		if err != nil {
			return nil, err
		}
		return re.MatchString, nil
	}
	pattern := strings.ToLower(rule.Pattern)
	return func(content string) bool { return strings.Contains(strings.ToLower(content), pattern) }, nil
}

// messageWatchText builds searchable text exclusively from canonical message
// fields. Attachment/card JSON is traversed generically, so provider wire
// formats never leak into the matcher or frontend.
func messageWatchText(message models.Message) string {
	parts := []string{message.Body, message.SenderName, message.QuotedSenderName}
	if message.QuotedBody != nil {
		parts = append(parts, *message.QuotedBody)
	}
	if message.Attachments != "" {
		var value any
		if json.Unmarshal([]byte(message.Attachments), &value) == nil {
			collectWatchStrings(value, &parts)
		} else {
			parts = append(parts, message.Attachments)
		}
	}
	return strings.Join(parts, "\n")
}

func collectWatchStrings(value any, parts *[]string) {
	switch typed := value.(type) {
	case string:
		*parts = append(*parts, typed)
		trimmed := strings.TrimSpace(typed)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			var nested any
			if json.Unmarshal([]byte(trimmed), &nested) == nil {
				collectWatchStrings(nested, parts)
			}
		}
	case []any:
		for _, item := range typed {
			collectWatchStrings(item, parts)
		}
	case map[string]any:
		for _, item := range typed {
			collectWatchStrings(item, parts)
		}
	}
}

func reconcileWatchRule(tx *gorm.DB, rule models.MessageWatchRule) error {
	matcher, err := watchMatcher(rule)
	if err != nil {
		return err
	}
	if err := tx.Where("rule_id = ?", rule.ID).Delete(&models.MessageWatchMatch{}).Error; err != nil {
		return err
	}
	var messages []models.Message
	if err := tx.Where("conversation_id = ? AND deleted_at IS NULL AND is_deleted = ?", rule.ConversationID, false).Find(&messages).Error; err != nil {
		return err
	}
	matches := make([]models.MessageWatchMatch, 0)
	for _, message := range messages {
		if matcher(messageWatchText(message)) {
			matches = append(matches, models.MessageWatchMatch{RuleID: rule.ID, MessageID: message.ID})
		}
	}
	if len(matches) == 0 {
		return nil
	}
	return tx.Create(&matches).Error
}

func reconcileAllWatchRules(database *gorm.DB) error {
	if database == nil {
		return nil
	}
	if !database.Migrator().HasTable(&models.MessageWatchRule{}) || !database.Migrator().HasTable(&models.MessageWatchMatch{}) {
		return nil
	}
	var rules []models.MessageWatchRule
	if err := database.Find(&rules).Error; err != nil {
		return err
	}
	return database.Transaction(func(tx *gorm.DB) error {
		for _, rule := range rules {
			if err := reconcileWatchRule(tx, rule); err != nil {
				return err
			}
		}
		return nil
	})
}

func highlightedMessagesWhere(database *gorm.DB) (string, []any) {
	args := []any{false, "", "null", "[]"}
	canonical := "deleted_at IS NULL AND is_deleted = ? AND highlight_reasons IS NOT NULL AND highlight_reasons NOT IN (?, ?, ?)"
	if database == nil || !database.Migrator().HasTable(&models.MessageWatchMatch{}) {
		return canonical, args
	}
	return `deleted_at IS NULL AND is_deleted = ? AND ((highlight_reasons IS NOT NULL AND highlight_reasons NOT IN (?, ?, ?)) OR EXISTS (SELECT 1 FROM message_watch_matches WHERE message_watch_matches.message_id = messages.id))`, args
}

func deleteConversationWatchData(database *gorm.DB, conversationID string) error {
	conversation, err := watchConversation(database, conversationID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	var ruleIDs []uint
	if err := database.Model(&models.MessageWatchRule{}).Where("conversation_id = ?", conversation.ID).Pluck("id", &ruleIDs).Error; err != nil {
		return err
	}
	return database.Transaction(func(tx *gorm.DB) error {
		if len(ruleIDs) > 0 {
			if err := tx.Where("rule_id IN ?", ruleIDs).Delete(&models.MessageWatchMatch{}).Error; err != nil {
				return err
			}
		}
		return tx.Where("conversation_id = ?", conversation.ID).Delete(&models.MessageWatchRule{}).Error
	})
}
