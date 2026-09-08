package main

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestGetAttachmentMessagesReturnsAllConversationAttachmentsNewestFirst(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}
	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB })

	base := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	threadID := "parent"
	messages := []models.Message{
		{ProtocolConvID: "test::chat", ProtocolMsgID: "old", Timestamp: base, Attachments: `[{"type":"image","url":"old"}]`},
		{ProtocolConvID: "test::chat", ProtocolMsgID: "empty", Timestamp: base.Add(time.Minute), Attachments: "[]"},
		{ProtocolConvID: "other::chat", ProtocolMsgID: "other", Timestamp: base.Add(2 * time.Minute), Attachments: `[{"type":"image","url":"other"}]`},
		{ProtocolConvID: "test::chat", ProtocolMsgID: "thread", ThreadID: &threadID, Timestamp: base.Add(3 * time.Minute), Attachments: `[{"type":"file","url":"new"}]`},
	}
	if err := database.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}

	got, err := (&App{}).GetAttachmentMessages("test::chat")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 attachment messages, got %d", len(got))
	}
	if got[0].ProtocolMsgID != "thread" || got[1].ProtocolMsgID != "old" {
		t.Fatalf("expected newest-first results including thread replies, got %q then %q", got[0].ProtocolMsgID, got[1].ProtocolMsgID)
	}
}
