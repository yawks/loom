package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/slack-go/slack"
	"gorm.io/gorm"
)

func idlePollingDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.AutoMigrate(&models.Message{}, &models.LinkedAccount{}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureMessagePollingIndices(database); err != nil {
		t.Fatal(err)
	}
	// Idempotence matters when opening an existing installation.
	if err := db.EnsureMessagePollingIndices(database); err != nil {
		t.Fatal(err)
	}
	return database
}

func assertPollingIndex(t *testing.T, database, query *gorm.DB, index string, destination any) {
	t.Helper()
	statement := query.Session(&gorm.Session{DryRun: true}).Find(destination).Statement
	var plan []struct{ Detail string }
	if err := database.Raw("EXPLAIN QUERY PLAN "+statement.SQL.String(), statement.Vars...).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	for _, step := range plan {
		if strings.Contains(step.Detail, "USING INDEX "+index) || strings.Contains(step.Detail, "USING COVERING INDEX "+index) {
			return
		}
	}
	t.Fatalf("polling query must use %s, got %#v", index, plan)
}

func TestSlackFallbackUsesMainIndexAndFreshScopedWatermarks(t *testing.T) {
	database := idlePollingDatabase(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	root, empty := "root", ""
	rows := []models.Message{
		{ProtocolConvID: "slack-1::C1", ProtocolMsgID: root, ThreadID: &root, Timestamp: now.Add(-time.Hour)},
		{ProtocolConvID: "slack-1::C1", ProtocolMsgID: "reply", ThreadID: &root, Timestamp: now},
		{ProtocolConvID: "slack-1::C2", ProtocolMsgID: "empty-thread", ThreadID: &empty, Timestamp: now.Add(-time.Minute)},
		{ProtocolConvID: "slack-1::C3", ProtocolMsgID: "null-thread", Timestamp: now.Add(-2 * time.Minute)},
		{ProtocolConvID: "slack-2::C1", ProtocolMsgID: "foreign", Timestamp: now},
		{ProtocolConvID: "slack-1::deleted", ProtocolMsgID: "deleted", Timestamp: now, DeletedAt: gorm.DeletedAt{Time: now, Valid: true}},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	assertPollingIndex(t, database, slackFallbackMessageQuery(database, "slack-1"), "idx_messages_conv_main", &[]slackFallbackConversation{})
	conversations, err := slackFallbackConversations(database, "slack-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(conversations) != 3 || conversations[0].ProtocolConvID != "slack-1::C2" {
		t.Fatalf("unexpected conversations: %#v", conversations)
	}
	if got := db.ParseTimeMillis(conversations[2].LastTimestamp); got != rows[0].Timestamp.UnixMilli() {
		t.Fatalf("reply advanced main watermark: %d", got)
	}
	newMain := models.Message{ProtocolConvID: "slack-1::C1", ProtocolMsgID: "new", Timestamp: now.Add(time.Minute)}
	if err := database.Create(&newMain).Error; err != nil {
		t.Fatal(err)
	}
	conversations, err = slackFallbackConversations(database, "slack-1")
	if err != nil || len(conversations) != 3 || conversations[0].ProtocolConvID != "slack-1::C1" || db.ParseTimeMillis(conversations[0].LastTimestamp) != newMain.Timestamp.UnixMilli() {
		t.Fatalf("new message not immediately visible: %#v, %v", conversations, err)
	}
	conversations, err = slackFallbackConversations(database, "")
	if err != nil || len(conversations) != 0 {
		t.Fatalf("empty scope: %#v, %v", conversations, err)
	}
}

func TestSlackActiveHuddlePollingUsesIndexAndIsolatesProviders(t *testing.T) {
	database := idlePollingDatabase(t)
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous })
	now := time.Now().UTC()
	rows := []models.Message{
		{ProtocolConvID: "slack-1::C1", ProtocolMsgID: "100.000001", CallType: "incoming_call", Timestamp: now},
		{ProtocolConvID: "slack-1::C2", ProtocolMsgID: "200.000001", CallType: "incoming_group_call", Timestamp: now},
		{ProtocolConvID: "slack-2::C3", ProtocolMsgID: "foreign", CallType: "incoming_call", Body: "untouched", Timestamp: now},
		{ProtocolConvID: "slack-1::C4", ProtocolMsgID: "old", CallType: "incoming_call", Timestamp: now.Add(-25 * time.Hour)},
		{ProtocolConvID: "slack-1::C5", ProtocolMsgID: "ended", CallType: "call_ended", Timestamp: now},
		{ProtocolConvID: "slack-1::C6", ProtocolMsgID: "deleted", CallType: "incoming_call", Timestamp: now, DeletedAt: gorm.DeletedAt{Time: now, Valid: true}},
		{ProtocolConvID: "slack-1::C7", ProtocolMsgID: "regular", Timestamp: now},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	assertPollingIndex(t, database, slackActiveHuddles(database, "slack-1", now.Add(-24*time.Hour)), "idx_messages_active_calls", &[]models.Message{})
	seen := map[string]bool{}
	p := NewSlackProvider()
	p.client = slack.New("token")
	p.config = core.ProviderConfig{"_instance_id": "slack-1"}
	p.apiBaseURL, p.authToken = "https://slack.test/api", "token"
	p.apiHTTPClient = &http.Client{Transport: huddleRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		channel, ts := r.URL.Query().Get("channel"), r.URL.Query().Get("latest")
		seen[channel] = true
		if channel != "C1" && channel != "C2" {
			t.Errorf("polled unrelated conversation %q", channel)
		}
		body := fmt.Sprintf(`{"ok":true,"messages":[{"ts":%q,"subtype":"huddle_thread","room":{"date_start":100,"date_end":225,"has_ended":true}}]}`, ts)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	p.pollActiveHuddles(context.Background())
	if len(seen) != 2 || !seen["C1"] || !seen["C2"] {
		t.Fatalf("polled channels: %v", seen)
	}
	for i, row := range rows {
		var stored models.Message
		if err := database.Unscoped().First(&stored, row.ID).Error; err != nil {
			t.Fatal(err)
		}
		want := row.CallType
		if i < 2 {
			want = "call_ended"
		}
		if stored.CallType != want || stored.Body != row.Body {
			t.Fatalf("unexpected mutation of %s: %#v", row.ProtocolMsgID, stored)
		}
	}
	if len(p.eventChan) != 4 {
		t.Fatalf("expected message and status events for both ended calls, got %d", len(p.eventChan))
	}
	for len(p.eventChan) > 0 {
		event := <-p.eventChan
		switch e := event.(type) {
		case core.MessageEvent:
			if e.InstanceID != "slack-1" || !strings.HasPrefix(e.Message.ProtocolConvID, "slack-1::") {
				t.Fatalf("foreign event: %#v", e)
			}
		case core.ContactStatusEvent:
			if e.InstanceID != "slack-1" {
				t.Fatalf("foreign event: %#v", e)
			}
		}
	}
	p.config = core.ProviderConfig{}
	clear(seen)
	p.pollActiveHuddles(context.Background())
	if len(seen) != 0 {
		t.Fatalf("empty instance polled: %v", seen)
	}
}
