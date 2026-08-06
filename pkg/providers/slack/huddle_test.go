package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type huddleRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn huddleRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestIsSlackHuddleSubtype(t *testing.T) {
	for _, subtype := range []string{"sh_room_created", "huddle_thread"} {
		if !isSlackHuddleSubtype(subtype) {
			t.Errorf("isSlackHuddleSubtype(%q) = false, want true", subtype)
		}
	}
	for _, subtype := range []string{"", "message_changed", "channel_join"} {
		if isSlackHuddleSubtype(subtype) {
			t.Errorf("isSlackHuddleSubtype(%q) = true, want false", subtype)
		}
	}
}

func TestHuddleLinkPrefersJoinURL(t *testing.T) {
	provider := &SlackProvider{teamID: "T123"}
	joinURL := "https://acme.slack.com/huddle/C123/abc"

	gotURL, gotAction := provider.huddleLink("<"+joinURL+"|Join huddle>", "C123")
	if gotURL != joinURL || gotAction != "join" {
		t.Fatalf("huddleLink() = (%q, %q), want (%q, %q)", gotURL, gotAction, joinURL, "join")
	}
}

func TestHuddleLinkFallsBackToWebConversation(t *testing.T) {
	provider := &SlackProvider{teamID: "T123"}

	gotURL, gotAction := provider.huddleLink("", "C456")
	if gotURL != "https://app.slack.com/client/T123/C456" || gotAction != "open" {
		t.Fatalf("huddleLink() = (%q, %q), want Slack conversation link with open action", gotURL, gotAction)
	}
}

func TestHuddleLinkWithoutWorkspace(t *testing.T) {
	provider := &SlackProvider{}

	gotURL, gotAction := provider.huddleLink("", "C456")
	if gotURL != "" || gotAction != "" {
		t.Fatalf("huddleLink() = (%q, %q), want empty values", gotURL, gotAction)
	}
}

func TestFetchAndApplyEndedHuddle(t *testing.T) {
	client := &http.Client{Transport: huddleRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("Authorization = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"ok":true,"messages":[{"ts":"100.000001","subtype":"huddle_thread","room":{"date_start":100,"date_end":225,"has_ended":true,"is_dm_call":true,"participant_history":["U1","U2"]}}]}`)),
			Request: r,
		}, nil
	})}

	provider := NewSlackProvider()
	provider.apiHTTPClient = client
	provider.authToken = "token"
	provider.apiBaseURL = "https://slack.test/api"
	room, err := provider.fetchHuddleRoom(context.Background(), "D1", "100.000001")
	if err != nil {
		t.Fatal(err)
	}
	message := models.Message{CallType: "incoming_call"}
	applySlackHuddleRoom(&message, room)
	if message.CallType != "call_ended" || message.CallOutcome != "CONNECTED" {
		t.Fatalf("ended huddle = type %q, outcome %q", message.CallType, message.CallOutcome)
	}
	if message.CallDurationSecs == nil || *message.CallDurationSecs != 125 {
		t.Fatalf("duration = %v, want 125", message.CallDurationSecs)
	}
	if message.CallParticipants != `["U1","U2"]` {
		t.Fatalf("participants = %s", message.CallParticipants)
	}
}

func TestApplyActiveHuddleDoesNotInventAnEnd(t *testing.T) {
	message := models.Message{CallType: "incoming_group_call"}
	applySlackHuddleRoom(&message, &slackHuddleRoom{DateStart: 100, HasEnded: false})
	if message.CallType != "incoming_group_call" {
		t.Fatalf("active huddle type = %q", message.CallType)
	}
	if message.CallDurationSecs != nil {
		t.Fatalf("active huddle duration = %v, want nil", message.CallDurationSecs)
	}
}

func TestHandleHuddleRoomUpdatePersistsDuration(t *testing.T) {
	previousDB := db.DB
	t.Cleanup(func() { db.DB = previousDB })
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.DB = database
	if err := db.DB.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}

	provider := NewSlackProvider()
	provider.config = core.ProviderConfig{"_instance_id": "slack-test"}
	startedAt := time.Unix(100, 0)
	start := models.Message{
		ProtocolConvID: core.BuildConvID("slack-test", "C1"),
		ProtocolMsgID:  "100.000001",
		Timestamp:      startedAt,
		CallType:       "incoming_group_call",
	}
	if err := db.DB.Create(&start).Error; err != nil {
		t.Fatal(err)
	}

	room := &slackHuddleRoom{DateStart: 100, DateEnd: 280, HasEnded: true, ParticipantHistory: []string{"U1"}}
	if !provider.handleHuddleRoomUpdate("C1", start.ProtocolMsgID, room) {
		t.Fatal("handleHuddleRoomUpdate returned false")
	}
	var stored models.Message
	if err := db.DB.First(&stored, start.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CallType != "call_ended" || stored.CallDurationSecs == nil || *stored.CallDurationSecs != 180 {
		t.Fatalf("stored call = type %q, duration %v", stored.CallType, stored.CallDurationSecs)
	}
	if !stored.Timestamp.Equal(startedAt) {
		t.Fatalf("start timestamp changed to %s", stored.Timestamp)
	}
}
