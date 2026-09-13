package whatsapp

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"
)

func TestReconcileContactConversationsRepairsMissingLinksAndIsolatesProviders(t *testing.T) {
	if err := db.InitMockDatabase(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB.DB(); _ = sqlDB.Close(); db.DB = nil })
	w := NewWhatsAppProvider()
	w.config["_instance_id"] = "whatsapp-1"
	accounts := make(map[string]models.LinkedAccount)
	for _, instance := range []string{"whatsapp-1", "whatsapp-2"} {
		meta := models.MetaContact{DisplayName: "New group"}
		if err := db.DB.Create(&meta).Error; err != nil {
			t.Fatal(err)
		}
		account := models.LinkedAccount{ProviderInstanceID: instance, Protocol: "whatsapp", UserID: "123@g.us", Username: meta.DisplayName, MetaContactID: meta.ID, IsGroup: true}
		if err := db.DB.Create(&account).Error; err != nil {
			t.Fatal(err)
		}
		accounts[instance] = account
		db.ContactStore.UpsertLinkedAccount(account)
		message := models.Message{ProtocolConvID: core.BuildConvID(instance, account.UserID), ProtocolMsgID: instance + "-message", Body: "Already received"}
		// A NULL link models messages received before directory discovery while
		// keeping this fixture valid with foreign-key enforcement enabled.
		if err := db.DB.Omit("ConversationID").Create(&message).Error; err != nil {
			t.Fatal(err)
		}
	}
	for pass := 0; pass < 2; pass++ {
		if err := w.reconcileContactConversations(); err != nil {
			t.Fatal(err)
		}
		var conversations []models.Conversation
		if err := db.DB.Find(&conversations).Error; err != nil {
			t.Fatal(err)
		}
		if len(conversations) != 1 {
			t.Fatalf("conversations = %+v", conversations)
		}
		conv := conversations[0]
		own := accounts["whatsapp-1"]
		if conv.LinkedAccountID != own.ID || !conv.IsGroup || conv.GroupName != own.Username {
			t.Fatalf("conversation = %+v", conv)
		}
		if got := db.ContactStore.GetConversation(own.ID); got != conv.ProtocolConvID {
			t.Fatalf("cache mapping = %q", got)
		}
		if got := db.ContactStore.GetConversation(accounts["whatsapp-2"].ID); got != "" {
			t.Fatalf("foreign mapping published: %q", got)
		}
		for _, instance := range []string{"whatsapp-1", "whatsapp-2"} {
			var message models.Message
			if err := db.ForProvider(db.DB, instance).Messages().First(&message).Error; err != nil {
				t.Fatal(err)
			}
			expected := uint(0)
			if instance == "whatsapp-1" {
				expected = conv.ID
			}
			if message.ConversationID != expected || message.Body != "Already received" {
				t.Fatalf("%s message changed incorrectly: %+v", instance, message)
			}
		}
		// Existing user preferences must survive another reconciliation.
		if pass == 0 {
			if err := db.DB.Model(&conv).Updates(map[string]interface{}{"is_muted": true, "is_pinned": true}).Error; err != nil {
				t.Fatal(err)
			}
		} else if !conv.IsMuted || !conv.IsPinned {
			t.Fatal("conversation preferences lost")
		}
	}
	w.config["_instance_id"] = ""
	if err := w.reconcileContactConversations(); err == nil {
		t.Fatal("empty instance accepted")
	}
}
