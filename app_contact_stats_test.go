package main

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestGetContactExchangeStatsUsesCompleteConversationTurns(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}, &models.Reaction{}, &models.MessageReceipt{}); err != nil {
		t.Fatal(err)
	}
	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB })

	base := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	messages := []models.Message{
		{ProtocolConvID: "test::chat", ProtocolMsgID: "1", SenderID: "contact", Timestamp: base, Attachments: "[]"},
		{ProtocolConvID: "test::chat", ProtocolMsgID: "2", SenderID: "contact", Timestamp: base.Add(10 * time.Second), Attachments: `[{"type":"image"}]`},
		{ProtocolConvID: "test::chat", ProtocolMsgID: "3", SenderID: "me", IsFromMe: true, Timestamp: base.Add(20 * time.Second)},
		{ProtocolConvID: "test::chat", ProtocolMsgID: "4", SenderID: "me", IsFromMe: true, Timestamp: base.Add(30 * time.Second)},
		{ProtocolConvID: "test::chat", ProtocolMsgID: "5", SenderID: "contact", Timestamp: base.Add(60 * time.Second)},
	}
	for i := range messages {
		if err := database.Create(&messages[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Create(&models.Reaction{MessageID: messages[2].ID, UserID: "contact", Emoji: "👍"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&models.Reaction{MessageID: messages[4].ID, UserID: "me", Emoji: "❤️"}).Error; err != nil {
		t.Fatal(err)
	}

	stats, err := (&App{}).GetContactExchangeStats("test::chat", "contact")
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalMessages != 5 || stats.SentMessages != 2 || stats.ReceivedMessages != 3 {
		t.Fatalf("unexpected message counts: %+v", stats)
	}
	if stats.AttachmentMessages != 1 || stats.ReactionsGiven != 1 || stats.ReactionsReceived != 1 {
		t.Fatalf("unexpected activity counts: %+v", stats)
	}
	if stats.MedianMyResponseSecs == nil || *stats.MedianMyResponseSecs != 10 {
		t.Fatalf("unexpected own response median: %v", stats.MedianMyResponseSecs)
	}
	if stats.MedianContactResponseSecs == nil || *stats.MedianContactResponseSecs != 30 {
		t.Fatalf("unexpected contact response median: %v", stats.MedianContactResponseSecs)
	}
}

func TestGetContactExchangeStatsExcludesUnjoinedGroupCalls(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Conversation{}, &models.Message{}); err != nil {
		t.Fatal(err)
	}
	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB })

	conversation := models.Conversation{ProtocolConvID: "test::group@g.us", IsGroup: true}
	if err := database.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	duration := int32(120)
	messages := []models.Message{
		{ProtocolConvID: conversation.ProtocolConvID, ProtocolMsgID: "missed", ConversationID: conversation.ID, Timestamp: time.Now(), CallType: "missed_group_voice", CallOutcome: "MISSED", CallDurationSecs: &duration},
		{ProtocolConvID: conversation.ProtocolConvID, ProtocolMsgID: "joined", ConversationID: conversation.ID, Timestamp: time.Now().Add(time.Minute), CallType: "missed_group_voice", CallOutcome: "CONNECTED", CallDurationSecs: &duration},
	}
	if err := database.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}

	stats, err := (&App{}).GetContactExchangeStats(conversation.ProtocolConvID, "")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 1 || stats.MissedCalls != 0 || stats.TotalCallDurationSecs != 120 {
		t.Fatalf("unexpected group call stats: %+v", stats)
	}
}
