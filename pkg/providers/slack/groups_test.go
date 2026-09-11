package slack

import (
	"encoding/json"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestNormalizeSlackResponseObjectErrors(t *testing.T) {
	input := []byte(`{"ok":true,"errors":{"warning":"ignored"}}`)
	normalized := normalizeSlackResponse(input)
	var response struct {
		OK     bool            `json:"ok"`
		Errors json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(normalized, &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Errors != nil {
		t.Fatalf("unexpected normalized response: %s", normalized)
	}
}

func TestNormalizeSlackResponsePreservesArrayErrors(t *testing.T) {
	input := []byte(`{"ok":false,"error":"failed","errors":["detail"]}`)
	if got := string(normalizeSlackResponse(input)); got != string(input) {
		t.Fatalf("response changed: %s", got)
	}
}

func TestNormalizeSlackChannelName(t *testing.T) {
	tests := map[string]string{
		"Projet Été 2026":        "projet-ete-2026",
		"  Design & Produit  ":   "design-produit",
		"release_candidate":      "release_candidate",
		"--- Déjà...terminé ---": "deja-termine",
		"💬":                      "",
	}
	for input, expected := range tests {
		if actual := normalizeSlackChannelName(input); actual != expected {
			t.Errorf("normalizeSlackChannelName(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestNormalizeSlackChannelNameTruncates(t *testing.T) {
	actual := normalizeSlackChannelName(strings.Repeat("a", slackChannelNameMaxLength+10))
	if len(actual) != slackChannelNameMaxLength {
		t.Fatalf("normalized length = %d, want %d", len(actual), slackChannelNameMaxLength)
	}
}

func TestGroupDetailsMPIMDoesNotRequireChannelMembershipFlag(t *testing.T) {
	provider := NewSlackProvider()
	provider.config = map[string]interface{}{"_instance_id": "slack-1"}

	details := provider.groupDetails("C06867NN9ED", &goslack.Channel{
		GroupConversation: goslack.GroupConversation{
			Conversation: goslack.Conversation{IsMpIM: true},
			Name:         "mpdm-example",
		},
		IsMember: false,
	})

	if !details.IsMember || !details.CanSendMessages {
		t.Fatalf("MPIM details unexpectedly read-only: %+v", details)
	}
	if details.ConversationID != "slack-1::C06867NN9ED" {
		t.Fatalf("conversation ID = %q", details.ConversationID)
	}
}

func TestGroupDetailsKeepsNonMemberChannelReadOnly(t *testing.T) {
	provider := NewSlackProvider()
	provider.config = map[string]interface{}{"_instance_id": "slack-1"}

	details := provider.groupDetails("C123", &goslack.Channel{IsMember: false})
	if details.IsMember || details.CanSendMessages {
		t.Fatalf("non-member channel unexpectedly writable: %+v", details)
	}
}

func TestGroupDetailsKeepsReadOnlyMPIMReadOnly(t *testing.T) {
	provider := NewSlackProvider()
	provider.config = map[string]interface{}{"_instance_id": "slack-1"}

	details := provider.groupDetails("C123", &goslack.Channel{
		GroupConversation: goslack.GroupConversation{
			Conversation: goslack.Conversation{IsMpIM: true, IsReadOnly: true},
		},
	})
	if details.CanSendMessages {
		t.Fatalf("read-only MPIM unexpectedly writable: %+v", details)
	}
}
