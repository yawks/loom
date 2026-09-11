package db

import (
	"Loom/pkg/models"
	"testing"
)

func TestConversationAvatarUpdatesAreScopedAndIncludeRemoval(t *testing.T) {
	if err := InitMockDatabase(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := DB.DB(); _ = sqlDB.Close(); DB = nil })
	accounts := make([]models.LinkedAccount, 2)
	for i, instance := range []string{"matrix-1", "matrix-2"} {
		meta := models.MetaContact{DisplayName: "Room", AvatarURL: "old"}
		if err := DB.Create(&meta).Error; err != nil {
			t.Fatal(err)
		}
		accounts[i] = models.LinkedAccount{MetaContactID: meta.ID, ProviderInstanceID: instance, UserID: "room", AvatarURL: "old"}
		if err := DB.Create(&accounts[i]).Error; err != nil {
			t.Fatal(err)
		}
		if err := DB.Create(&models.Conversation{LinkedAccountID: accounts[i].ID, ProtocolConvID: instance + "::room"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, photo := range []string{"new", ""} {
		if err := UpdateConversationAvatar("matrix-1", "matrix-1::room", photo); err != nil {
			t.Fatal(err)
		}
		for i, want := range []string{photo, "old"} {
			var account models.LinkedAccount
			var meta models.MetaContact
			DB.First(&account, accounts[i].ID)
			DB.First(&meta, account.MetaContactID)
			if account.AvatarURL != want || meta.AvatarURL != want {
				t.Fatalf("instance %d: account=%q meta=%q want=%q", i, account.AvatarURL, meta.AvatarURL, want)
			}
		}
		cached, ok := ContactStore.FindByProviderUser("matrix-1", "room")
		if !ok || cached.AvatarURL != photo {
			t.Fatal("account cache not updated")
		}
		meta, ok := ContactStore.FindMetaContact(cached.MetaContactID)
		if !ok || meta.AvatarURL != photo {
			t.Fatal("meta cache not updated")
		}
	}
	if err := UpdateConversationAvatar("", "matrix-1::room", "bad"); err == nil {
		t.Fatal("accepted empty instance")
	}
	if err := UpdateConversationAvatar("matrix-1", "matrix-2::room", "bad"); err == nil {
		t.Fatal("accepted foreign conversation")
	}
}
