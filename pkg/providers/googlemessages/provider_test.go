package googlemessages

import (
	"Loom/pkg/core"
	"testing"
	"time"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

func TestToModelMessageUsesDMSenderName(t *testing.T) {
	provider := NewProvider()
	provider.instance = "googlemessages-1"
	remote := &gmproto.Message{
		MessageID:      "message-1",
		ConversationID: "conversation-1",
		SenderParticipant: &gmproto.Participant{
			ID:       &gmproto.SmallInfo{ParticipantID: "participant-1"},
			FullName: "Wrong cached name",
		},
	}

	message := provider.toModelMessage(remote, "", "Correct conversation name")
	if message.SenderName != "Correct conversation name" {
		t.Fatalf("sender name = %q, want authoritative DM name", message.SenderName)
	}
}

func TestToModelMessageKeepsOwnSenderName(t *testing.T) {
	provider := NewProvider()
	provider.instance = "googlemessages-1"
	remote := &gmproto.Message{
		MessageID:      "message-1",
		ConversationID: "conversation-1",
		SenderParticipant: &gmproto.Participant{
			ID:       &gmproto.SmallInfo{ParticipantID: "me"},
			FullName: "My profile name",
			IsMe:     true,
		},
	}

	message := provider.toModelMessage(remote, "", "Contact name")
	if message.SenderName != "My profile name" {
		t.Fatalf("sender name = %q, want own profile name", message.SenderName)
	}
}

func TestGoogleMessagesReceipts(t *testing.T) {
	timestamp := time.Unix(123, 0)
	tests := []struct {
		name       string
		status     gmproto.MessageStatusType
		wantType   core.ReceiptType
		wantLength int
	}{
		{name: "sent", status: gmproto.MessageStatusType_OUTGOING_COMPLETE, wantLength: 0},
		{name: "delivered", status: gmproto.MessageStatusType_OUTGOING_DELIVERED, wantType: core.ReceiptTypeDelivery, wantLength: 1},
		{name: "read", status: gmproto.MessageStatusType_OUTGOING_DISPLAYED, wantType: core.ReceiptTypeRead, wantLength: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipts := googleMessagesReceipts(test.status, "conversation-1", timestamp)
			if len(receipts) != test.wantLength {
				t.Fatalf("got %d receipts, want %d", len(receipts), test.wantLength)
			}
			if test.wantLength == 0 {
				return
			}
			if receipts[0].ReceiptType != string(test.wantType) {
				t.Errorf("got receipt type %q, want %q", receipts[0].ReceiptType, test.wantType)
			}
			if receipts[0].UserID != "conversation-1" {
				t.Errorf("got user ID %q", receipts[0].UserID)
			}
			if !receipts[0].Timestamp.Equal(timestamp) {
				t.Errorf("got timestamp %v, want %v", receipts[0].Timestamp, timestamp)
			}
		})
	}
}

func TestGoogleMessagesCapabilitiesIncludeReadReceipts(t *testing.T) {
	provider := NewProvider()
	if !provider.GetCapabilities().SupportsReadReceipts {
		t.Fatal("Google Messages must advertise read receipt support")
	}
}

func TestGoogleMessagesPhoneRecipientCapabilities(t *testing.T) {
	capabilities := NewProvider().GetCapabilities()
	if !capabilities.SupportsPhoneNumberRecipient {
		t.Fatal("Google Messages must advertise phone-number recipients")
	}
	if !capabilities.SupportsContactDirectory {
		t.Fatal("Google Messages conversations must remain available in the picker")
	}
}

func TestValidPhoneNumberAcceptsLocalAndInternationalFormats(t *testing.T) {
	for _, number := range []string{"36180", "0612345678", "+33612345678", "+14155552671", "+442079460018"} {
		if !validPhoneNumber(number) {
			t.Errorf("validPhoneNumber(%q) = false", number)
		}
	}
	for _, number := range []string{"", "+", "12", "+123", "+33hello", "++33612345678"} {
		if validPhoneNumber(number) {
			t.Errorf("validPhoneNumber(%q) = true", number)
		}
	}
}
