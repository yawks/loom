package teams

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"encoding/json"
	"go.mau.fi/mautrix-teams/pkg/msteams"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatUpdateRefreshesExistingGroupPhotoAndRemoval(t *testing.T) {
	if err := db.InitMockDatabase(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB.DB(); _ = sqlDB.Close(); db.DB = nil })
	picture := "etag@https://document/image"
	failed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "pictureV2") {
			if failed {
				w.WriteHeader(503)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nimage"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "19:room@thread.v2", "properties": map[string]string{"topic": "Room", "picture": picture, "chatType": "group"}})
	}))
	defer srv.Close()
	client, err := msteams.NewClient(msteams.ClientConfig{UserMRI: "8:orgid:self", AuthToken: "auth", SkypeToken: "skype", Endpoints: msteams.Endpoints{MTBase: srv.URL, ChatSvcBase: srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	p := NewProvider()
	p.instance = "teams-1"
	other := NewProvider()
	other.instance = "teams-2"
	for _, provider := range []*Provider{p, other} {
		if err := provider.storeConversation(models.LinkedAccount{ProviderInstanceID: provider.instance, UserID: "19:room@thread.v2", ConversationID: "19:room@thread.v2", Username: "Room", AvatarURL: "old", IsGroup: true}); err != nil {
			t.Fatal(err)
		}
	}
	event := msteams.Event{Type: msteams.EventTypeChatUpdate, ThreadID: "19:room@thread.v2"}
	p.handleRemoteEvent(client, event)
	account, _ := db.ContactStore.FindByProviderUser(p.instance, event.ThreadID)
	if !strings.HasPrefix(account.AvatarURL, "data:image/png;base64,") {
		t.Fatalf("photo not refreshed: %s", account.AvatarURL)
	}
	photo := account.AvatarURL
	failed = true
	p.handleRemoteEvent(client, event)
	account, _ = db.ContactStore.FindByProviderUser(p.instance, event.ThreadID)
	if account.AvatarURL != photo {
		t.Fatal("failed download erased photo")
	}
	picture = ""
	p.handleRemoteEvent(client, event)
	account, _ = db.ContactStore.FindByProviderUser(p.instance, event.ThreadID)
	if account.AvatarURL != "" {
		t.Fatal("photo removal lost")
	}
	foreign, _ := db.ContactStore.FindByProviderUser(other.instance, event.ThreadID)
	if foreign.AvatarURL != "old" {
		t.Fatal("foreign avatar changed")
	}
	if len(p.eventChan) != 3 {
		t.Fatalf("got %d refresh events", len(p.eventChan))
	}
	for len(p.eventChan) > 0 {
		e, ok := (<-p.eventChan).(core.GroupChangeEvent)
		if !ok || e.InstanceID != p.instance || e.ConversationID != core.BuildConvID(p.instance, event.ThreadID) {
			t.Fatal("invalid refresh event")
		}
	}
}
