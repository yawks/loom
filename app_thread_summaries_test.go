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

func TestGetThreadSummariesReturnsCountsWithoutReplies(t *testing.T) {
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

	conversationID := "test::conversation"
	parentA, parentB := "parent-a", "parent-b"
	messages := []models.Message{
		{ProtocolConvID: conversationID, ProtocolMsgID: parentA, Timestamp: time.Now()},
		{ProtocolConvID: conversationID, ProtocolMsgID: parentB, ThreadID: &parentB, Timestamp: time.Now()},
		{ProtocolConvID: conversationID, ProtocolMsgID: "reply-a-1", ThreadID: &parentA, Timestamp: time.Now()},
		{ProtocolConvID: conversationID, ProtocolMsgID: "reply-a-2", ThreadID: &parentA, Timestamp: time.Now()},
		{ProtocolConvID: conversationID, ProtocolMsgID: "reply-b-1", ThreadID: &parentB, Timestamp: time.Now()},
	}
	for i := range messages {
		if err := database.Create(&messages[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	summaries, err := (&App{}).GetThreadSummaries(conversationID, []string{parentA, parentB, "no-thread"})
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int, len(summaries))
	for _, summary := range summaries {
		counts[summary.ParentMessageID] = summary.ReplyCount
	}
	if len(counts) != 2 || counts[parentA] != 2 || counts[parentB] != 1 {
		t.Fatalf("unexpected thread summaries: %+v", summaries)
	}
}

func TestGetUnreadMessageLocationsOrdersAndResolvesThreads(t *testing.T) {
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

	const conversationID = "test::unread-locations"
	threadID := "parent"
	base := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	messages := []models.Message{
		{ProtocolConvID: conversationID, ProtocolMsgID: "newer-main", Timestamp: base.Add(2 * time.Minute)},
		{ProtocolConvID: conversationID, ProtocolMsgID: "oldest-reply", ThreadID: &threadID, Timestamp: base},
		{ProtocolConvID: "other::conversation", ProtocolMsgID: "foreign", ThreadID: &threadID, Timestamp: base.Add(-time.Minute)},
	}
	for i := range messages {
		if err := database.Create(&messages[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	locations, err := (&App{}).GetUnreadMessageLocations(conversationID, []string{"newer-main", "oldest-reply", "foreign", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) != 2 {
		t.Fatalf("got %d locations, want 2: %+v", len(locations), locations)
	}
	if locations[0].MessageID != "oldest-reply" || locations[0].ThreadID != threadID {
		t.Fatalf("oldest location = %+v, want thread reply", locations[0])
	}
	if locations[1].MessageID != "newer-main" || locations[1].ThreadID != "" {
		t.Fatalf("main location = %+v, want top-level message", locations[1])
	}
}

func TestGetMessagesForConversationPaginatesMainMessages(t *testing.T) {
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

	const conversationID = "test::busy-thread"
	base := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 55; i++ {
		parentID := fmt.Sprintf("parent-%02d", i)
		if err := database.Create(&models.Message{
			ProtocolConvID: conversationID,
			ProtocolMsgID:  parentID,
			Timestamp:      base.Add(time.Duration(i) * time.Minute),
		}).Error; err != nil {
			t.Fatal(err)
		}
		if i >= 50 {
			for j := 0; j < 40; j++ {
				if err := database.Create(&models.Message{
					ProtocolConvID: conversationID,
					ProtocolMsgID:  fmt.Sprintf("reply-%02d-%02d", i, j),
					ThreadID:       &parentID,
					Timestamp:      base.Add(time.Duration(100+i*40+j) * time.Minute),
				}).Error; err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	messages, err := (&App{}).GetMessagesForConversation(conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 50 {
		t.Fatalf("got %d timeline messages, want 50", len(messages))
	}
	for _, message := range messages {
		if message.ThreadID != nil && *message.ThreadID != message.ProtocolMsgID {
			t.Fatalf("thread reply %q leaked into the timeline page", message.ProtocolMsgID)
		}
	}
	for _, message := range messages {
		if message.ProtocolMsgID == "parent-54" && message.ThreadReplyCount != 40 {
			t.Fatalf("parent thread count = %d, want 40", message.ThreadReplyCount)
		}
	}
}
