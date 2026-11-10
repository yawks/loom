package main

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	err = db.Transaction(db.DB, func(tx *gorm.DB) error {
		attemptRule := models.MessageWatchRule{ConversationID: conversation.ID, Pattern: pattern, IsRegex: isRegex}
		if err := tx.Create(&attemptRule).Error; err != nil {
			return err
		}
		if err := reconcileWatchRule(tx, attemptRule); err != nil {
			return err
		}
		rule = attemptRule
		return nil
	})
	if err == nil {
		err = reconcileAllWatchRules(db.DB)
	}
	return rule, err
}

// UpdateMessageWatchRule backfills the new pattern without revoking old matches.
func (a *App) UpdateMessageWatchRule(ruleID uint, pattern string, isRegex bool) (models.MessageWatchRule, error) {
	pattern = strings.TrimSpace(pattern)
	if err := validateWatchPattern(pattern, isRegex); err != nil {
		return models.MessageWatchRule{}, err
	}
	var rule models.MessageWatchRule
	err := db.Transaction(db.DB, func(tx *gorm.DB) error {
		var attemptRule models.MessageWatchRule
		if err := tx.First(&attemptRule, ruleID).Error; err != nil {
			return err
		}
		attemptRule.Pattern, attemptRule.IsRegex = pattern, isRegex
		attemptRule.LastScannedMessageID = 0
		if err := tx.Save(&attemptRule).Error; err != nil {
			return err
		}
		if err := reconcileWatchRule(tx, attemptRule); err != nil {
			return err
		}
		rule = attemptRule
		return nil
	})
	if err == nil {
		err = reconcileAllWatchRules(db.DB)
	}
	return rule, err
}

// DeleteMessageWatchRule disables future matching, preserving existing highlights.
func (a *App) DeleteMessageWatchRule(ruleID uint) error {
	return db.Transaction(db.DB, func(tx *gorm.DB) error {
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
	var messages []models.Message
	if err := tx.Where("conversation_id = ? AND id > ?", rule.ConversationID, rule.LastScannedMessageID).Order("id ASC").Limit(500).Find(&messages).Error; err != nil {
		return err
	}
	matches := make([]models.MessageWatchMatch, 0)
	for _, message := range messages {
		if !message.IsDeleted && matcher(messageWatchText(message)) {
			matches = append(matches, models.MessageWatchMatch{RuleID: rule.ID, MessageID: message.ID})
		}
	}
	if len(matches) > 0 {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&matches, 100).Error; err != nil {
			return err
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return tx.Model(&models.MessageWatchRule{}).Where("id = ?", rule.ID).
		UpdateColumn("last_scanned_message_id", messages[len(messages)-1].ID).Error
}

var watchReconcileMu sync.Mutex

func reconcileAllWatchRules(database *gorm.DB) error {
	watchReconcileMu.Lock()
	defer watchReconcileMu.Unlock()
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
	for _, rule := range rules {
		for {
			// Do not acquire a writer connection at all when there is no new work.
			var pending []uint
			if err := database.Model(&models.Message{}).Where("conversation_id = ? AND id > ?", rule.ConversationID, rule.LastScannedMessageID).Limit(1).Pluck("id", &pending).Error; err != nil {
				return err
			}
			if len(pending) == 0 {
				break
			}
			if err := db.Transaction(database, func(tx *gorm.DB) error {
				var current models.MessageWatchRule
				if err := tx.First(&current, rule.ID).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return nil
					}
					return err
				}
				return reconcileWatchRule(tx, current)
			}); err != nil {
				return err
			}
			if err := database.First(&rule, rule.ID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					break
				}
				return err
			}
		}
	}
	return nil
}

func highlightedMessagesWhere(database *gorm.DB) (string, []any) {
	args := []any{false, "", "null", "[]"}
	canonical := "deleted_at IS NULL AND is_deleted = ? AND highlight_reasons IS NOT NULL AND highlight_reasons NOT IN (?, ?, ?)"
	if database == nil || !database.Migrator().HasTable(&models.MessageWatchMatch{}) {
		return canonical, args
	}
	// Build the small candidate set first instead of probing matches for every
	// message in the database on each attention-inbox refresh.
	return `deleted_at IS NULL AND is_deleted = ? AND id IN (
		SELECT id FROM messages WHERE highlight_reasons IS NOT NULL AND highlight_reasons NOT IN (?, ?, ?)
		UNION SELECT message_id FROM message_watch_matches
	)`, args
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
	if err := database.Unscoped().Model(&models.MessageWatchRule{}).Where("conversation_id = ?", conversation.ID).Pluck("id", &ruleIDs).Error; err != nil {
		return err
	}
	return db.Transaction(database, func(tx *gorm.DB) error {
		if len(ruleIDs) > 0 {
			if err := tx.Where("rule_id IN ?", ruleIDs).Delete(&models.MessageWatchMatch{}).Error; err != nil {
				return err
			}
		}
		return tx.Unscoped().Where("conversation_id = ?", conversation.ID).Delete(&models.MessageWatchRule{}).Error
	})
}
