package main

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestGetHighlightedMessagesReturnsOnlyCanonicalMatchesNewestFirst(t *testing.T) {
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

	contact := models.MetaContact{DisplayName: "Project room", AvatarURL: "avatar"}
	if err := database.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	account := models.LinkedAccount{MetaContactID: contact.ID, Protocol: "test", ProviderInstanceID: "test-1"}
	if err := database.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	conversation := models.Conversation{LinkedAccountID: account.ID, ProtocolConvID: "test-1::room"}
	if err := database.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	messages := []models.Message{
		{ConversationID: conversation.ID, ProtocolConvID: conversation.ProtocolConvID, ProtocolMsgID: "ordinary", Body: "ordinary", Timestamp: base},
		{ConversationID: conversation.ID, ProtocolConvID: conversation.ProtocolConvID, ProtocolMsgID: "older", Body: "older", Timestamp: base.Add(time.Minute), HighlightReasons: []string{models.HighlightReasonDirectMention}},
		{ConversationID: conversation.ID, ProtocolConvID: conversation.ProtocolConvID, ProtocolMsgID: "newer", Body: "newer", Timestamp: base.Add(2 * time.Minute), HighlightReasons: []string{"future_reason"}},
	}
	if err := database.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}

	page, err := (&App{}).GetHighlightedMessages(0)
	if err != nil {
		t.Fatal(err)
	}
	if page.HasMore || len(page.Items) != 2 {
		t.Fatalf("unexpected page: %+v", page)
	}
	if page.Items[0].Message.ProtocolMsgID != "newer" || page.Items[1].Message.ProtocolMsgID != "older" {
		t.Fatalf("messages are not newest first: %+v", page.Items)
	}
	if page.Items[0].MetaContactID != contact.ID || page.Items[0].ProviderInstanceID != account.ProviderInstanceID {
		t.Fatalf("missing provider-neutral conversation metadata: %+v", page.Items[0])
	}
	refs, err := (&App{}).GetHighlightedMessageRefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("unexpected highlighted refs: %+v", refs)
	}
	for _, ref := range refs {
		if ref.ConversationID != conversation.ProtocolConvID || ref.MessageID == "ordinary" {
			t.Fatalf("invalid highlighted ref: %+v", ref)
		}
	}
}
