package matrix

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRoomAvatarStateTimelineAndRemoval(t *testing.T) {
	if err := db.InitMockDatabase(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB.DB(); _ = sqlDB.Close(); db.DB = nil })
	p := NewProvider()
	p.instanceID = "matrix-1"
	p.homeserver = "https://matrix.example"
	p.accessToken = "secret"
	p.persistRoom(models.LinkedAccount{ProviderInstanceID: "matrix-1", Protocol: "matrix", UserID: "!room:example", Username: "Room", AvatarURL: "old", IsGroup: true}, "!room:example")
	other := NewProvider()
	other.instanceID = "matrix-2"
	other.persistRoom(models.LinkedAccount{ProviderInstanceID: "matrix-2", Protocol: "matrix", UserID: "!room:example", Username: "Other", AvatarURL: "other", IsGroup: true}, "!room:example")
	requests := 0
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Authorization") != "Bearer secret" || !strings.HasSuffix(r.URL.Path, "/latest") {
			t.Errorf("incorrect media request: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("\x89PNG\r\n\x1a\nimage"))}, nil
	})}
	key := ""
	event := func(body string) matrixEvent {
		return matrixEvent{Type: "m.room.avatar", StateKey: &key, Content: json.RawMessage(body)}
	}
	p.updateRoomAvatars(context.Background(), map[string][][]matrixEvent{"!room:example": {{event(`{"url":"mxc://media/old"}`)}, {event(`{"url":"mxc://media/latest"}`)}}})
	got, _ := db.ContactStore.FindByProviderUser("matrix-1", "!room:example")
	if requests != 1 || !strings.HasPrefix(got.AvatarURL, "data:image/png;base64,") {
		t.Fatalf("avatar not resolved: %q, requests=%d", got.AvatarURL, requests)
	}
	saved := got.AvatarURL
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
	})}
	p.updateRoomAvatars(context.Background(), map[string][][]matrixEvent{"!room:example": {{event(`{"url":"mxc://media/fail"}`)}}})
	got, _ = db.ContactStore.FindByProviderUser("matrix-1", "!room:example")
	if got.AvatarURL != saved {
		t.Fatal("failed download erased photo")
	}
	p.updateRoomAvatars(context.Background(), map[string][][]matrixEvent{"!room:example": {{event(`{}`)}}})
	got, _ = db.ContactStore.FindByProviderUser("matrix-1", "!room:example")
	if got.AvatarURL != "" {
		t.Fatal("removal not persisted")
	}
	foreign, _ := db.ContactStore.FindByProviderUser("matrix-2", "!room:example")
	if foreign.AvatarURL != "other" {
		t.Fatal("foreign avatar changed")
	}
	if len(p.events) != 2 {
		t.Fatalf("got %d refresh events, want 2", len(p.events))
	}
	for len(p.events) > 0 {
		e := (<-p.events).(core.GroupChangeEvent)
		if e.InstanceID != "matrix-1" || e.ConversationID != "matrix-1::!room:example" {
			t.Fatal("invalid event ownership")
		}
	}
}

func TestRoomPhotoWinsOverMemberRegardlessOfStateOrder(t *testing.T) {
	p := NewProvider()
	p.userID = "@self:example"
	p.homeserver = "https://example"
	var events []matrixEvent
	if err := json.Unmarshal([]byte(`[{"type":"m.room.avatar","content":{"url":"mxc://media/room"}},{"type":"m.room.member","state_key":"@other:example","content":{"membership":"join","displayname":"Other","avatar_url":"mxc://media/member"}},{"type":"m.room.member","state_key":"@self:example","content":{"membership":"join"}}]`), &events); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if got := summarizeRoomEvents(p, events); got.Avatar != p.mediaURL("mxc://media/room") {
			t.Fatalf("member replaced room photo: %+v", got)
		}
		events[0], events[2] = events[2], events[0]
	}
}
