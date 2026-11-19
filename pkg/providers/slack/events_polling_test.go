package slack

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
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

func TestSlackFallbackListsOnlyCurrentProviderConversations(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}, &models.LinkedAccount{}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureMessagePollingIndices(database); err != nil {
		t.Fatal(err)
	}
	messages := []models.Message{
		{ProtocolConvID: "slack-1::U1", ProtocolMsgID: "slack-message", Timestamp: time.Now()},
		{ProtocolConvID: "teams-1::19:chat", ProtocolMsgID: "teams-message", Timestamp: time.Now()},
		{ProtocolConvID: "whatsapp-1::123@s.whatsapp.net", ProtocolMsgID: "whatsapp-message", Timestamp: time.Now()},
	}
	if err := database.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}
	accounts := []models.LinkedAccount{
		{ProviderInstanceID: "slack-1", Protocol: "slack", UserID: "C-empty", IsGroup: true},
		{ProviderInstanceID: "slack-1", Protocol: "slack", UserID: "U-active", Extra: `{"has_conversation":true}`},
		{ProviderInstanceID: "slack-1", Protocol: "slack", UserID: "U-directory-only"},
		{ProviderInstanceID: "teams-1", Protocol: "teams", UserID: "C-foreign", IsGroup: true},
	}
	if err := database.Create(&accounts).Error; err != nil {
		t.Fatal(err)
	}

	conversations, err := slackFallbackConversations(database, "slack-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(conversations) != 3 {
		t.Fatalf("fallback conversations = %#v, want message-backed plus two empty Slack conversations", conversations)
	}
	got := make(map[string]bool, len(conversations))
	for _, conversation := range conversations {
		got[conversation.ProtocolConvID] = true
	}
	for _, want := range []string{"slack-1::U1", "slack-1::C-empty", "slack-1::U-active"} {
		if !got[want] {
			t.Errorf("fallback conversations missing %s: %#v", want, conversations)
		}
	}
	for _, excluded := range []string{"slack-1::U-directory-only", "teams-1::C-foreign"} {
		if got[excluded] {
			t.Errorf("fallback conversations included %s", excluded)
		}
	}
}

func TestSlackFallbackEmitsNewMainMessagesAndThreadReplies(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Hour)
	threadID := "1788793712.361129"
	known := models.Message{ProtocolConvID: "slack-1::C1", ProtocolMsgID: "already-known", Timestamp: since.Add(time.Minute), ThreadID: &threadID}
	if err := database.Create(&known).Error; err != nil {
		t.Fatal(err)
	}
	stored := []models.Message{
		{ProtocolConvID: "slack-1::C1", ProtocolMsgID: "new-main", Timestamp: since.Add(2 * time.Minute)},
		{ProtocolConvID: "slack-1::C1", ProtocolMsgID: "new-reply", ThreadID: &threadID, Timestamp: since.Add(3 * time.Minute)},
		{ProtocolConvID: "teams-1::C1", ProtocolMsgID: "foreign", Timestamp: since.Add(4 * time.Minute)},
		{ProtocolConvID: "slack-1::C2", ProtocolMsgID: "other-conversation", Timestamp: since.Add(4 * time.Minute)},
		{ProtocolConvID: "slack-1::C1", ProtocolMsgID: "old-backfill", Timestamp: since.Add(-time.Minute)},
	}
	if err := database.Create(&stored).Error; err != nil {
		t.Fatal(err)
	}
	got, err := slackNewlyStoredMessages(database, "slack-1", "slack-1::C1", since, known.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("newly stored messages = %d, want 2", len(got))
	}
	if got[0].ProtocolMsgID != "new-main" || got[1].ProtocolMsgID != "new-reply" {
		t.Fatalf("newly stored message IDs = [%s, %s], want [new-main, new-reply]", got[0].ProtocolMsgID, got[1].ProtocolMsgID)
	}
	if got[1].ThreadID == nil || *got[1].ThreadID != threadID {
		t.Fatalf("thread reply was not preserved: %#v", got[1].ThreadID)
	}
	// Verify that an idle cycle seeks past existing replies instead of scanning
	// their timestamps. Capture the actual helper query, including provider scope.
	var statement *gorm.Statement
	if err := database.Callback().Query().After("gorm:query").Register("test:capture_new_messages", func(tx *gorm.DB) {
		statement = tx.Statement
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := slackNewlyStoredMessages(database.Session(&gorm.Session{DryRun: true}), "slack-1", "slack-1::C1", since, known.ID); err != nil {
		t.Fatal(err)
	}
	var plan []struct{ Detail string }
	if err := database.Raw("EXPLAIN QUERY PLAN "+statement.SQL.String(), statement.Vars...).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	seeksID := false
	for _, step := range plan {
		seeksID = seeksID || strings.Contains(step.Detail, "rowid>?")
	}
	if !seeksID {
		t.Fatalf("polling does not seek by row ID: %v; %s; %v", plan, statement.SQL.String(), statement.Vars)
	}
	got, err = slackNewlyStoredMessages(database, "", "slack-1::C1", since, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty instance must fail closed: %v, %v", got, err)
	}
}

func TestSlackFallbackDoesNotRediscoverMessagesMovedFromLegacyDMAlias(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-60 * 24 * time.Hour)
	rows := []models.Message{
		{ProtocolConvID: "slack-1::U123", ProtocolMsgID: "old-main", Timestamp: since},
		{ProtocolConvID: "slack-1::D123", ProtocolMsgID: "already-stored", Timestamp: since.Add(time.Hour)},
		{ProtocolConvID: "slack-2::U123", ProtocolMsgID: "foreign", Timestamp: time.Now()},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	boundary, err := slackStoredMessageHighWatermark(database, "slack-1")
	if err != nil || boundary != rows[1].ID {
		t.Fatalf("provider boundary = %d, %v; want %d", boundary, err, rows[1].ID)
	}
	// History normalization updates this old row, but must not announce it as
	// recovered unread activity merely because it now belongs to the user ID.
	if err := database.Model(&models.Message{}).Where("id = ?", rows[1].ID).
		Update("protocol_conv_id", "slack-1::U123").Error; err != nil {
		t.Fatal(err)
	}
	fresh := models.Message{ProtocolConvID: "slack-1::U123", ProtocolMsgID: "actually-new", Timestamp: time.Now()}
	if err := database.Create(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	got, err := slackNewlyStoredMessages(database, "slack-1", "slack-1::U123", since, boundary)
	if err != nil || len(got) != 1 || got[0].ProtocolMsgID != fresh.ProtocolMsgID {
		t.Fatalf("recovered messages = %#v, %v; want only actually-new", got, err)
	}
	var foreign models.Message
	if err := database.First(&foreign, rows[2].ID).Error; err != nil || foreign.ProtocolConvID != rows[2].ProtocolConvID {
		t.Fatalf("foreign row changed: %#v, %v", foreign, err)
	}
	if boundary, err := slackStoredMessageHighWatermark(database, ""); err != nil || boundary != 0 {
		t.Fatalf("empty instance must fail closed: %d, %v", boundary, err)
	}
}
