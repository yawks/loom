package whatsapp

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"testing"
)

func TestGroupPictureRemovalUpdatesStorageAndCache(t *testing.T) {
	if err := db.InitMockDatabase(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB.DB(); _ = sqlDB.Close(); db.DB = nil })
	jid := types.NewJID("123", types.GroupServer)
	w := NewWhatsAppProvider()
	w.config = core.ProviderConfig{"_instance_id": "whatsapp-1"}
	for _, instance := range []string{"whatsapp-1", "whatsapp-2"} {
		meta := models.MetaContact{DisplayName: "Group", AvatarURL: "old"}
		if err := db.DB.Create(&meta).Error; err != nil {
			t.Fatal(err)
		}
		account := models.LinkedAccount{ProviderInstanceID: instance, UserID: jid.String(), MetaContactID: meta.ID, AvatarURL: "old", IsGroup: true}
		if err := db.DB.Create(&account).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.DB.Create(&models.Conversation{LinkedAccountID: account.ID, ProtocolConvID: core.BuildConvID(instance, jid.String()), IsGroup: true}).Error; err != nil {
			t.Fatal(err)
		}
		db.ContactStore.UpsertLinkedAccount(account)
		if instance == "whatsapp-1" {
			w.conversations[jid.String()] = account
		}
	}
	w.eventHandler(&events.Picture{JID: jid, Remove: true})
	account, _ := db.ContactStore.FindByProviderUser("whatsapp-1", jid.String())
	if account.AvatarURL != "" || w.conversations[jid.String()].AvatarURL != "" {
		t.Fatal("removed avatar remains cached")
	}
	other, _ := db.ContactStore.FindByProviderUser("whatsapp-2", jid.String())
	if other.AvatarURL != "old" {
		t.Fatal("foreign avatar changed")
	}
	select {
	case raw := <-w.eventChan:
		e, ok := raw.(core.GroupChangeEvent)
		if !ok || e.InstanceID != "whatsapp-1" {
			t.Fatalf("unexpected event %+v", raw)
		}
	default:
		t.Fatal("missing refresh")
	}
}
