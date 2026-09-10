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
