package main

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"fmt"
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
	if len(matches) != 2 {
		t.Fatalf("expected body and canonical card matches to be retained, got %+v", matches)
	}

	page, err := app.GetHighlightedMessages(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("unexpected watchlist: %+v", page.Items)
	}
	if err := app.DeleteMessageWatchRule(rule.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Model(&models.MessageWatchMatch{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected highlights to survive rule deletion, got %d", count)
	}
	refs, err := app.GetHighlightedMessageRefs()
	if err != nil || len(refs) != 2 {
		t.Fatalf("highlights after deletion: %v, %v", refs, err)
	}
	if err := deleteConversationWatchData(db.DB, conversation.ProtocolConvID); err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Unscoped().Model(&models.MessageWatchRule{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("hard cleanup left disabled rules: %d, %v", count, err)
	}
	if err := db.DB.Model(&models.MessageWatchMatch{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("hard cleanup left highlights: %d, %v", count, err)
	}
}

func TestMessageWatchRuleRejectsInvalidRegex(t *testing.T) {
	conversation := setupWatchRuleTestDB(t)
	if _, err := (&App{}).CreateMessageWatchRule(conversation.ProtocolConvID, "(", true); err == nil {
		t.Fatal("expected invalid regex to be rejected")
	}
}

func TestWatchRulesCheckpointBatchesAndReadOnlyRefresh(t *testing.T) {
	conversation := setupWatchRuleTestDB(t)
	messages := make([]models.Message, 1101)
	for i := range messages {
		messages[i] = models.Message{ConversationID: conversation.ID, ProtocolConvID: conversation.ProtocolConvID,
			ProtocolMsgID: fmt.Sprintf("batch-%d", i), Body: "urgent", Timestamp: time.Now()}
	}
	if err := db.DB.CreateInBatches(&messages, 20).Error; err != nil {
		t.Fatal(err)
	}
	app := &App{}
	rule, err := app.CreateMessageWatchRule(conversation.ProtocolConvID, "urgent", false)
	if err != nil {
		t.Fatal(err)
	}
	var persisted models.MessageWatchRule
	if err := db.DB.First(&persisted, rule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.LastScannedMessageID != messages[len(messages)-1].ID {
		t.Fatalf("incomplete checkpoint: %+v", persisted)
	}
	var count int64
	if err := db.DB.Model(&models.MessageWatchMatch{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != int64(len(messages)) {
		t.Fatalf("matches = %d", count)
	}

	writes := 0
	countWrite := func(tx *gorm.DB) { writes++ }
	if err := db.DB.Callback().Create().Before("gorm:create").Register("test:count", countWrite); err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Callback().Update().Before("gorm:update").Register("test:count", countWrite); err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Callback().Delete().Before("gorm:delete").Register("test:count", countWrite); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetHighlightedMessageRefs(); err != nil {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatalf("unchanged refresh performed %d writes", writes)
	}

	// A new insertion is evaluated even if its provider timestamp is old.
	message := models.Message{ConversationID: conversation.ID, ProtocolConvID: conversation.ProtocolConvID,
		ProtocolMsgID: "late-history", Body: "urgent", Timestamp: time.Unix(1, 0)}
	if err := db.DB.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	if err := reconcileAllWatchRules(db.DB); err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Model(&models.MessageWatchMatch{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != int64(len(messages)+1) {
		t.Fatalf("new message not evaluated: %d", count)
	}
	if err := app.DeleteMessageWatchRule(rule.ID); err != nil {
		t.Fatal(err)
	}
	message.ID = 0
	message.ProtocolMsgID = "after-deletion"
	if err := db.DB.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	refs, err := app.GetHighlightedMessageRefs()
	if err != nil || len(refs) != len(messages)+1 {
		t.Fatalf("deleted rule matched new work or lost highlights: %d, %v", len(refs), err)
	}
}
