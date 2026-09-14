package googlemessages

import (
	"testing"

	"Loom/pkg/models"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

func TestParticipantRepairIsScopedIdempotentAndDoesNotCopyCollidingProfiles(t *testing.T) {
	database := identityTestDB(t)
	if err := database.AutoMigrate(&models.ParticipantProfile{}, &models.Reaction{}, &models.MessageReceipt{}, &models.GroupParticipant{}); err != nil {
		t.Fatal(err)
	}
	quoted := "23"
	for _, instance := range []string{"one", "two"} {
		conversation := models.Conversation{ProtocolConvID: instance + "::21"}
		if err := database.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
		message := models.Message{ProtocolMsgID: instance + "-msg", ProtocolConvID: conversation.ProtocolConvID, SenderID: "2", SenderName: "Own name", IsFromMe: true, QuotedSenderID: &quoted}
		if err := database.Create(&message).Error; err != nil {
			t.Fatal(err)
		}
		for _, row := range []any{
			&models.Reaction{MessageID: message.ID, UserID: "23", Emoji: "👍"},
			&models.MessageReceipt{MessageID: message.ID, UserID: "23", ReceiptType: "read"},
			&models.GroupParticipant{ConversationID: conversation.ID, UserID: "23"},
			&models.ParticipantProfile{ProviderInstanceID: instance, UserID: "2", DisplayName: "Unrelated conversation"},
		} {
			if err := database.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	p := &Provider{instance: "one"}
	for i := 0; i < 2; i++ {
		if err := p.repairParticipantIDs(); err != nil {
			t.Fatal(err)
		}
	}
	for _, instance := range []string{"one", "two"} {
		var message models.Message
		if err := database.Preload("Reactions").Preload("Receipts").Where("protocol_conv_id = ?", instance+"::21").First(&message).Error; err != nil {
			t.Fatal(err)
		}
		sender, other := "2", "23"
		if instance == "one" {
			sender, other = "participant:2", "participant:23"
		}
		if message.SenderID != sender || *message.QuotedSenderID != other || message.Reactions[0].UserID != other || message.Receipts[0].UserID != other {
			t.Fatalf("incorrect migration for %s: %+v", instance, message)
		}
		if message.SenderName != "Own name" {
			t.Fatal("stored name overwritten")
		}
		var participant models.GroupParticipant
		database.Joins("JOIN conversations ON conversations.id = group_participants.conversation_id").Where("conversations.protocol_conv_id = ?", instance+"::21").First(&participant)
		if participant.UserID != other {
			t.Fatal("participant scope/migration mismatch")
		}
	}
	var count int64
	database.Model(&models.ParticipantProfile{}).Where("user_id LIKE ?", "participant:%").Count(&count)
	if count != 0 {
		t.Fatal("ambiguous cached profile copied")
	}
	if err := (&Provider{}).repairParticipantIDs(); err == nil {
		t.Fatal("empty provider accepted")
	}
}

func TestMessageAndProfileParticipantIDsCannotCollideWithConversations(t *testing.T) {
	p := NewProvider()
	remote := &gmproto.Message{ConversationID: "21", ParticipantID: "23", SenderParticipant: &gmproto.Participant{ID: &gmproto.SmallInfo{ParticipantID: "23"}, FullName: "Naima"}, Reactions: []*gmproto.ReactionEntry{{Data: gmproto.MakeReactionData("👍"), ParticipantIDs: []string{"2"}}}}
	message := p.toModelMessage(remote, "", "")
	if message.SenderID != "participant:23" || message.Reactions[0].UserID != "participant:2" {
		t.Fatal("wire participant ID exposed without qualification")
	}
	profile := googleMessagesContactProfile(remote.SenderParticipant, "one")
	if profile.UserID != message.SenderID {
		t.Fatal("profile/message identity mismatch")
	}
	if p.NormalizeParticipantID(message.SenderID) != message.SenderID || p.NormalizeParticipantID("one::21") != "one::21" {
		t.Fatal("normalization is not idempotent or rewrites aggregate receipts")
	}
}
