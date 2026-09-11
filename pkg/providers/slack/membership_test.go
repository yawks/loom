package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"gorm.io/gorm"
)

func membershipTestProvider(t *testing.T, handler http.HandlerFunc) *SlackProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	p := NewSlackProvider()
	p.config = core.ProviderConfig{"_instance_id": "slack-1", "slack_mode": "official"}
	p.client = slack.New("xoxp-test", slack.OptionAPIURL(server.URL+"/"))
	p.selfUserID = "Uself"
	return p
}

func TestOfficialMembershipSnapshot(t *testing.T) {
	var calls atomic.Int32
	var joined atomic.Bool
	p := membershipTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/users.conversations" {
			t.Errorf("unexpected API %s", r.URL.Path)
		}
		_ = r.ParseForm()
		if r.Form.Get("types") != "public_channel,private_channel,mpim" || r.Form.Get("exclude_archived") != "true" {
			t.Errorf("unexpected membership parameters: %v", r.Form)
		}
		if r.Form.Get("cursor") == "" {
			fmt.Fprint(w, `{"ok":true,"channels":[{"id":"Cmember"}],"response_metadata":{"next_cursor":"page2"}}`)
		} else if joined.Load() {
			fmt.Fprint(w, `{"ok":true,"channels":[{"id":"Cleft"},{"id":"Gmember"}]}`)
		} else {
			fmt.Fprint(w, `{"ok":true,"channels":[{"id":"Gmember"},{"id":"Carchived","is_archived":true}]}`)
		}
	})
	for id, want := range map[string]bool{"Cmember": true, "slack-1::Gmember": true, "Cleft": false, "Carchived": false, "Ddm": true, "Uuser": true} {
		got, err := p.canIngestConversation(context.Background(), id)
		if err != nil || got != want {
			t.Fatalf("%s: got %v, %v, want %v", id, got, err, want)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("snapshot fetched %d times", calls.Load())
	}
	joined.Store(true)
	p.handleMembershipChange("Uother")
	if got, _ := p.canIngestConversation(context.Background(), "Cleft"); got {
		t.Fatal("another user's join changed membership")
	}
	p.handleMembershipChange("Uself")
	if got, err := p.canIngestConversation(context.Background(), "Cleft"); !got || err != nil {
		t.Fatalf("join not refreshed: %v %v", got, err)
	}
	joined.Store(false)
	p.membership.expires = time.Time{}
	if got, err := p.canIngestConversation(context.Background(), "Cleft"); got || err != nil {
		t.Fatalf("expired membership accepted: %v %v", got, err)
	}
	if got, err := p.canIngestConversation(context.Background(), "slack-2::Cmember"); got || err == nil {
		t.Fatal("accepted foreign provider")
	}
	p.config["_instance_id"] = ""
	if got, err := p.canIngestConversation(context.Background(), "Cmember"); got || err == nil {
		t.Fatal("accepted empty provider")
	}
}

func TestOfficialMembershipFailureDoesNotUsePartialOrStaleSnapshot(t *testing.T) {
	var fail atomic.Bool
	var calls atomic.Int32
	p := membershipTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		if fail.Load() && r.Form.Get("cursor") != "" {
			fmt.Fprint(w, `{"ok":false,"error":"ratelimited"}`)
		} else if fail.Load() {
			fmt.Fprint(w, `{"ok":true,"channels":[{"id":"Cmember"}],"response_metadata":{"next_cursor":"page2"}}`)
		} else {
			fmt.Fprint(w, `{"ok":true,"channels":[{"id":"Cmember"}]}`)
		}
	})
	if got, err := p.canIngestConversation(context.Background(), "Cmember"); !got || err != nil {
		t.Fatal(got, err)
	}
	fail.Store(true)
	p.invalidateMemberships()
	for i := 0; i < 3; i++ {
		if got, err := p.canIngestConversation(context.Background(), "Cmember"); got || err == nil {
			t.Fatal("accepted incomplete or stale list")
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("failure was not cached: %d calls", calls.Load())
	}
	fail.Store(false)
	p.membership.expires = time.Time{}
	if got, err := p.canIngestConversation(context.Background(), "Cmember"); !got || err != nil {
		t.Fatal("membership did not recover", err)
	}
}

func TestOfficialMembershipRefreshDoesNotBlockCachedChecks(t *testing.T) {
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var calls atomic.Int32
	p := membershipTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fmt.Fprint(w, `{"ok":true,"channels":[{"id":"Cmember"}]}`)
			return
		}
		close(refreshStarted)
		<-releaseRefresh
		fmt.Fprint(w, `{"ok":true,"channels":[{"id":"Cmember"}]}`)
	})
	if allowed, err := p.canIngestConversation(context.Background(), "Cmember"); !allowed || err != nil {
		t.Fatal(allowed, err)
	}
	p.membership.mu.Lock()
	p.membership.expires = time.Time{}
	p.membership.mu.Unlock()
	refreshDone := make(chan struct{})
	go func() {
		_, _ = p.canIngestConversation(context.Background(), "Cmember")
		close(refreshDone)
	}()
	<-refreshStarted

	checkDone := make(chan bool, 1)
	go func() {
		allowed, _ := p.canIngestConversation(context.Background(), "Cmember")
		checkDone <- allowed
	}()
	select {
	case allowed := <-checkDone:
		if !allowed {
			t.Fatal("cached membership was not available during refresh")
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("cached membership check blocked behind network refresh")
	}
	close(releaseRefresh)
	<-refreshDone
}

