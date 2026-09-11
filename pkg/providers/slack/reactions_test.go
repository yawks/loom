package slack

import (
	"Loom/pkg/core"
	slackapi "github.com/slack-go/slack"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNormalizeSlackReactionName(t *testing.T) {
	tests := map[string]string{
		"bicep":              "muscle",
		"flexed_biceps":      "muscle",
		"muscle":             "muscle",
		"man_raising_hand":   "man-raising-hand",
		"woman_raising_hand": "woman-raising-hand",
		"raising_hand":       "raising_hand",
		"custom_emoji":       "custom_emoji",
		"🙃":                  "upside_down_face",
		"💪":                  "muscle",
		"👍🏽":                 "+1::skin-tone-4",
		"🙋‍♀️":               "woman-raising-hand",
		"❤️":                 "heart",
		"❤":                  "heart",
		"🫠":                  "melting_face",
	}

	for input, want := range tests {
		if got := normalizeSlackReactionName(input); got != want {
			t.Errorf("normalizeSlackReactionName(%q) = %q, want %q", input, got, want)
		}
	}
}

type reactionTransport func(*http.Request) (*http.Response, error)

func (f reactionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReactionRequestDoesNotBlockProviderAndUsesSlackName(t *testing.T) {
	for _, added := range []bool{true, false} {
		p := NewSlackProvider()
		p.config = map[string]any{"_instance_id": "slack-1"}
		p.selfUserID = "U_SELF"
		p.eventChan = make(chan core.ProviderEvent, 10)
		calls := 0
		p.client = slackapi.New("test", slackapi.OptionHTTPClient(&http.Client{Transport: reactionTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if _, ok := r.Context().Deadline(); !ok {
				t.Error("missing network deadline")
			}
			unlocked := make(chan struct{})
			go func() { p.mu.Lock(); p.mu.Unlock(); close(unlocked) }()
			select {
			case <-unlocked:
			case <-time.After(time.Second):
				t.Error("reaction holds provider lock")
			}
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("name") != "+1::skin-tone-4" || r.Form.Get("channel") != "C123" || r.Form.Get("timestamp") != "123.456" {
				t.Errorf("unexpected form: %v", r.Form)
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: r}, nil
		})}))
		var err error
		if added {
			err = p.AddReaction("slack-1::C123", "123.456", "👍🏽")
		} else {
			err = p.RemoveReaction("slack-1::C123", "123.456", "👍🏽")
		}
		if err != nil {
			t.Fatal(err)
		}
		if calls != 1 {
			t.Fatalf("got %d requests", calls)
		}
		select {
		case event := <-p.eventChan:
			reaction := event.(core.ReactionEvent)
			if reaction.Emoji != "+1::skin-tone-4" || reaction.InstanceID != "slack-1" || reaction.ConversationID != "slack-1::C123" || reaction.Added != added {
				t.Errorf("unexpected event: %+v", reaction)
			}
		default:
			t.Fatal("missing reaction event")
		}
	}
}

func TestRejectedReactionDoesNotEmitSuccess(t *testing.T) {
	p := NewSlackProvider()
	p.config = map[string]any{"_instance_id": "slack-1"}
	p.selfUserID = "U_SELF"
	p.eventChan = make(chan core.ProviderEvent, 10)
	p.client = slackapi.New("test", slackapi.OptionHTTPClient(&http.Client{Transport: reactionTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":false,"error":"invalid_name"}`)), Request: r}, nil
	})}))
	for _, add := range []bool{true, false} {
		var err error
		if add {
			err = p.AddReaction("slack-1::C123", "123.456", ":unknown_custom:")
		} else {
			err = p.RemoveReaction("slack-1::C123", "123.456", ":unknown_custom:")
		}
		if err == nil || !strings.Contains(err.Error(), "invalid_name") {
			t.Fatalf("expected API error, got %v", err)
		}
		if len(p.eventChan) != 0 {
			t.Fatal("rejected reaction emitted success")
		}
	}
}
