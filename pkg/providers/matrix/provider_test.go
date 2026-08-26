package matrix

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"Loom/pkg/core"
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
