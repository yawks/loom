package slack

import (
	"encoding/json"
	"strings"
	"testing"
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
