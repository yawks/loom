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
	slackapi "github.com/slack-go/slack"
	"gorm.io/gorm"
)

func TestSlackConversationSyncRowsIncludesStaleConversationBeyondFirstFifty(t *testing.T) {
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
	provider.config = core.ProviderConfig{"_instance_id": "slack-1"}

	messages := make([]models.Message, 0, 53)
	for i := 0; i < 51; i++ {
		messages = append(messages, models.Message{
			ProtocolConvID: fmt.Sprintf("slack-1::C%03d", i),
			ProtocolMsgID:  fmt.Sprintf("recent-%03d", i),
			Timestamp:      time.Now().Add(-time.Duration(i) * time.Minute),
		})
	}
	messages = append(messages,
		models.Message{
			ProtocolConvID: "slack-1::C01J3DSNSKY",
			ProtocolMsgID:  "stale-target",
			Timestamp:      time.Now().AddDate(0, -2, 0),
		},
		models.Message{
			ProtocolConvID: "teams-1::C-foreign",
			ProtocolMsgID:  "foreign",
			Timestamp:      time.Now(),
		},
	)
	if err := db.DB.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}

	rows, err := provider.slackConversationSyncRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 52 {
		t.Fatalf("got %d Slack conversations, want 52", len(rows))
	}
	foundTarget := false
	for _, row := range rows {
		if row.ProtocolConvID == "teams-1::C-foreign" {
			t.Fatal("sync query included a conversation owned by another provider")
		}
		if row.ProtocolConvID == "slack-1::C01J3DSNSKY" {
			foundTarget = true
		}
	}
	if !foundTarget {
		t.Fatal("stale Slack conversation was excluded from incremental sync")
	}
}

