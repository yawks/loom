package googlemessages

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"encoding/json"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"gorm.io/gorm"
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

func TestGoogleMessagesContactProfileUsesFormattedPhoneNumber(t *testing.T) {
	participant := &gmproto.Participant{
		ID:              &gmproto.SmallInfo{ParticipantID: "participant-1", Number: "+33612345678"},
		FullName:        "Alice Martin",
		FormattedNumber: "06 12 34 56 78",
	}

	profile := googleMessagesContactProfile(participant, "googlemessages-1")
	if profile.DisplayName != "Alice Martin" {
		t.Fatalf("display name = %q", profile.DisplayName)
	}
	if len(profile.PhoneNumbers) != 1 || profile.PhoneNumbers[0] != "06 12 34 56 78" {
		t.Fatalf("phone numbers = %#v", profile.PhoneNumbers)
	}
	if profile.Protocol != providerID || profile.ProviderInstanceID != "googlemessages-1" {
		t.Fatalf("provider identity = %q / %q", profile.Protocol, profile.ProviderInstanceID)
	}
}

func TestGoogleMessagesContactProfileFallsBackToRawPhoneNumber(t *testing.T) {
	participant := &gmproto.Participant{
		ID: &gmproto.SmallInfo{ParticipantID: "participant-1", Number: "+33612345678"},
	}

	profile := googleMessagesContactProfile(participant, "googlemessages-1")
	if len(profile.PhoneNumbers) != 1 || profile.PhoneNumbers[0] != "+33612345678" {
		t.Fatalf("phone numbers = %#v", profile.PhoneNumbers)
	}
	if profile.DisplayName != "+33612345678" {
		t.Fatalf("display name = %q", profile.DisplayName)
	}
}

func TestLinkedAccountPersistsDirectConversationPhoneNumber(t *testing.T) {
	provider := NewProvider()
	provider.instance = "googlemessages-1"
	remote := &gmproto.Conversation{
		ConversationID: "21",
		Participants: []*gmproto.Participant{
			{ID: &gmproto.SmallInfo{ParticipantID: "me"}, IsMe: true},
			{ID: &gmproto.SmallInfo{ParticipantID: "alice", Number: "+33612345678"}, FormattedNumber: "06 12 34 56 78"},
		},
	}

	account := provider.linkedAccount(remote)
	var extra struct {
		PhoneNumbers []string `json:"phoneNumbers"`
	}
	if err := json.Unmarshal([]byte(account.Extra), &extra); err != nil {
		t.Fatalf("decode persisted profile: %v", err)
	}
	if len(extra.PhoneNumbers) != 1 || extra.PhoneNumbers[0] != "06 12 34 56 78" {
		t.Fatalf("persisted phone numbers = %#v", extra.PhoneNumbers)
	}
}

func TestStoredConversationTipIsProviderScoped(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}, &models.Reaction{}, &models.MessageReceipt{}); err != nil {
		t.Fatal(err)
	}
	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB })

	provider := NewProvider()
	provider.instance = "googlemessages-1"
	remote := &gmproto.Conversation{ConversationID: "21", LatestMessageID: "other-tip"}
	other := models.Message{ProtocolConvID: "googlemessages-2::21", ProtocolMsgID: "other-tip", Timestamp: time.Now()}
	if err := database.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if provider.hasStoredConversationTip(remote) {
		t.Fatal("another provider instance's message satisfied the local tip")
	}

	remote.LatestMessageID = "own-tip"
	ownTimestamp := time.Date(2026, 9, 3, 7, 45, 33, 0, time.UTC)
	own := models.Message{ProtocolConvID: "googlemessages-1::21", ProtocolMsgID: "own-tip", Timestamp: ownTimestamp}
	if err := database.Create(&own).Error; err != nil {
		t.Fatal(err)
	}
	if !provider.hasStoredConversationTip(remote) {
		t.Fatal("own provider instance's stored tip was not found")
	}
	globalSince := time.Date(2026, 9, 10, 10, 32, 0, 0, time.UTC)
	if got, want := provider.conversationSyncSince("21", globalSince), ownTimestamp.Add(-5*time.Minute); !got.Equal(want) {
		t.Fatalf("conversation sync lower bound = %s, want %s", got, want)
	}
	// A historical reaction has no remote timestamp and must not make the
	// conversation look active at synchronization time.
	historical := own
	historical.Reactions = []models.Reaction{{UserID: "alice", Emoji: "👍"}}
	if err := provider.storeMessages([]models.Message{historical}); err != nil {
		t.Fatal(err)
	}
	var reaction models.Reaction
	if err := database.Where("message_id = ?", own.ID).First(&reaction).Error; err != nil {
		t.Fatal(err)
	}
	if !reaction.CreatedAt.Equal(ownTimestamp) {
		t.Fatalf("historical reaction timestamp = %s, want message timestamp %s", reaction.CreatedAt, ownTimestamp)
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
