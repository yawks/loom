package slack

import (
	"strings"
	"testing"
)

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
