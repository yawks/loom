package slack

import (
	"Loom/pkg/models"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestPersistSlackSentMessageCreatesAndUpdatesLocalCopy(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}

	message := &models.Message{
		ConversationID: 7,
		ProtocolConvID: "slack-test:C1",
		ProtocolMsgID:  "1700000000.000001",
		SenderID:       "U1",
		Body:           "first body",
		Timestamp:      time.Unix(1700000000, 1_000),
		IsFromMe:       true,
	}
	if err := persistSlackSentMessage(database, message); err != nil {
		t.Fatalf("persist new sent message: %v", err)
	}
	if message.ID == 0 {
		t.Fatal("persisted message did not receive its local ID")
	}

	localID := message.ID
	message.Body = "updated body"
	if err := persistSlackSentMessage(database, message); err != nil {
		t.Fatalf("upsert sent message: %v", err)
	}

	var stored models.Message
	if err := database.Where("protocol_msg_id = ?", message.ProtocolMsgID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ID != localID || stored.Body != "updated body" || !stored.IsFromMe {
		t.Fatalf("unexpected stored message: %+v", stored)
	}
	var count int64
	if err := database.Model(&models.Message{}).Where("protocol_msg_id = ?", message.ProtocolMsgID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("stored %d copies, want 1", count)
	}
}