func TestOfficialLeftChannelCannotBeRecreatedByEventsOrPolling(t *testing.T) {
	previousDB := db.DB
	t.Cleanup(func() { db.DB = previousDB })
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.DB = database
	if err := database.AutoMigrate(&models.Message{}, &models.LinkedAccount{}, &models.Conversation{}); err != nil {
		t.Fatal(err)
	}
	rows := []models.Message{
		{ProtocolConvID: "slack-1::Cleft", ProtocolMsgID: "1.0", Body: "old", Timestamp: time.Now().Add(-time.Hour)},
		{ProtocolConvID: "slack-2::Cforeign", ProtocolMsgID: "2.0", Body: "foreign", Timestamp: time.Now()},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	p := membershipTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/users.conversations" {
			t.Errorf("left channel triggered API: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"ok":true,"channels":[]}`)
	})
	p.handleMessageEvent(&slackevents.MessageEvent{Channel: "Cleft", Text: "new", TimeStamp: "3.0"})
	p.handleRTMMessageEvent(&slack.MessageEvent{Msg: slack.Msg{Channel: "Cleft", Text: "new", Timestamp: "4.0"}})
	p.syncConversationHistory("slack-1::Cleft")
	p.pollKnownConversationHistoryFallback(context.Background(), 0, 100)
	if err := p.incrementalSyncExistingConversations(context.Background(), time.Time{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Minute)
	if msgs, err := p.GetConversationHistory("slack-1::Cleft", 100, nil, &since); err != nil || len(msgs) != 0 {
		t.Fatal(msgs, err)
	}
	if msgs, err := p.getThreadReplies("Cleft", "1.0"); err != nil || len(msgs) != 0 {
		t.Fatal(msgs, err)
	}
	var stored []models.Message
	if err := database.Order("id").Find(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 || stored[0].Body != "old" || stored[1].Body != "foreign" {
		t.Fatalf("messages changed: %#v", stored)
	}
	var count int64
	database.Model(&models.Conversation{}).Count(&count)
	if count != 0 {
		t.Fatal("recreated conversation")
	}
	database.Model(&models.LinkedAccount{}).Count(&count)
	if count != 0 {
		t.Fatal("recreated contact")
	}
	if requests.Load() != 1 {
		t.Fatalf("expected one shared membership request, got %d", requests.Load())
	}
	for len(p.eventChan) > 0 {
		switch e := (<-p.eventChan).(type) {
		case core.MessageEvent:
			t.Fatalf("unexpected message event: %#v", e)
		case core.ContactStatusEvent:
			t.Fatalf("unexpected contact event: %#v", e)
		}
	}
}

func TestOfficialMembershipRefreshAfterLeaveAndClientReplacement(t *testing.T) {
	var left atomic.Bool
	p := membershipTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations.leave":
			left.Store(true)
			fmt.Fprint(w, `{"ok":true}`)
		case "/users.conversations":
			if left.Load() {
				fmt.Fprint(w, `{"ok":true,"channels":[]}`)
			} else {
				fmt.Fprint(w, `{"ok":true,"channels":[{"id":"Cmember"}]}`)
			}
		default:
			t.Errorf("unexpected API %s", r.URL.Path)
		}
	})
	if got, err := p.canIngestConversation(context.Background(), "Cmember"); !got || err != nil {
		t.Fatal(got, err)
	}
	if err := p.LeaveGroup("slack-1::Cmember"); err != nil {
		t.Fatal(err)
	}
	if got, err := p.canIngestConversation(context.Background(), "Cmember"); got || err != nil {
		t.Fatal("leave retained cached membership", err)
	}
	replacement := membershipTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true,"channels":[{"id":"Cmember"}]}`)
	})
	p.client = replacement.client
	if got, err := p.canIngestConversation(context.Background(), "Cmember"); !got || err != nil {
		t.Fatal("replacement reused old snapshot", err)
	}
}

func TestOfficialSearchDoesNotEmitLeftChannelMessages(t *testing.T) {
	p := membershipTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search.messages":
			fmt.Fprint(w, `{"ok":true,"messages":{"matches":[{"channel":{"id":"Cleft"},"ts":"1700000000.0","text":"ignored"}],"paging":{"pages":1}}}`)
		case "/users.conversations":
			fmt.Fprint(w, `{"ok":true,"channels":[]}`)
		default:
			t.Errorf("unexpected API %s", r.URL.Path)
		}
	})
	got, err := p.pollGlobalUpdates(context.Background(), time.Unix(1699999999, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(time.Unix(1700000000, 0)) {
		t.Fatal("ignored message did not advance search cursor", got)
	}
	if len(p.eventChan) != 0 {
		t.Fatal("left channel emitted an event")
	}
}
