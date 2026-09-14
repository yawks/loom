package matrix

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"Loom/pkg/core"
)

func TestRoomMetadataPermissions(t *testing.T) {
	for _, tc := range []struct {
		name, powers, create, membership string
		want                             [3]bool
	}{
		{"admin", `{"users":{"@me:test":100}}`, `{}`, "join", [3]bool{true, true, true}},
		{"member", `{}`, `{}`, "join", [3]bool{}},
		{"per field", `{"users_default":10,"events":{"m.room.topic":0,"m.room.avatar":10}}`, `{}`, "join", [3]bool{false, true, true}},
		{"legacy levels", `{"users":{"@me:test":"50"}}`, `{}`, "join", [3]bool{true, true, true}},
		{"not joined", `{"users":{"@me:test":100}}`, `{}`, "leave", [3]bool{}},
		{"creator", "", `{"creator":"@me:test"}`, "join", [3]bool{true, true, true}},
		{"v12 creator", `{"state_default":1000}`, `{"room_version":"12","additional_creators":["@me:test"]}`, "join", [3]bool{true, true, true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			empty, self := "", "@me:test"
			events := []matrixEvent{
				{Type: "m.room.member", StateKey: &self, Content: json.RawMessage(`{"membership":"` + tc.membership + `"}`)},
				{Type: "m.room.create", StateKey: &empty, Content: json.RawMessage(tc.create)},
			}
			if tc.powers != "" {
				events = append(events, matrixEvent{Type: "m.room.power_levels", StateKey: &empty, Content: json.RawMessage(tc.powers)})
			}
			_, n, d, p := roomMetadataPermissions(events, self)
			if got := [3]bool{n, d, p}; got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestUpdateGroupPhotoUploadsAndSetsState(t *testing.T) {
	p := NewProvider()
	if err := p.Init(core.ProviderConfig{"homeserver": "https://example.org", "access_token": "secret", "_instance_id": "matrix-2"}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing authentication")
		}
		switch calls {
		case 1:
			if r.Method != "POST" || r.URL.Path != "/_matrix/media/v3/upload" || r.Header.Get("Content-Type") != "image/png" {
				t.Fatalf("unexpected upload %v", r)
			}
			return jsonResponse(`{"content_uri":"mxc://example.org/photo"}`), nil
		case 2:
			if r.Method != "PUT" || r.URL.Path != "/_matrix/client/v3/rooms/!room:example.org/state/m.room.avatar" {
				t.Fatalf("unexpected state request %v", r)
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "mxc://example.org/photo") {
				t.Fatalf("unexpected body %s", body)
			}
			return jsonResponse(`{}`), nil
		default:
			t.Fatal("unexpected request")
			return nil, nil
		}
	})}
	if err := p.UpdateGroupPhoto("matrix-2::!room:example.org", []byte("\x89PNG\r\n\x1a\n")); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("got %d requests", calls)
	}
	if err := p.UpdateGroupPhoto("matrix-2::!room:example.org", []byte("text")); err == nil {
		t.Fatal("accepted non-image")
	}
}

func TestRoomSummaryDescription(t *testing.T) {
	s := summarizeRoomEvents(NewProvider(), []matrixEvent{{Type: "m.room.topic", Content: json.RawMessage(`{"topic":"Room description"}`)}})
	if s.Description != "Room description" {
		t.Fatalf("missing description: %+v", s)
	}
}

// A room with two members still supports state edits when its power levels allow them.
func TestTwoMemberRoomRetainsMetadataPermissions(t *testing.T) {
	self, other, empty := "@me:test", "@other:test", ""
	events := []matrixEvent{
		{Type: "m.room.member", StateKey: &self, Content: json.RawMessage(`{"membership":"join"}`)},
		{Type: "m.room.member", StateKey: &other, Content: json.RawMessage(`{"membership":"join"}`)},
		{Type: "m.room.power_levels", StateKey: &empty, Content: json.RawMessage(`{"users":{"@me:test":100}}`)},
	}
	summary := summarizeRoomEvents(NewProvider(), events)
	if !summary.IsDirect {
		t.Fatal("expected a two-member conversation")
	}
	member, name, topic, photo := roomMetadataPermissions(summary.Events, self)
	if !member || !name || !topic || !photo {
		t.Fatal("two-member room lost admin editing permissions")
	}
	_, name, topic, photo = roomMetadataPermissions(summary.Events, other)
	if name || topic || photo {
		t.Fatal("ordinary member received editing permissions")
	}
}
