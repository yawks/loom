package teams

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"go.mau.fi/mautrix-teams/pkg/msteams"
	"gorm.io/gorm"
)

func pendingRecording(status, kind string) msteams.Message {
	return msteams.Message{MessageType: "RichText/Media_CallRecording", Content: `<URIObject type="Video.2/CallRecording.1" uri=""><RecordingStatus status="` + status + `"/><Title>Meeting</Title><a href="">Play</a><RecordingContent contentTypes="` + kind + `"><item type="video" uri=""/></RecordingContent></URIObject>`}
}

func TestPendingMeetingRecordings(t *testing.T) {
	for _, status := range []string{"Initial", "ChunkFinished"} {
		for _, kind := range []string{"Recording", "Transcript"} {
			remote := pendingRecording(status, kind)
			if !isTeamsPendingRecording(remote) {
				t.Fatalf("not filtered: %s %s", status, kind)
			}
			p := NewProvider()
			p.handleRemoteEvent(nil, msteams.Event{Type: msteams.EventTypeNewMessage, Message: &remote})
			if len(p.eventChan) != 0 {
				t.Fatal("pending recording emitted")
			}
			for _, content := range []string{
				strings.Replace(remote.Content, `uri=""`, `uri="https://files.example/recording"`, 1),
				strings.Replace(remote.Content, `href=""`, `href="https://files.example/recording"`, 1),
				strings.Replace(remote.Content, `type="video" uri=""`, `type="video" uri="https://files.example/recording"`, 1),
			} {
				ready := remote
				ready.Content = content
				if isTeamsPendingRecording(ready) {
					t.Fatal("playable recording hidden")
				}
			}
		}
	}
	for _, remote := range []msteams.Message{{Content: "MeetingPlay"}, {MessageType: "RichText/Media_CallRecording", Content: "invalid"}, pendingRecording("Failed", "Recording")} {
		if isTeamsPendingRecording(remote) {
			t.Fatal("ordinary or failed message hidden")
		}
	}
}

func TestRemoveStoredPendingRecordingsIsProviderScoped(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous; sqlDB, _ := database.DB(); _ = sqlDB.Close() })
	rows := []models.Message{
		{ProtocolConvID: "teams-1::room", ProtocolMsgID: "pending", Body: "MeetingPlay"},
		{ProtocolConvID: "teams-1::room", ProtocolMsgID: "ordinary", Body: "MeetingPlay"},
		{ProtocolConvID: "teams-1::room", ProtocolMsgID: "ready", Body: "MeetingPlay"},
		{ProtocolConvID: "teams-2::room", ProtocolMsgID: "other", Body: "MeetingPlay"},
	}
	if err = database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	requests := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		requests[id] = true
		remote := pendingRecording("Initial", "Recording")
		if id == "ordinary" {
			remote.MessageType = "Text"
			remote.Content = "MeetingPlay"
		}
		if id == "ready" {
			remote.Content = strings.Replace(remote.Content, `href=""`, `href="https://files.example/recording"`, 1)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "messagetype": remote.MessageType, "content": remote.Content})
	}))
	defer server.Close()
	client, err := msteams.NewClient(msteams.ClientConfig{UserMRI: "8:orgid:self", AuthToken: "auth", SkypeToken: "skype", Endpoints: msteams.Endpoints{ChatSvcBase: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	p := NewProvider()
	p.instance = "teams-1"
	if err = p.removeStoredPendingRecordings(client); err != nil {
		t.Fatal(err)
	}
	if requests["other"] || len(requests) != 3 {
		t.Fatalf("unexpected work: %v", requests)
	}
	var remaining []models.Message
	if err = database.Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 3 {
		t.Fatalf("remaining: %+v", remaining)
	}
	for _, row := range remaining {
		if row.ProtocolMsgID == "pending" {
			t.Fatal("placeholder still visible")
		}
	}
}
