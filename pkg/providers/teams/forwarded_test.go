package teams

import (
	"encoding/json"
	"strings"
	"testing"

	"Loom/pkg/db"
	"Loom/pkg/models"
	"github.com/glebarez/sqlite"
	"go.mau.fi/mautrix-teams/pkg/msteams"
	"gorm.io/gorm"
)

func TestForwardedMessageWithGIFAndVideo(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{TenantID: "tenant", UserMRI: "self", RefreshToken: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	message := NewProvider().toModelMessage(client, msteams.Message{
		ID: "forward", MessageType: "RichText/Html", ContentType: "Text",
		Content:     `<p><img itemtype="http://schema.skype.com/Emoji" alt="🥵"></p><blockquote itemtype="http://schema.skype.com/Forward"><p>Forwarded text</p><img src="https://media.example/giphy.gif?a=1&amp;b=2" alt="Fight GIF"><img src="https://media.example/giphy.gif?a=1&amp;b=2"></blockquote>`,
		SharedFiles: []msteams.SharedFile{{Name: "recording.mov", ShareURL: "https://files.example/recording.mov"}},
	}, "thread")
	if !message.IsForwarded || message.QuotedMessageID != nil {
		t.Fatalf("incorrect forward metadata: %+v", message)
	}
	if !strings.Contains(message.Body, "🥵") || !strings.Contains(message.Body, "Forwarded text") || strings.Contains(message.Body, "<img") {
		t.Fatalf("body=%q", message.Body)
	}
	var attachments []models.Attachment
	if err := json.Unmarshal([]byte(message.Attachments), &attachments); err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 2 {
		t.Fatalf("attachments=%+v", attachments)
	}
	if attachments[0].Type != "video" || attachments[1].Type != "image" || attachments[1].MimeType != "image/gif" || attachments[1].URL != "https://media.example/giphy.gif?a=1&b=2" {
		t.Fatalf("attachments=%+v", attachments)
	}
}

func TestEmbeddedMediaExcludesReplyPreviewsAndUnsafeURLs(t *testing.T) {
	media, forwarded := teamsEmbeddedMedia(`<blockquote itemtype="http://schema.skype.com/Reply" itemid="parent"><blockquote itemtype="http://schema.skype.com/Forward"><img src="https://example.test/quoted.gif"></blockquote></blockquote><img src="javascript:alert(1)"><img src="data:image/gif;base64,AAAA"><img itemtype="http://schema.skype.com/Emoji" src="https://example.test/emoji.png"><img itemtype="http://schema.skype.com/Giphy" src="https://example.test/actual.gif">`)
	if forwarded || len(media) != 1 || media[0].URL != "https://example.test/actual.gif" {
		t.Fatalf("forwarded=%v media=%+v", forwarded, media)
	}
}

func TestForwardTemplateMetadata(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{TenantID: "tenant", UserMRI: "self", RefreshToken: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	for _, tc := range []struct {
		template any
		want     bool
	}{{"basic_forward_message_template", true}, {"", false}, {nil, false}} {
		message := NewProvider().toModelMessage(client, msteams.Message{Content: "text", Properties: map[string]any{"forwardTemplateId": tc.template}}, "thread")
		if message.IsForwarded != tc.want {
			t.Fatalf("template=%v forwarded=%v", tc.template, message.IsForwarded)
		}
	}
}

func TestStoreMessagesRefreshesForwardedMetadata(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Conversation{}, &models.Message{}); err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous; sqlDB, _ := database.DB(); _ = sqlDB.Close() })
	conversation := models.Conversation{ProtocolConvID: "teams-1::room"}
	if err := database.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	provider := NewProvider()
	message := models.Message{ProtocolConvID: conversation.ProtocolConvID, ProtocolMsgID: "forward"}
	if err := provider.storeMessages([]models.Message{message}); err != nil {
		t.Fatal(err)
	}
	message.IsForwarded = true
	message.Attachments = `[{"type":"image","url":"https://example.test/giphy.gif","mimeType":"image/gif"}]`
	if err := provider.storeMessages([]models.Message{message}); err != nil {
		t.Fatal(err)
	}
	// A partial live echo must not clear metadata recovered from history.
	message.IsForwarded = false
	message.Attachments = ""
	if err := provider.storeMessages([]models.Message{message}); err != nil {
		t.Fatal(err)
	}
	var stored models.Message
	if err := database.Where("protocol_msg_id = ?", message.ProtocolMsgID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.IsForwarded || !strings.Contains(stored.Attachments, "giphy.gif") {
		t.Fatalf("stored=%+v", stored)
	}
}