func TestRefreshThreadRepliesResolvesNamespacedDMToChannelID(t *testing.T) {
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

	parentTS := fmt.Sprintf("%d.000001", time.Now().Add(-time.Hour).Unix())
	replyTS := fmt.Sprintf("%d.000002", time.Now().Unix())
	parent := models.Message{
		ProtocolConvID: "slack-1::U123",
		ProtocolMsgID:  parentTS,
		Timestamp:      time.Now().Add(-time.Hour),
	}
	if err := db.DB.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}

	httpClient := &http.Client{Transport: huddleRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		body := ""
		switch request.URL.Path {
		case "/conversations.open":
			if got := request.Form.Get("users"); got != "U123" {
				t.Errorf("conversations.open users = %q, want U123", got)
			}
			body = `{"ok":true,"channel":{"id":"D123"}}`
		case "/conversations.replies":
			if got := request.Form.Get("channel"); got != "D123" {
				t.Errorf("conversations.replies channel = %q, want D123", got)
			}
			body = fmt.Sprintf(`{"ok":true,"messages":[{"ts":%q,"user":"U123","text":"parent"},{"ts":%q,"thread_ts":%q,"user":"U456","text":"reply"}],"has_more":false}`, parentTS, replyTS, parentTS)
		case "/users.info":
			userID := request.Form.Get("user")
			body = fmt.Sprintf(`{"ok":true,"user":{"id":%q,"name":%q,"real_name":%q}}`, userID, userID, userID)
		default:
			t.Errorf("unexpected Slack API path %s", request.URL.Path)
			body = `{"ok":false,"error":"unexpected_endpoint"}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}

	provider := NewSlackProvider()
	provider.config = core.ProviderConfig{"_instance_id": "slack-1"}
	provider.client = slackapi.New("token", slackapi.OptionAPIURL("https://slack.test/"), slackapi.OptionHTTPClient(httpClient))
	provider.dmChannelCache["D123"] = "U123"

	if got := provider.refreshThreadReplies(context.Background(), "slack-1::U123"); got != 1 {
		t.Fatalf("refreshThreadReplies() = %d, want 1 new reply", got)
	}

	var stored models.Message
	if err := db.DB.Where("protocol_conv_id = ? AND protocol_msg_id = ?", "slack-1::U123", replyTS).First(&stored).Error; err != nil {
		t.Fatalf("thread reply was not stored: %v", err)
	}
	if stored.ThreadID == nil || *stored.ThreadID != parentTS {
		t.Fatalf("stored thread ID = %#v, want %s", stored.ThreadID, parentTS)
	}
}

func TestPartitionSlackBootstrapMessagesUsesLastReadForThreadReplies(t *testing.T) {
	threadID := "1643297619.008800"
	readThreadReply := models.Message{
		ProtocolMsgID: "1643300921.010000",
		ThreadID:      &threadID,
		Timestamp:     time.Unix(1643300921, 10_000_000),
	}
	unreadThreadReply := models.Message{
		ProtocolMsgID: "1647877680.000001",
		ThreadID:      &threadID,
		Timestamp:     time.Unix(1647877680, 1_000),
	}

	read, unread := partitionSlackBootstrapMessages(
		[]models.Message{readThreadReply, unreadThreadReply},
		"1647877678.473649",
	)
	if len(read) != 1 || read[0].ProtocolMsgID != readThreadReply.ProtocolMsgID {
		t.Fatalf("read bootstrap messages = %#v, want old thread reply", read)
	}
	if len(unread) != 1 || unread[0].ProtocolMsgID != unreadThreadReply.ProtocolMsgID {
		t.Fatalf("unread bootstrap messages = %#v, want reply after last_read", unread)
	}
}

func TestSlackConversationBootstrapLookbackIsBounded(t *testing.T) {
	if slackConversationBootstrapLookback != 30*24*time.Hour {
		t.Fatalf("bootstrap lookback = %s, want 30 days", slackConversationBootstrapLookback)
	}
}

func TestThreadRepliesKeepsFirstReplyOnFilteredAndPaginatedResponses(t *testing.T) {
	p := membershipTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/conversations.info":
			fmt.Fprint(w, `{"ok":true,"channel":{"id":"D123","is_im":true,"user":"U123"}}`)
		case "/conversations.replies":
			if r.Form.Get("cursor") == "" {
				fmt.Fprint(w, `{"ok":true,"messages":[{"ts":"200.000001","thread_ts":"100.000001","text":"first"}],"has_more":true,"response_metadata":{"next_cursor":"page2"}}`)
			} else {
				fmt.Fprint(w, `{"ok":true,"messages":[{"ts":"201.000001","thread_ts":"100.000001","text":"second"}],"has_more":false}`)
			}
		default:
			t.Errorf("unexpected API %s", r.URL.Path)
		}
	})
	replies, err := p.getThreadReplies("D123", "100.000001", time.Unix(199, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 2 || replies[0].ProtocolMsgID != "200.000001" || replies[1].ProtocolMsgID != "201.000001" {
		t.Fatalf("replies = %#v, want both replies without a parent", replies)
	}
}

func TestHistoryFallbackRecoversReplyOlderThanMainCursor(t *testing.T) {
	previousDB := db.DB
	t.Cleanup(func() { db.DB = previousDB })
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.DB = database
	if err := database.AutoMigrate(&models.Message{}, &models.LinkedAccount{}, &models.Conversation{}, &models.MetaContact{}, &models.Reaction{}, &models.MessageReceipt{}); err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("CREATE INDEX IF NOT EXISTS idx_messages_deleted_conv ON messages(deleted_at, protocol_conv_id)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureMessagePollingIndices(database); err != nil {
		t.Fatal(err)
	}
	if err := db.ContactStore.Load(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	parentTS := fmt.Sprintf("%d.000001", now.Add(-time.Hour).Unix())
	previousReplyTS := fmt.Sprintf("%d.000001", now.Add(-30*time.Minute).Unix())
	replyTS := fmt.Sprintf("%d.000001", now.Add(-20*time.Minute).Unix())
	rows := []models.Message{
		{ProtocolConvID: "slack-1::U123", ProtocolMsgID: parentTS, Timestamp: now.Add(-time.Hour)},
		{ProtocolConvID: "slack-1::U123", ProtocolMsgID: previousReplyTS, ThreadID: &parentTS, Timestamp: now.Add(-30 * time.Minute)},
		{ProtocolConvID: "slack-1::U123", ProtocolMsgID: "latest", Timestamp: now.Add(-5 * time.Minute)},
		{ProtocolConvID: "slack-2::Uforeign", ProtocolMsgID: "foreign", Body: "untouched", Timestamp: now},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	p := membershipTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/conversations.open":
			if r.Form.Get("users") != "U123" {
				t.Errorf("foreign conversation fetched: %v", r.Form)
			}
			fmt.Fprint(w, `{"ok":true,"channel":{"id":"D123"}}`)
		case "/conversations.history":
			fmt.Fprint(w, `{"ok":true,"messages":[],"has_more":false}`)
		case "/conversations.replies":
			if r.Form.Get("channel") != "D123" {
				t.Errorf("wrong channel: %v", r.Form)
			}
			if r.Form.Get("ts") == parentTS {
				if r.Form.Get("oldest") == "" {
					t.Error("missing existing reply cursor")
				}
				fmt.Fprintf(w, `{"ok":true,"messages":[{"ts":%q,"thread_ts":%q,"text":"recovered"}],"has_more":false}`, replyTS, parentTS)
			} else {
				fmt.Fprint(w, `{"ok":true,"messages":[],"has_more":false}`)
			}
		case "/users.info":
			fmt.Fprint(w, `{"ok":true,"user":{"id":"Uself","name":"self"}}`)
		default:
			t.Errorf("unexpected API %s", r.URL.Path)
			fmt.Fprint(w, `{"ok":false,"error":"unexpected"}`)
		}
	})
	p.dmChannelCache["D123"] = "U123"
	p.pollKnownConversationHistoryFallback(context.Background(), 0, 100)
	var stored models.Message
	if err := database.Where("protocol_msg_id = ?", replyTS).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ProtocolConvID != "slack-1::U123" {
		t.Fatalf("wrong conversation: %s", stored.ProtocolConvID)
	}
	select {
	case event := <-p.eventChan:
		batch, ok := event.(core.MessageBatchEvent)
		if !ok || !batch.ForceUnread || len(batch.Messages) != 1 || batch.Messages[0].ProtocolMsgID != replyTS {
			t.Fatalf("unexpected event %#v", event)
		}
	default:
		t.Fatal("missing unread event")
	}
	select {
	case event := <-p.eventChan:
		t.Fatalf("duplicate event: %#v", event)
	default:
	}
	var foreign models.Message
	if err := database.First(&foreign, rows[3].ID).Error; err != nil || foreign.Body != "untouched" {
		t.Fatalf("foreign row changed: %#v %v", foreign, err)
	}
}
