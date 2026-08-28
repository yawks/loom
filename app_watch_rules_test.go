package main

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupWatchRuleTestDB(t *testing.T) models.Conversation {
	t.Helper()
	previous := db.DB
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(
		&models.MetaContact{}, &models.LinkedAccount{}, &models.Conversation{},
		&models.Message{}, &models.MessageWatchRule{}, &models.MessageWatchMatch{},
	); err != nil {
		t.Fatal(err)
	}
	db.DB = database
	t.Cleanup(func() { db.DB = previous })
	meta := models.MetaContact{DisplayName: "Room"}
	if err := database.Create(&meta).Error; err != nil {
		t.Fatal(err)
	}
	account := models.LinkedAccount{MetaContactID: meta.ID, ProviderInstanceID: "test-1", Protocol: "test", UserID: "room"}
	if err := database.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	conversation := models.Conversation{LinkedAccountID: account.ID, ProtocolConvID: "test-1::room"}
	if err := database.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	return conversation
}

func TestMessageWatchRuleBackfillsCanonicalAttachmentContentAndRecalculates(t *testing.T) {
	conversation := setupWatchRuleTestDB(t)
	messages := []models.Message{
		{ConversationID: conversation.ID, ProtocolConvID: conversation.ProtocolConvID, ProtocolMsgID: "body", Body: "Production is URGENT", Timestamp: time.Now()},
		{ConversationID: conversation.ID, ProtocolConvID: conversation.ProtocolConvID, ProtocolMsgID: "card", Body: "", Attachments: `[{"title":"Deployment blocked","fields":[{"value":"Owner Alice"}]}]`, Timestamp: time.Now()},
	}
	if err := db.DB.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}

	app := &App{}
	rule, err := app.CreateMessageWatchRule(conversation.ProtocolConvID, "urgent", false)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.DB.Model(&models.MessageWatchMatch{}).Where("rule_id = ?", rule.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one body match, got %d", count)
	}

	rule, err = app.UpdateMessageWatchRule(rule.ID, `deployment\s+blocked`, true)
	if err != nil {
		t.Fatal(err)
	}
	var matches []models.MessageWatchMatch
	if err := db.DB.Where("rule_id = ?", rule.ID).Find(&matches).Error; err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].MessageID != messages[1].ID {
		t.Fatalf("expected the canonical card match, got %+v", matches)
	}

	page, err := app.GetHighlightedMessages(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Message.ProtocolMsgID != "card" {
		t.Fatalf("unexpected watchlist: %+v", page.Items)
	}
	if err := app.DeleteMessageWatchRule(rule.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Model(&models.MessageWatchMatch{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected matches to be deleted, got %d", count)
	}
}

func TestMessageWatchRuleRejectsInvalidRegex(t *testing.T) {
	conversation := setupWatchRuleTestDB(t)
	if _, err := (&App{}).CreateMessageWatchRule(conversation.ProtocolConvID, "(", true); err == nil {
		t.Fatal("expected invalid regex to be rejected")
	}
}
