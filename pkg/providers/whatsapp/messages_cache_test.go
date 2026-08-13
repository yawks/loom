package whatsapp

import (
	"encoding/json"
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

func TestConvertMessageUsesImageCaptionAsBody(t *testing.T) {
	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-1"
	chat := types.NewJID("33600000000", types.DefaultUserServer)
	event := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat},
			ID:            "captioned-image",
			Timestamp:     time.Unix(1_700_000_000, 0),
		},
		Message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption: proto.String("Voici la photo"),
		}},
	}

	got := provider.convertMessage(event)
	if got == nil {
		t.Fatal("convertMessage returned nil")
	}
	if got.Body != "Voici la photo" {
		t.Fatalf("body = %q, want image caption", got.Body)
	}
}

func TestConvertMessageFormatsContactCard(t *testing.T) {
	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-1"
	chat := types.NewJID("33600000000", types.DefaultUserServer)
	event := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat},
			ID:            "contact-card",
			Timestamp:     time.Unix(1_700_000_000, 0),
		},
		Message: &waE2E.Message{ContactMessage: &waE2E.ContactMessage{
			DisplayName: proto.String("Alice Martin"),
			Vcard:       proto.String("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Alice Martin\r\nTEL;TYPE=CELL:+33612345678\r\nEND:VCARD"),
		}},
	}

	got := provider.convertMessage(event)
	if got == nil {
		t.Fatal("convertMessage returned nil")
	}
	if got.Body != "" {
		t.Fatalf("body = %q, want empty body for structured contact", got.Body)
	}
	var attachments []models.Attachment
	if err := json.Unmarshal([]byte(got.Attachments), &attachments); err != nil {
		t.Fatalf("unmarshal attachments: %v", err)
	}
	if len(attachments) != 1 || attachments[0].Type != "contact" || attachments[0].ContactName != "Alice Martin" {
		t.Fatalf("attachments = %#v", attachments)
	}
	if len(attachments[0].ContactPhones) != 1 || attachments[0].ContactPhones[0] != "+33612345678" {
		t.Fatalf("contact phones = %#v", attachments[0].ContactPhones)
	}
}

func TestFormatMultipleContactCards(t *testing.T) {
	msg := &waE2E.Message{ContactsArrayMessage: &waE2E.ContactsArrayMessage{Contacts: []*waE2E.ContactMessage{
		{DisplayName: proto.String("Alice"), Vcard: proto.String("TEL:+33111111111")},
		{DisplayName: proto.String("Bob"), Vcard: proto.String("TEL;TYPE=WORK:+33222222222")},
	}}}

	want := "👤 Alice\n📞 +33111111111\n\n👤 Bob\n📞 +33222222222"
	if got := formatContactCards(msg); got != want {
		t.Fatalf("formatContactCards() = %q, want %q", got, want)
	}
}

func TestConvertMessageFormatsStaticLocation(t *testing.T) {
	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-1"
	chat := types.NewJID("33600000000", types.DefaultUserServer)
	event := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat},
			ID:            "location-message",
			Timestamp:     time.Unix(1_700_000_000, 0),
		},
		Message: &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
			DegreesLatitude:  proto.Float64(48.85837),
			DegreesLongitude: proto.Float64(2.294481),
			Name:             proto.String("Tour Eiffel"),
			Address:          proto.String("Champ de Mars, Paris"),
		}},
	}

	got := provider.convertMessage(event)
	if got == nil {
		t.Fatal("convertMessage returned nil")
	}
	var attachments []models.Attachment
	if err := json.Unmarshal([]byte(got.Attachments), &attachments); err != nil {
		t.Fatalf("unmarshal attachments: %v", err)
	}
	if len(attachments) != 1 || attachments[0].Type != "location" {
		t.Fatalf("attachments = %#v", attachments)
	}
	if attachments[0].Latitude == nil || *attachments[0].Latitude != 48.85837 || attachments[0].Longitude == nil || *attachments[0].Longitude != 2.294481 {
		t.Fatalf("location coordinates = %#v", attachments[0])
	}
	if attachments[0].LocationName != "Tour Eiffel" || attachments[0].Address != "Champ de Mars, Paris" {
		t.Fatalf("location metadata = %#v", attachments[0])
	}
}

func TestConvertMessageFormatsLiveLocation(t *testing.T) {
	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-1"
	chat := types.NewJID("33600000000", types.DefaultUserServer)
	event := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat},
			ID:            "live-location-message",
			Timestamp:     time.Unix(1_700_000_000, 0),
		},
		Message: &waE2E.Message{LiveLocationMessage: &waE2E.LiveLocationMessage{
			DegreesLatitude:  proto.Float64(43.296482),
			DegreesLongitude: proto.Float64(5.36978),
			AccuracyInMeters: proto.Uint32(12),
			Caption:          proto.String("En chemin"),
			ContextInfo:      &waE2E.ContextInfo{StanzaID: proto.String("original-location-message")},
		}},
	}

	got := provider.convertMessage(event)
	if got == nil || got.Body != "En chemin" {
		t.Fatalf("converted message = %#v", got)
	}
	if got.ProtocolMsgID != "original-location-message" {
		t.Fatalf("protocol message ID = %q, want original live-location ID", got.ProtocolMsgID)
	}
	var attachments []models.Attachment
	if err := json.Unmarshal([]byte(got.Attachments), &attachments); err != nil {
		t.Fatalf("unmarshal attachments: %v", err)
	}
	if len(attachments) != 1 || !attachments[0].IsLive || attachments[0].Accuracy != 12 {
		t.Fatalf("live location attachment = %#v", attachments)
	}
}
