package googlemessages

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"testing"
)

func TestLiveGroupAvatarReplacementAndRemovalAreProviderScoped(t *testing.T) {
	if err := db.InitMockDatabase(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB.DB(); _ = sqlDB.Close(); db.DB = nil })
	p := NewProvider()
	p.instance = "googlemessages-1"
	other := NewProvider()
	other.instance = "googlemessages-2"
	remote := &gmproto.Conversation{ConversationID: "21", Name: "Group", IsGroupChat: true, GroupAvatarURL: "old"}
	if err := other.storeConversation(remote); err != nil {
		t.Fatal(err)
	}
	for _, avatar := range []string{"old", "new", ""} {
		remote.GroupAvatarURL = avatar
		p.handleLibGMEvent(remote)
		account, ok := db.ContactStore.FindByProviderUser(p.instance, "21")
		if !ok || account.AvatarURL != avatar {
			t.Fatalf("avatar not updated: %+v", account)
		}
		var meta models.MetaContact
		if err := db.DB.First(&meta, account.MetaContactID).Error; err != nil {
			t.Fatal(err)
		}
		if meta.AvatarURL != avatar {
			t.Fatal("meta avatar not updated")
		}
		foreign, _ := db.ContactStore.FindByProviderUser(other.instance, "21")
		if foreign.AvatarURL != "old" {
			t.Fatal("foreign avatar changed")
		}
		select {
		case raw := <-p.eventChan:
			e, ok := raw.(core.ContactStatusEvent)
			if !ok || e.InstanceID != p.instance {
				t.Fatalf("unexpected refresh: %+v", raw)
			}
		default:
			t.Fatal("missing refresh")
		}
	}
}
