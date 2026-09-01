package main

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestSearchMessagesInConversationScopesAllLinkedAccounts(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.MetaContact{}, &models.LinkedAccount{}, &models.Conversation{}, &models.Message{}); err != nil {
		t.Fatal(err)
	}
	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB })

	target := models.MetaContact{DisplayName: "Target"}
	other := models.MetaContact{DisplayName: "Other"}
	if err := database.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	accounts := []models.LinkedAccount{
		{MetaContactID: target.ID, Protocol: "test-a", ProviderInstanceID: "a"},
		{MetaContactID: target.ID, Protocol: "test-b", ProviderInstanceID: "b"},
		{MetaContactID: other.ID, Protocol: "test-c", ProviderInstanceID: "c"},
	}
	if err := database.Create(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	conversations := []models.Conversation{
		{LinkedAccountID: accounts[0].ID, ProtocolConvID: "target-a"},
		{LinkedAccountID: accounts[1].ID, ProtocolConvID: "target-b"},
		{LinkedAccountID: accounts[2].ID, ProtocolConvID: "other-c"},
	}
	if err := database.Create(&conversations).Error; err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	messages := []models.Message{
		{ConversationID: conversations[0].ID, ProtocolConvID: "target-a", ProtocolMsgID: "first", Body: "shared needle", Timestamp: base},
		{ConversationID: conversations[1].ID, ProtocolConvID: "target-b", ProtocolMsgID: "second", Body: "shared needle", Timestamp: base.Add(time.Minute)},
		{ConversationID: conversations[2].ID, ProtocolConvID: "other-c", ProtocolMsgID: "excluded", Body: "shared needle", Timestamp: base.Add(2 * time.Minute)},
	}
	if err := database.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}

	page, err := (&App{}).SearchMessagesInConversation("needle", 0, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(page.Items), page.Items)
	}
	if page.Items[0].Message.ProtocolMsgID != "second" || page.Items[1].Message.ProtocolMsgID != "first" {
		t.Fatalf("unexpected scoped result order: %+v", page.Items)
	}
	for _, item := range page.Items {
		if item.MetaContactID != target.ID {
			t.Fatalf("result escaped target conversation: %+v", item)
		}
	}
}
