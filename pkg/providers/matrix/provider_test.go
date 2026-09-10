package matrix

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
)

func TestCapabilitiesMatchImplementedMatrixFeatures(t *testing.T) {
	caps := NewProvider().GetCapabilities()
	if !caps.SupportsThreads || !caps.SupportsReactions || !caps.SupportsTypingIndicator || !caps.SupportsReadReceipts {
		t.Fatalf("expected Matrix messaging capabilities, got %+v", caps)
	}
	if caps.SupportsQRCodeAuth || caps.SupportsPinConversation || caps.SupportsGroupPhoto || caps.SupportsGroupAdminRoles {
		t.Fatalf("provider advertises an unsupported capability: %+v", caps)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestRequestsUseBearerAuthentication(t *testing.T) {
	p := NewProvider()
	p.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/_matrix/client/v3/account/whoami" {
			t.Errorf("unexpected path %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("missing bearer token")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"user_id":"@alice:example.org"}`))}, nil
	})}
	if err := p.Init(core.ProviderConfig{"homeserver": "https://matrix.example.org", "access_token": "secret", "_instance_id": "matrix-1"}); err != nil {
		t.Fatal(err)
	}
	var who struct {
		UserID string `json:"user_id"`
	}
	if err := p.do(context.Background(), http.MethodGet, "/account/whoami", nil, nil, &who); err != nil {
		t.Fatal(err)
	}
	if who.UserID != "@alice:example.org" {
		t.Fatalf("unexpected user: %s", who.UserID)
	}
}

func TestPasswordLoginDiscoversHomeserverAndReplacesPassword(t *testing.T) {
	p := NewProvider()
	p.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.String() {
		case "https://example.org/.well-known/matrix/client":
			return jsonResponse(`{"m.homeserver":{"base_url":"https://matrix.example.org/"}}`), nil
		case "https://matrix.example.org/_matrix/client/v3/login":
			if request.Method != http.MethodPost {
				t.Errorf("unexpected login method %s", request.Method)
			}
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), `"password":"correct horse"`) {
				t.Errorf("login body does not contain password: %s", body)
			}
			return jsonResponse(`{"user_id":"@alice:example.org","access_token":"generated","device_id":"LOOM"}`), nil
		default:
			t.Fatalf("unexpected request %s", request.URL)
			return nil, nil
		}
	})}
	if err := p.Init(core.ProviderConfig{"user_id": "@alice:example.org", "password": "correct horse", "_instance_id": "matrix-1"}); err != nil {
		t.Fatal(err)
	}
	if err := p.loginWithPassword(context.Background()); err != nil {
		t.Fatal(err)
	}
	config := p.GetConfig()
	if _, exists := config["password"]; exists {
		t.Fatal("password must be removed after token exchange")
	}
	if token, _ := config.GetString("access_token"); token != "generated" {
		t.Fatalf("unexpected token %q", token)
	}
	if homeserver, _ := config.GetString("homeserver"); homeserver != "https://matrix.example.org" {
		t.Fatalf("unexpected homeserver %q", homeserver)
	}
}

func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestConversationInvitationsCanBeListedAcceptedAndDeclined(t *testing.T) {
	p := NewProvider()
	p.instanceID = "matrix-1"
	p.userID = "@alice:example.org"
	p.homeserver = "https://matrix.example.org"
	p.accessToken = "token"
	var posts []string
	p.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/_matrix/client/v3/sync":
			return jsonResponse(`{"rooms":{"invite":{"!room:example.org":{"invite_state":{"events":[{"type":"m.room.name","content":{"name":"Projet"}},{"type":"m.room.member","sender":"@bob:example.org","state_key":"@bob:example.org","content":{"membership":"join","displayname":"Bob"}},{"type":"m.room.member","sender":"@bob:example.org","state_key":"@alice:example.org","content":{"membership":"invite"}}]}}}}}`), nil
		case request.Method == http.MethodGet && request.URL.Path == "/_matrix/client/v3/joined_rooms":
			return jsonResponse(`{"joined_rooms":["!room:example.org"]}`), nil
		case request.Method == http.MethodPost:
			posts = append(posts, request.URL.Path)
			return jsonResponse(`{}`), nil
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/state"):
			return jsonResponse(`[{"type":"m.room.name","content":{"name":"Projet"}}]`), nil
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})}

	invitations, err := p.ListConversationInvitations()
	if err != nil {
		t.Fatal(err)
	}
	if len(invitations) != 1 || invitations[0].ConversationID != "matrix-1::!room:example.org" || invitations[0].Name != "Projet" || invitations[0].InvitedByName != "Bob" {
		t.Fatalf("unexpected invitations: %+v", invitations)
	}
	if err := p.AcceptConversationInvitation(invitations[0].ConversationID); err != nil {
		t.Fatal(err)
	}
	if err := p.DeclineConversationInvitation(invitations[0].ConversationID); err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 || !strings.Contains(posts[0], "/join/") || !strings.HasSuffix(posts[1], "/leave") {
		t.Fatalf("unexpected invitation actions: %v", posts)
	}
}

func TestSentMessageIsPersistedForRecentConversations(t *testing.T) {
	if err := db.InitMockDatabase(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if sqlDB, err := db.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
		db.DB = nil
	}()
	meta := models.MetaContact{DisplayName: "Bob"}
	if err := db.DB.Create(&meta).Error; err != nil {
		t.Fatal(err)
	}
	account := models.LinkedAccount{MetaContactID: meta.ID, Protocol: "matrix", ProviderInstanceID: "matrix-2", UserID: "@bob:matrix.org", Username: "Bob"}
	if err := db.DB.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	conversationID := "matrix-2::!MHvONIbUofiwKosHzh:matrix.org"
	conversation := models.Conversation{LinkedAccountID: account.ID, ProtocolConvID: conversationID}
	if err := db.DB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	p := NewProvider()
	p.config = core.ProviderConfig{"_instance_id": "matrix-2"}
	p.instanceID = "matrix-2"
	p.userID = "@alice:matrix.org"
	p.selfName = "Alice"
	p.selfAvatarURL = "https://matrix.example.org/avatar.png"
	p.homeserver = "https://matrix.example.org"
	p.accessToken = "token"
	p.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.Contains(request.URL.Path, "/rooms/!MHvONIbUofiwKosHzh:matrix.org/send/m.room.message/") {
			t.Fatalf("unexpected send path %s", request.URL.Path)
		}
		return jsonResponse(`{"event_id":"$sent"}`), nil
	})}
	message, err := p.SendMessage(conversationID, "hello", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var stored models.Message
	if err := db.DB.Where("protocol_msg_id = ?", "$sent").First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ProtocolConvID != conversationID || stored.ConversationID != conversation.ID || stored.Body != "hello" {
		t.Fatalf("unexpected stored message: %+v", stored)
	}
	if message.SenderName != "Alice" || message.SenderAvatarURL != "https://matrix.example.org/avatar.png" {
		t.Fatalf("outgoing self profile was not applied: %+v", message)
	}
	if time.Since(message.Timestamp) > time.Second {
		t.Fatalf("unexpected message timestamp %v", message.Timestamp)
	}
}
