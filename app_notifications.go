package main

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const (
	notificationScopeAll  = "all"
	notificationScopeDM   = "dm"
	notificationEvery     = "every_message"
	notificationAttention = "attention"
)

type SystemNotification struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Body           string `json:"body,omitempty"`
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
}

func defaultNotificationSettings(instanceID string) models.NotificationSettings {
	return models.NotificationSettings{
		ProviderInstanceID: instanceID,
		UseGlobal:          instanceID != "",
		Enabled:            false, ShowConversationName: true, ShowMessageDetail: true,
		ConversationScope: notificationScopeAll, Trigger: notificationEvery,
	}
}

// GetNotificationSettings returns a stored policy or its default value.
func (a *App) GetNotificationSettings(providerInstanceID string) (models.NotificationSettings, error) {
	settings := defaultNotificationSettings(providerInstanceID)
	if db.DB == nil {
		return settings, nil
	}
	err := db.DB.Where("provider_instance_id = ?", providerInstanceID).First(&settings).Error
	if err == gorm.ErrRecordNotFound {
		return settings, nil
	}
	return settings, err
}

// SaveNotificationSettings validates and persists a global or account policy.
func (a *App) SaveNotificationSettings(settings models.NotificationSettings) (models.NotificationSettings, error) {
	if db.DB == nil {
		return settings, fmt.Errorf("database is not initialized")
	}
	if settings.ConversationScope != notificationScopeAll && settings.ConversationScope != notificationScopeDM {
		return settings, fmt.Errorf("invalid notification conversation scope")
	}
	if settings.Trigger != notificationEvery && settings.Trigger != notificationAttention {
		return settings, fmt.Errorf("invalid notification trigger")
	}
	if settings.ProviderInstanceID == "" {
		settings.UseGlobal = false
	}
	if !settings.ShowConversationName {
		settings.ShowMessageDetail = false
	}
	var existing models.NotificationSettings
	err := db.DB.Where("provider_instance_id = ?", settings.ProviderInstanceID).First(&existing).Error
	if err == nil {
		settings.ID = existing.ID
		settings.CreatedAt = existing.CreatedAt
	} else if err != gorm.ErrRecordNotFound {
		return settings, err
	}
	err = db.DB.Save(&settings).Error
	return settings, err
}

func (a *App) effectiveNotificationSettings(instanceID string) (models.NotificationSettings, error) {
	account, err := a.GetNotificationSettings(instanceID)
	if err != nil {
		return account, err
	}
	if account.UseGlobal {
		return a.GetNotificationSettings("")
	}
	return account, nil
}

func messageNeedsAttention(database *gorm.DB, message models.Message) bool {
	if len(message.HighlightReasons) > 0 {
		return true
	}
	if database == nil || message.ConversationID == 0 {
		return false
	}
	var rules []models.MessageWatchRule
	if database.Where("conversation_id = ?", message.ConversationID).Find(&rules).Error != nil {
		return false
	}
	text := messageWatchText(message)
	for _, rule := range rules {
		matcher, err := watchMatcher(rule)
		if err == nil && matcher(text) {
			return true
		}
	}
	return false
}

func (a *App) prepareSystemNotification(event core.MessageEvent) *SystemNotification {
	message := event.Message
	if message.IsFromMe || message.IsDeleted || message.IsStatusMessage {
		return nil
	}
	settings, err := a.effectiveNotificationSettings(event.InstanceID)
	if err != nil || !settings.Enabled {
		return nil
	}

	var conversation models.Conversation
	if db.DB == nil {
		return nil
	}
	query := db.DB
	if message.ConversationID != 0 {
		query = query.Where("id = ?", message.ConversationID)
	} else {
		query = query.Where("protocol_conv_id = ?", message.ProtocolConvID)
	}
	if query.First(&conversation).Error != nil {
		return nil
	}
	message.ConversationID = conversation.ID
	if settings.ConversationScope == notificationScopeDM && conversation.IsGroup {
		return nil
	}
	if settings.Trigger == notificationAttention && !messageNeedsAttention(db.DB, message) {
		return nil
	}

	title := "New message"
	body := ""
	if settings.ShowConversationName {
		title = strings.TrimSpace(conversation.GroupName)
		if title == "" {
			var account models.LinkedAccount
			if db.DB.First(&account, conversation.LinkedAccountID).Error == nil {
				title = strings.TrimSpace(account.Username)
			}
		}
		if title == "" {
			title = "New message"
		}
		if settings.ShowMessageDetail {
			body = strings.TrimSpace(message.Body)
			if body == "" && strings.TrimSpace(message.Attachments) != "" && message.Attachments != "[]" {
				body = "Attachment"
			}
		}
	}
	return &SystemNotification{ID: event.InstanceID + ":" + message.ProtocolMsgID, Title: title, Body: body, ConversationID: message.ProtocolConvID, MessageID: message.ProtocolMsgID}
}
