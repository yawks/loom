package teams

import (
	"go.mau.fi/mautrix-teams/pkg/msteams"
	"testing"
	"time"
)

func TestUpcomingScheduledMessagesExcludesExpiredRemoteDrafts(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	drafts := []msteams.ScheduledDraftItem{
		{DraftID: "sent-but-retained", SendAt: now.Add(-time.Hour)},
		{DraftID: "due-now", SendAt: now},
		{DraftID: "missing-time"},
		{DraftID: "upcoming", SendAt: future, Content: "<p>Hello</p>"},
	}
	const conversationID = "teams-1::19:room@thread.v2"
	// Reopening can return the same expired drafts from the server.
	for range 2 {
		messages := upcomingScheduledMessages(drafts, "teams-1", conversationID, now)
		if len(messages) != 1 || messages[0].ID != "upcoming" {
			t.Fatalf("expected only future draft, got %+v", messages)
		}
		if messages[0].ProviderInstanceID != "teams-1" || messages[0].ProtocolConvID != conversationID || !messages[0].ScheduledAt.Equal(future) {
			t.Fatalf("unexpected canonical message: %+v", messages[0])
		}
	}
	if messages := upcomingScheduledMessages(drafts, "teams-1", conversationID, future); len(messages) != 0 {
		t.Fatalf("expired draft reappeared: %+v", messages)
	}
}
