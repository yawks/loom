package slack

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"
	"time"
)

func TestSlackSearchPollSinceKeepsIndexingOverlap(t *testing.T) {
	lastPoll := time.Date(2026, time.August, 29, 23, 20, 0, 0, time.UTC)
	want := lastPoll.Add(-10 * time.Minute)

	if got := slackSearchPollSince(lastPoll); !got.Equal(want) {
		t.Fatalf("slackSearchPollSince() = %s, want %s", got, want)
	}
}

func TestSlackFallbackParsesSQLiteAggregateTimestamp(t *testing.T) {
	raw := "2026-08-29 23:20:00.123456789+00:00"
	want := time.Date(2026, time.August, 29, 23, 20, 0, 123000000, time.UTC)

	got := time.UnixMilli(db.ParseTimeMillis(raw))
	if !got.Equal(want) {
		t.Fatalf("parsed fallback timestamp = %s, want %s", got, want)
	}
}

func TestSlackFallbackEmitsNewMainMessagesAndThreadReplies(t *testing.T) {
	threadID := "1788793712.361129"
	stored := []models.Message{
		{ProtocolMsgID: "already-known"},
		{ProtocolMsgID: "new-main"},
		{ProtocolMsgID: "new-reply", ThreadID: &threadID},
	}

	got := slackNewlyStoredMessages(stored, []string{"already-known"})
	if len(got) != 2 {
		t.Fatalf("newly stored messages = %d, want 2", len(got))
	}
	if got[0].ProtocolMsgID != "new-main" || got[1].ProtocolMsgID != "new-reply" {
		t.Fatalf("newly stored message IDs = [%s, %s], want [new-main, new-reply]", got[0].ProtocolMsgID, got[1].ProtocolMsgID)
	}
	if got[1].ThreadID == nil || *got[1].ThreadID != threadID {
		t.Fatalf("thread reply was not preserved: %#v", got[1].ThreadID)
	}
}
