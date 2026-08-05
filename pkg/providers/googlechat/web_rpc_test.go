package googlechat

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"Loom/pkg/core"
)

func TestGoogleChatScheduledMessageCapabilities(t *testing.T) {
	provider := NewGoogleChatProvider()
	var _ core.ScheduledMessageProvider = provider

	caps := provider.GetCapabilities()
	if !caps.SupportsScheduledMessages || !caps.SupportsListScheduledMessages {
		t.Fatalf("expected Google Chat to support scheduled messages capabilities, got %+v", caps)
	}
}

func TestGoogleChatWebClientSAPISIDHash(t *testing.T) {
	client := newGoogleChatWebClient("gc-test-1", map[string]string{
		"SAPISID": "test_sapisid_secret_123",
		"SID":     "test_sid",
	}, nil)

	if !client.HasCookies() {
		t.Fatalf("expected client to report having cookies")
	}

	authHeader, cookieHeader, err := client.generateSAPISIDHash()
	if err != nil {
		t.Fatalf("failed to generate SAPISID hash: %v", err)
	}

	if !strings.HasPrefix(authHeader, "SAPISIDHASH ") {
		t.Errorf("expected authHeader to start with 'SAPISIDHASH ', got %q", authHeader)
	}

	if !strings.Contains(cookieHeader, "SAPISID=test_sapisid_secret_123") {
		t.Errorf("expected cookieHeader to contain SAPISID, got %q", cookieHeader)
	}
}

func TestGoogleChatScheduleMessageValidation(t *testing.T) {
	client := newGoogleChatWebClient("gc-test-1", map[string]string{}, nil)

	_, err := client.ScheduleMessage("_webSpaceID", "", "", "Hello world", "spaces/AAAA1234", time.Now().Add(10*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "session cookies required") {
		t.Fatalf("expected error for missing cookies, got: %v", err)
	}

	client.SetCookies(map[string]string{"SAPISID": "valid_cookie", "X-Framework-Xsrf-Token": "valid_xsrf"})
	_, err = client.ScheduleMessage("_webSpaceID", "", "", "Hello world", "spaces/AAAA1234", time.Now().Add(-10*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "must be in the future") {
		t.Fatalf("expected error for past scheduled time, got: %v", err)
	}
}

func TestBuildCreateUnsentMessageThreadPayload(t *testing.T) {
	at := time.Unix(1785996000, 0)
	payload := buildCreateUnsentMessagePayload("5-xNo0A8iGE", "-zrGTiAAAAE", "C_0pR-kBX5w", "héhé", at)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	message := decoded[1].([]interface{})
	wantRef := []interface{}{"5-xNo0A8iGE", []interface{}{nil, nil, []interface{}{"-zrGTiAAAAE"}}, "C_0pR-kBX5w"}
	if !reflect.DeepEqual(message[0], wantRef) {
		t.Fatalf("space ref=%#v, want %#v", message[0], wantRef)
	}
}

func TestBuildCreateUnsentMessageTopLevelPayload(t *testing.T) {
	payload := buildCreateUnsentMessagePayload("iQm4i3P--7s", "-zrGTiAAAAE", "", "test", time.Unix(1786341600, 0))
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	message := decoded[1].([]interface{})
	wantRef := []interface{}{"iQm4i3P--7s", []interface{}{nil, nil, []interface{}{"-zrGTiAAAAE"}}}
	if !reflect.DeepEqual(message[0], wantRef) {
		t.Fatalf("space ref=%#v, want %#v", message[0], wantRef)
	}
}

func TestNewUnsentMessageIDMatchesWebClientShape(t *testing.T) {
	first, err := newUnsentMessageID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newUnsentMessageID()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 11 || strings.ContainsAny(first, "+/=") {
		t.Fatalf("unexpected unsent message ID %q", first)
	}
	if first == second {
		t.Fatal("expected a fresh unsent message ID")
	}
}

func TestBrowserCookieParamsUseURLInsteadOfDomain(t *testing.T) {
	params := browserCookieParams(map[string]string{
		"SID":                    "sid",
		"__Host-test":            "host-cookie",
		"X-Framework-Xsrf-Token": "internal",
		"empty":                  "",
	})
	if len(params) != 2 {
		t.Fatalf("got %d browser cookies, want 2", len(params))
	}
	for _, param := range params {
		if param.URL != "https://chat.google.com/" || param.Domain != "" || param.Path != "" {
			t.Fatalf("invalid browser cookie scope: %+v", param)
		}
	}
}
