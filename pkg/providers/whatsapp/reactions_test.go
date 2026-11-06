package whatsapp

import (
	"testing"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"github.com/glebarez/sqlite"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"gorm.io/gorm"
)

func TestReactionMessageTarget(t *testing.T) {
	for _, tc := range []struct {
		name, chat, sender, participant string
		fromMe                          bool
	}{
		{name: "incoming group", chat: "123@g.us", sender: "456@s.whatsapp.net", participant: "456@s.whatsapp.net"},
		{name: "LID group author", chat: "123@g.us", sender: "456@lid", participant: "456@lid"},
		{name: "device-qualified author", chat: "123@g.us", sender: "456:7@s.whatsapp.net", participant: "456@s.whatsapp.net"},
		{name: "own group message", chat: "123@g.us", fromMe: true},
		{name: "direct message", chat: "456@s.whatsapp.net", sender: "456@s.whatsapp.net"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := NewWhatsAppProvider()
			provider.config["_instance_id"] = "whatsapp-2"
			provider.client = &whatsmeow.Client{Store: &store.Device{}}
			provider.conversationMessages[core.BuildConvID("whatsapp-2", tc.chat)] = []models.Message{{
				ProtocolMsgID: "target", SenderID: tc.sender, IsFromMe: tc.fromMe,
			}}
			chat, err := types.ParseJID(tc.chat)
			if err != nil {
				t.Fatal(err)
			}
			// Removal must carry exactly the same target key as addition.
			for _, emoji := range []string{"👍", ""} {
				message, err := provider.reactionMessage(chat, "target", emoji)
				if err != nil {
					t.Fatal(err)
				}
				reaction := message.GetReactionMessage()
				key := reaction.GetKey()
				if key.GetParticipant() != tc.participant || key.GetFromMe() != tc.fromMe || key.GetRemoteJID() != tc.chat || key.GetID() != "target" {
					t.Fatalf("unexpected reaction key: %v", key)
				}
				if reaction.GetText() != emoji || reaction.GetSenderTimestampMS() == 0 {
					t.Fatalf("unexpected reaction payload: %v", reaction)
				}
			}
		})
	}
}

func TestReactionMessageDatabaseScope(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous })
	if err := database.Create(&[]models.Message{
		{ProtocolConvID: "whatsapp-2::123@g.us", ProtocolMsgID: "target", SenderID: "456@s.whatsapp.net"},
		{ProtocolConvID: "whatsapp-3::123@g.us", ProtocolMsgID: "other-provider", SenderID: "789@s.whatsapp.net"},
		{ProtocolConvID: "whatsapp-2::999@g.us", ProtocolMsgID: "other-chat", SenderID: "789@s.whatsapp.net"},
		{ProtocolConvID: "whatsapp-2::123@g.us", ProtocolMsgID: "missing-sender"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	provider := NewWhatsAppProvider()
	provider.config["_instance_id"] = "whatsapp-2"
	provider.client = &whatsmeow.Client{Store: &store.Device{}}
	chat := types.NewJID("123", types.GroupServer)
	message, err := provider.reactionMessage(chat, "target", "👍")
	if err != nil {
		t.Fatal(err)
	}
	if got := message.GetReactionMessage().GetKey().GetParticipant(); got != "456@s.whatsapp.net" {
		t.Fatalf("participant = %q", got)
	}
	for _, id := range []string{"other-provider", "other-chat", "missing-sender", "unknown"} {
		if _, err := provider.reactionMessage(chat, id, "👍"); err == nil {
			t.Errorf("expected reaction target %q to be rejected", id)
		}
	}
}
