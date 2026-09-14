package googlemessages

import (
	"testing"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"github.com/glebarez/sqlite"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"gorm.io/gorm"
)

func testLine(id string, number int32) *gmproto.SIMCard {
	return &gmproto.SIMCard{SIMParticipant: &gmproto.SIMParticipant{ID: id}, SIMData: &gmproto.SIMData{CarrierName: "Carrier", FormattedPhoneNumber: "test-number", SIMPayload: &gmproto.SIMPayload{SIMNumber: number, Two: 2}}}
}
func identityTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.AutoMigrate(&models.Conversation{}, &models.Message{}); err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous })
	return database
}

func TestSelectedLineSurvivesRestartAndCannotCrossProviders(t *testing.T) {
	database := identityTestDB(t)
	for _, id := range []string{"one::chat", "two::chat"} {
		if err := database.Create(&models.Conversation{ProtocolConvID: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	p := &Provider{instance: "one", simCards: map[string]*gmproto.SIMCard{"a": testLine("a", 1), "b": testLine("b", 2)}}
	if err := p.SetConversationIdentity("one::chat", "b"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetConversationIdentity("two::chat", "b"); err == nil {
		t.Fatal("cross-provider selection accepted")
	}
	restarted := &Provider{instance: "one", simCards: p.simCards}
	remote := &gmproto.Conversation{ConversationID: "chat", DefaultOutgoingID: "a", SimCard: testLine("a", 1)}
	card, err := restarted.resolveSendingIdentity("one::chat", remote)
	if err != nil || card.GetSIMParticipant().GetID() != "b" || card.GetSIMData().GetSIMPayload().GetSIMNumber() != 2 {
		t.Fatalf("selected line/payload mismatch: %v", err)
	}
	restarted.updateSIMCards(&gmproto.Settings{SIMCards: []*gmproto.SIMCard{testLine("a", 1)}})
	if _, err = restarted.resolveSendingIdentity("one::chat", remote); err == nil {
		t.Fatal("unavailable line silently fell back")
	}
	if err := restarted.SetConversationIdentity("one::chat", ""); err != nil {
		t.Fatal(err)
	}
	card, err = restarted.resolveSendingIdentity("one::chat", remote)
	if err != nil || card.GetSIMParticipant().GetID() != "a" {
		t.Fatal("remote default not restored")
	}
	var other models.Conversation
	database.Where("protocol_conv_id = ?", "two::chat").First(&other)
	if other.OutgoingIdentityID != "" {
		t.Fatal("other provider mutated")
	}
}

func TestIdentityReconciliationIsScopedAndDoesNotGuessIncomingLine(t *testing.T) {
	database := identityTestDB(t)
	rows := []models.Message{
		{ProtocolMsgID: "own", ProtocolConvID: "one::chat", SenderID: "participant:b", IsFromMe: true},
		{ProtocolMsgID: "incoming", ProtocolConvID: "one::chat", SenderID: "participant:b"},
		{ProtocolMsgID: "other", ProtocolConvID: "two::chat", SenderID: "participant:b", IsFromMe: true},
		{ProtocolMsgID: "snapshot", ProtocolConvID: "one::chat", SenderID: "participant:b", IsFromMe: true, LocalIdentityID: "old", LocalIdentityLabel: "Old label"},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	p := &Provider{instance: "one"}
	if err := p.reconcileMessageIdentities([]core.CommunicationIdentity{canonicalIdentity(testLine("b", 2))}); err != nil {
		t.Fatal(err)
	}
	var result []models.Message
	database.Order("id").Find(&result)
	if result[0].LocalIdentityID != "b" {
		t.Fatal("outgoing mapping missing")
	}
	if result[1].LocalIdentityID != "" || !result[1].LocalIdentityApplicable {
		t.Fatal("incoming line was guessed")
	}
	if result[2].LocalIdentityID != "" || result[2].LocalIdentityApplicable {
		t.Fatal("other provider changed")
	}
	if result[3].LocalIdentityID != "old" || result[3].LocalIdentityLabel != "Old label" {
		t.Fatal("historical identity overwritten")
	}
	if err := (&Provider{}).reconcileMessageIdentities(nil); err == nil {
		t.Fatal("empty instance accepted")
	}
}

func TestIncomingSenderPayloadDoesNotIdentifyReceivingSIM(t *testing.T) {
	p := &Provider{instance: "one", simCards: map[string]*gmproto.SIMCard{"b": testLine("b", 2)}}
	remote := &gmproto.Message{ParticipantID: "b", SenderParticipant: &gmproto.Participant{ID: &gmproto.SmallInfo{ParticipantID: "b"}, SimPayload: &gmproto.SIMPayload{SIMNumber: 2, Two: 2}}}
	message := p.toModelMessage(remote, "one::chat", "")
	if message.LocalIdentityID != "" || !message.LocalIdentityApplicable {
		t.Fatal("incoming sender confused with recipient")
	}
	remote.SenderParticipant.IsMe = true
	message = p.toModelMessage(remote, "one::chat", "")
	if message.LocalIdentityID != "b" {
		t.Fatal("own sender line not resolved")
	}
}
