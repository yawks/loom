package teams

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"github.com/glebarez/sqlite"
	"go.mau.fi/mautrix-teams/pkg/msteams"
	"gorm.io/gorm"
	"testing"
)

func TestDirectMentionHighlight(t *testing.T) {
	client, err := msteams.NewClient(msteams.ClientConfig{TenantID: "tenant", UserMRI: "8:orgid:self", RefreshToken: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	for _, tc := range []struct {
		name, content, sender string
		mentions              []msteams.Mention
		want                  bool
	}{
		{"html", `<at id="8:orgid:self">Self</at>`, "other", nil, true},
		{"metadata", "Hello", "other", []msteams.Mention{{UserID: "8:orgid:self"}}, true},
		{"bare identity", "Hello", "other", []msteams.Mention{{UserID: "SELF"}}, true},
		{"other user", `<at id="8:orgid:other">Other</at>`, "other", nil, false},
		{"outgoing", `<at id="8:orgid:self">Self</at>`, "8:orgid:self", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := NewProvider().toModelMessage(client, msteams.Message{ID: "message", From: tc.sender, Content: tc.content, ContentType: "html", Mentions: tc.mentions}, "room")
			if got := len(message.HighlightReasons) > 0; got != tc.want {
				t.Fatalf("highlight=%v want %v", message.HighlightReasons, tc.want)
			}
		})
	}
}

func TestStoreMessagesRefreshesHighlightReasons(t *testing.T) {
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
	message := models.Message{ProtocolConvID: conversation.ProtocolConvID, ProtocolMsgID: "mention"}
	if err := provider.storeMessages([]models.Message{message}); err != nil {
		t.Fatal(err)
	}
	for _, reasons := range [][]string{{models.HighlightReasonDirectMention}, nil} {
		message.HighlightReasons = reasons
		if err := provider.storeMessages([]models.Message{message}); err != nil {
			t.Fatal(err)
		}
		var stored models.Message
		if err := database.Where("protocol_msg_id = ?", message.ProtocolMsgID).First(&stored).Error; err != nil {
			t.Fatal(err)
		}
		if len(stored.HighlightReasons) != len(reasons) {
			t.Fatalf("stored highlights=%v want %v", stored.HighlightReasons, reasons)
		}
	}
}
