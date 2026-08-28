package slack

import (
	"Loom/pkg/models"
	"testing"
	"time"
)

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

func TestPartitionSlackIncrementalMessagesWithoutCursorKeepsIncomingUnread(t *testing.T) {
	messages := []models.Message{
		{ProtocolMsgID: "1787922724.208639", Timestamp: time.Unix(1787922724, 208639000)},
		{ProtocolMsgID: "1787922725.000001", Timestamp: time.Unix(1787922725, 1000), IsFromMe: true},
	}

	read, unread := partitionSlackIncrementalMessages(messages, "")
	if len(read) != 1 || read[0].ProtocolMsgID != messages[1].ProtocolMsgID {
		t.Fatalf("read = %#v, want only the outgoing message", read)
	}
	if len(unread) != 1 || unread[0].ProtocolMsgID != messages[0].ProtocolMsgID {
		t.Fatalf("unread = %#v, want only the incoming message", unread)
	}
}

func TestPartitionSlackIncrementalMessagesUsesReadCursor(t *testing.T) {
	messages := []models.Message{
		{ProtocolMsgID: "1787922724.208639", Timestamp: time.Unix(1787922724, 208639000)},
		{ProtocolMsgID: "1787922726.000001", Timestamp: time.Unix(1787922726, 1000)},
	}

	read, unread := partitionSlackIncrementalMessages(messages, "1787922725.000000")
	if len(read) != 1 || read[0].ProtocolMsgID != messages[0].ProtocolMsgID {
		t.Fatalf("read = %#v, want the message before the cursor", read)
	}
	if len(unread) != 1 || unread[0].ProtocolMsgID != messages[1].ProtocolMsgID {
		t.Fatalf("unread = %#v, want the message after the cursor", unread)
	}
}
