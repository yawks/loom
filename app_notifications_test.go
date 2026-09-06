package main

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func seedNotificationConversation(t *testing.T, isGroup bool) models.Conversation {
	t.Helper()
	previous := db.DB
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.MetaContact{}, &models.LinkedAccount{}, &models.Conversation{}, &models.Message{}, &models.MessageWatchRule{}, &models.MessageWatchMatch{}, &models.NotificationSettings{}); err != nil {
		t.Fatal(err)
	}
	db.DB = database
	t.Cleanup(func() { db.DB = previous })
	account := models.LinkedAccount{Username: "Alice", ProviderInstanceID: "account-1"}
	if err := db.DB.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	conversation := models.Conversation{LinkedAccountID: account.ID, ProtocolConvID: "account-1::chat", IsGroup: isGroup, GroupName: "Project"}
	if err := db.DB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	return conversation
}

func TestPrepareSystemNotificationHonorsPrivacyAndScope(t *testing.T) {
	conversation := seedNotificationConversation(t, true)
	app := NewApp()
	settings := defaultNotificationSettings("")
	settings.Enabled = true
	settings.ConversationScope = notificationScopeDM
	if _, err := app.SaveNotificationSettings(settings); err != nil {
		t.Fatal(err)
	}
	message := models.Message{ConversationID: conversation.ID, ProtocolMsgID: "m1", Body: "secret"}
	event := core.MessageEvent{InstanceID: "account-1", Message: message}
	if got := app.prepareSystemNotification(event); got != nil {
		t.Fatalf("group should be filtered: %+v", got)
	}

	settings.ConversationScope = notificationScopeAll
	settings.ShowConversationName = false
	settings.ShowMessageDetail = false
	if _, err := app.SaveNotificationSettings(settings); err != nil {
		t.Fatal(err)
	}
	got := app.prepareSystemNotification(event)
	if got == nil || got.Title != "New message" || got.Body != "" {
		t.Fatalf("unexpected private notification: %+v", got)
	}
}

func TestPrepareSystemNotificationAttentionUsesWatchRules(t *testing.T) {
	conversation := seedNotificationConversation(t, false)
	app := NewApp()
	settings := defaultNotificationSettings("")
	settings.Enabled = true
	settings.Trigger = notificationAttention
	if _, err := app.SaveNotificationSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&models.MessageWatchRule{ConversationID: conversation.ID, Pattern: "urgent"}).Error; err != nil {
		t.Fatal(err)
	}
	event := core.MessageEvent{InstanceID: "account-1", Message: models.Message{ConversationID: conversation.ID, ProtocolMsgID: "m2", Body: "This is urgent"}}
	if got := app.prepareSystemNotification(event); got == nil {
		t.Fatal("watched keyword should notify")
	}
	event.Message.Body = "Routine"
	if got := app.prepareSystemNotification(event); got != nil {
		t.Fatalf("routine message should not notify: %+v", got)
	}
}
