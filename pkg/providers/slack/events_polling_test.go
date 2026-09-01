package slack

import (
	"Loom/pkg/db"
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
