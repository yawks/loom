package whatsapp

import (
	"fmt"
	"testing"
	"time"

	"Loom/pkg/models"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func TestSetCachedConversationMessagesLockedBoundsMessages(t *testing.T) {
	provider := NewWhatsAppProvider()
	messages := make([]models.Message, maxMessagesPerConversation+25)
	for i := range messages {
		messages[i] = models.Message{
			ProtocolMsgID: fmt.Sprintf("message-%d", i),
			Timestamp:     time.Unix(int64(i), 0),
		}
	}

	provider.setCachedConversationMessagesLocked("conversation", messages)

	cached := provider.conversationMessages["conversation"]
	if len(cached) != maxMessagesPerConversation {
		t.Fatalf("cached %d messages, want %d", len(cached), maxMessagesPerConversation)
	}
	if cached[0].ProtocolMsgID != "message-25" {
		t.Fatalf("oldest cached message = %q, want %q", cached[0].ProtocolMsgID, "message-25")
	}
}

func TestSetCachedConversationMessagesLockedEvictsOldestConversation(t *testing.T) {
	provider := NewWhatsAppProvider()
	for i := 0; i < maxCachedConversations; i++ {
		provider.setCachedConversationMessagesLocked(fmt.Sprintf("conversation-%d", i), []models.Message{{
			Timestamp: time.Unix(int64(i+1), 0),
		}})
	}

	provider.setCachedConversationMessagesLocked("new-conversation", []models.Message{{
		Timestamp: time.Unix(int64(maxCachedConversations+1), 0),
	}})

	if len(provider.conversationMessages) != maxCachedConversations {
		t.Fatalf("cached %d conversations, want %d", len(provider.conversationMessages), maxCachedConversations)
	}
	if _, exists := provider.conversationMessages["conversation-0"]; exists {
		t.Fatal("oldest conversation was not evicted")
	}
	if _, exists := provider.conversationMessages["new-conversation"]; !exists {
		t.Fatal("new conversation was not cached")
	}
}

func TestConvertMessageUnwrapsBotForwardedText(t *testing.T) {
	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-1"
	chat := types.NewJID("33600000000", types.DefaultUserServer)
	event := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat},
			ID:            "forwarded-message",
			Timestamp:     time.Unix(1_700_000_000, 0),
		},
		Message: &waE2E.Message{
			BotForwardedMessage: &waE2E.FutureProofMessage{
				Message: &waE2E.Message{
					ExtendedTextMessage: &waE2E.ExtendedTextMessage{
						Text: proto.String("message transféré"),
					},
				},
			},
		},
	}

	got := provider.convertMessage(event)
	if got == nil {
		t.Fatal("convertMessage returned nil")
	}
	if got.Body != "message transféré" {
		t.Fatalf("body = %q, want %q", got.Body, "message transféré")
	}
	if got.ProtocolConvID != "whatsapp-1::33600000000@s.whatsapp.net" {
		t.Fatalf("protocol conversation ID = %q, want namespaced ID", got.ProtocolConvID)
	}
	if !got.IsForwarded {
		t.Fatal("forwarded message was not marked as forwarded")
	}
	if event.Message.GetExtendedTextMessage() == nil {
		t.Fatal("event message was not unwrapped for attachment/context processing")
	}
}
