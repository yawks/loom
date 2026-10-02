package signal

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/events"
	signalpb "go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/backuppb"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
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

func TestChatEventDoesNotAcknowledgeFailedPersistence(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous })
	p := NewProvider().(*Provider)
	p.config = core.ProviderConfig{"_instance_id": "signal-test"}
	event := &events.ChatEvent{
		Info:  events.MessageInfo{ChatID: "chat", Sender: uuid.New()},
		Event: &signalpb.DataMessage{Body: proto.String("offline message"), Timestamp: proto.Uint64(123)},
	}
	// Missing canonical tables force an actual database failure.
	if p.handleSignalEvent(event) {
		t.Fatal("failed persistence was acknowledged to Signal")
	}
	if len(p.history) != 0 || len(p.events) != 0 {
		t.Fatal("failed message was published before persistence")
	}
}

func TestMessageTimestamp(t *testing.T) {
	for input, want := range map[string]uint64{"123": 123, "sender|456": 456} {
		got, err := messageTimestamp(input)
		if err != nil || got != want {
			t.Fatalf("messageTimestamp(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
}
