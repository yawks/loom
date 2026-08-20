package whatsapp

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"Loom/pkg/models"
	waProto "go.mau.fi/whatsmeow/binary/proto"
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

func TestReconcileDuplicateMessageBackfillsCaptionAndAttachmentWithoutEditing(t *testing.T) {
	existing := &models.Message{ProtocolMsgID: "image-message"}
	incoming := &models.Message{
		ProtocolMsgID: "image-message",
		Body:          "Légende de la photo",
		Attachments:   `[{"type":"image","url":"/tmp/photo.jpg"}]`,
	}

	reconcileDuplicateMessage(existing, incoming)

	if existing.Body != incoming.Body || existing.Attachments != incoming.Attachments {
		t.Fatalf("reconciled message = %#v", existing)
	}
	if existing.IsEdited || existing.EditedTimestamp != nil {
		t.Fatalf("duplicate backfill was incorrectly marked edited: %#v", existing)
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

func TestConvertGroupMessageWithoutMetadataLookup(t *testing.T) {
	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-1"
	chat := types.NewJID("120363427708347162", types.GroupServer)
	sender := types.NewJID("33600000000", types.DefaultUserServer)
	event := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: sender},
			ID:            "group-message",
			Timestamp:     time.Unix(1_700_000_000, 0),
		},
		Message: &waE2E.Message{Conversation: proto.String("bonjour")},
	}

	got := provider.convertMessage(event)
	if got == nil {
		t.Fatal("convertMessage returned nil")
	}
	if got.ProtocolConvID != "whatsapp-1::120363427708347162@g.us" {
		t.Fatalf("protocol conversation ID = %q", got.ProtocolConvID)
	}
	if got.Body != "bonjour" {
		t.Fatalf("body = %q, want %q", got.Body, "bonjour")
	}
	if provider.knownGroups[chat.String()] != chat.String() {
		t.Fatalf("unknown group was not tracked immediately")
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

func TestReconcileDuplicateMessagePreservesEditedBody(t *testing.T) {
	editTime := time.Now()
	existing := &models.Message{
		ProtocolMsgID:   "edited-msg-1",
		Body:            "Texte modifié",
		IsEdited:        true,
		EditedTimestamp: &editTime,
	}
	incomingUnedited := &models.Message{
		ProtocolMsgID: "edited-msg-1",
		Body:          "Texte original non modifié",
		IsEdited:      false,
	}

	reconcileDuplicateMessage(existing, incomingUnedited)

	if existing.Body != "Texte modifié" || !existing.IsEdited || existing.EditedTimestamp == nil {
		t.Fatalf("reconcileDuplicateMessage overwrote edited body: %#v", existing)
	}

	incomingNewerEdit := &models.Message{
		ProtocolMsgID:   "edited-msg-1",
		Body:            "Texte encore plus récent",
		IsEdited:        true,
		EditedTimestamp: &editTime,
	}
	reconcileDuplicateMessage(existing, incomingNewerEdit)
	if existing.Body != "Texte encore plus récent" || !existing.IsEdited {
		t.Fatalf("reconcileDuplicateMessage did not accept newer edit: %#v", existing)
	}
}

func TestStoreMessagesForConversationPreservesEditedState(t *testing.T) {
	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-1"
	convID := "whatsapp-1::33600000000@s.whatsapp.net"

	editTime := time.Now()
	editedMsg := models.Message{
		ProtocolMsgID:   "msg-edit-test",
		ProtocolConvID:  convID,
		Body:            "Message modifié",
		IsEdited:        true,
		EditedTimestamp: &editTime,
		Timestamp:       time.Unix(100, 0),
	}

	provider.storeMessagesForConversation(convID, []models.Message{editedMsg})

	// Redelivering the unedited version of the same message should NOT revert the cache
	uneditedMsg := models.Message{
		ProtocolMsgID:  "msg-edit-test",
		ProtocolConvID: convID,
		Body:           "Message original",
		IsEdited:       false,
		Timestamp:      time.Unix(100, 0),
	}

	provider.storeMessagesForConversation(convID, []models.Message{uneditedMsg})

	cached := provider.conversationMessages[convID]
	if len(cached) != 1 {
		t.Fatalf("expected 1 message in cache, got %d", len(cached))
	}
	if cached[0].Body != "Message modifié" || !cached[0].IsEdited {
		t.Fatalf("storeMessagesForConversation reverted edited message: %#v", cached[0])
	}
}

func TestHandleEditedProtocolMessageUpdatesCacheAndPendingEdits(t *testing.T) {
	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-1"
	convID := "33600000000@s.whatsapp.net"
	nsConvID := "whatsapp-1::33600000000@s.whatsapp.net"

	chat := types.NewJID("33600000000", types.DefaultUserServer)

	// 1. Target message in cache is updated
	provider.setCachedConversationMessagesLocked(nsConvID, []models.Message{{
		ProtocolMsgID:  "msg-target-1",
		ProtocolConvID: nsConvID,
		Body:           "Original",
		Timestamp:      time.Unix(100, 0),
	}})

	editEvent := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat},
			ID:            "edit-protocol-msg-1",
			Timestamp:     time.Unix(200, 0),
		},
		Message: &waE2E.Message{
			ProtocolMessage: &waE2E.ProtocolMessage{
				Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
				Key: &waProto.MessageKey{
					RemoteJID: proto.String(convID),
					ID:        proto.String("msg-target-1"),
				},
				EditedMessage: &waE2E.Message{
					Conversation: proto.String("Modified text"),
				},
			},
		},
	}

	provider.handleEditedProtocolMessage(editEvent, editEvent.Message.ProtocolMessage, false)

	cached := provider.conversationMessages[nsConvID]
	if len(cached) != 1 || cached[0].Body != "Modified text" || !cached[0].IsEdited {
		t.Fatalf("handleEditedProtocolMessage failed to update cache: %#v", cached)
	}

	// 2. Target message not yet in cache is saved to pendingEdits and applied on convertMessage
	editEvent2 := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat},
			ID:            "edit-protocol-msg-2",
			Timestamp:     time.Unix(300, 0),
		},
		Message: &waE2E.Message{
			ProtocolMessage: &waE2E.ProtocolMessage{
				Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
				Key: &waProto.MessageKey{
					RemoteJID: proto.String(convID),
					ID:        proto.String("msg-target-pending"),
				},
				EditedMessage: &waE2E.Message{
					Conversation: proto.String("Pending modified text"),
				},
			},
		},
	}

	provider.handleEditedProtocolMessage(editEvent2, editEvent2.Message.ProtocolMessage, false)

	// Now convert the original message that arrives later
	originalEvent := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat},
			ID:            "msg-target-pending",
			Timestamp:     time.Unix(250, 0),
		},
		Message: &waE2E.Message{
			Conversation: proto.String("Original text"),
		},
	}

	converted := provider.convertMessage(originalEvent)
	if converted == nil || converted.Body != "Pending modified text" || !converted.IsEdited {
		t.Fatalf("pending edit was not applied to converted message: %#v", converted)
	}
}

