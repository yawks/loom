package slack

import "testing"

func TestSlackConversationMetadataValueResolvesCanonicalConversationID(t *testing.T) {
	metadata := map[string]string{
		"C123": "1787920000.000001",
	}

	got, ok := slackConversationMetadataValue(metadata, "slack-work::C123")
	if !ok {
		t.Fatal("expected metadata stored under the provider conversation ID to be found")
	}
	if got != metadata["C123"] {
		t.Fatalf("got %q, want %q", got, metadata["C123"])
	}
}

func TestSlackConversationMetadataValueAcceptsCanonicalMapKey(t *testing.T) {
	metadata := map[string]string{
		"slack-work::C123": "1787920000.000001",
	}

	got, ok := slackConversationMetadataValue(metadata, "slack-work::C123")
	if !ok {
		t.Fatal("expected metadata stored under the canonical conversation ID to be found")
	}
	if got != metadata["slack-work::C123"] {
		t.Fatalf("got %q, want %q", got, metadata["slack-work::C123"])
	}
}
