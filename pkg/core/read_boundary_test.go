package core

import (
	"testing"
	"time"

	"Loom/pkg/models"
)

func TestSplitRecoveredMessagesByOwnMessage(t *testing.T) {
	base := time.Unix(100, 0)
	messages := []models.Message{
		{ProtocolMsgID: "before", Timestamp: base},
		{ProtocolMsgID: "mine", Timestamp: base.Add(time.Second), IsFromMe: true},
		{ProtocolMsgID: "after", Timestamp: base.Add(2 * time.Second)},
	}
	read, unread := SplitRecoveredMessagesByOwnActivity(messages, "self")
	if len(read) != 2 || read[1].ProtocolMsgID != "mine" || len(unread) != 1 || unread[0].ProtocolMsgID != "after" {
		t.Fatalf("unexpected split: read=%v unread=%v", read, unread)
	}
}

func TestSplitRecoveredMessagesByOwnReaction(t *testing.T) {
	base := time.Unix(100, 0)
	messages := []models.Message{
		{ProtocolMsgID: "reacted", Timestamp: base, Reactions: []models.Reaction{{UserID: "SELF", CreatedAt: base.Add(2 * time.Second)}}},
		{ProtocolMsgID: "older-than-reaction", Timestamp: base.Add(time.Second)},
		{ProtocolMsgID: "after", Timestamp: base.Add(3 * time.Second)},
	}
	read, unread := SplitRecoveredMessagesByOwnActivity(messages, "self")
	if len(read) != 2 || read[1].ProtocolMsgID != "older-than-reaction" || len(unread) != 1 || unread[0].ProtocolMsgID != "after" {
		t.Fatalf("unexpected split: read=%v unread=%v", read, unread)
	}
}

func TestSplitRecoveredMessagesWithoutOwnActivity(t *testing.T) {
	base := time.Unix(100, 0)
	messages := []models.Message{{ProtocolMsgID: "one", Timestamp: base}, {ProtocolMsgID: "two", Timestamp: base.Add(time.Second)}}
	read, unread := SplitRecoveredMessagesByOwnActivity(messages, "self")
	if len(read) != 0 || len(unread) != 2 {
		t.Fatalf("unexpected split: read=%v unread=%v", read, unread)
	}
}
