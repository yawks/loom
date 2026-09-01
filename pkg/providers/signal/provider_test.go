package signal

import (
	"testing"

	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/backuppb"
)

func TestCapabilitiesMatchImplementedSurface(t *testing.T) {
	caps := NewProvider().GetCapabilities()
	if !caps.SupportsQRCodeAuth || !caps.SupportsReactions || !caps.SupportsTypingIndicator || !caps.SupportsEditMessage || !caps.SupportsDeleteMessage || !caps.SupportsReadReceipts {
		t.Fatalf("expected implemented Signal capabilities, got %+v", caps)
	}
	if caps.SupportsThreads || caps.SupportsGroupManagement || caps.SupportsPinConversation || caps.SupportsMuteConversation {
		t.Fatalf("unsupported Signal capabilities must remain disabled: %+v", caps)
	}
}

func TestBackupConversationIdentityForContact(t *testing.T) {
	aci := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	recipient := &backuppb.Recipient{Destination: &backuppb.Recipient_Contact{Contact: &backuppb.Contact{
		Aci:               aci[:],
		ProfileGivenName:  stringPointer("Alice"),
		ProfileFamilyName: stringPointer("Martin"),
	}}}
	id, name, isGroup := backupConversationIdentity(recipient)
	if id != aci.String() || name != "Alice Martin" || isGroup {
		t.Fatalf("unexpected contact identity: id=%q name=%q isGroup=%v", id, name, isGroup)
	}
}

func stringPointer(value string) *string { return &value }

func TestMessageTimestamp(t *testing.T) {
	for input, want := range map[string]uint64{"123": 123, "sender|456": 456} {
		got, err := messageTimestamp(input)
		if err != nil || got != want {
			t.Fatalf("messageTimestamp(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
}
