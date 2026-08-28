package db

import (
	"testing"
	"time"

	"Loom/pkg/models"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestLatestOwnActivityUsesPersistedReactionTimestamp(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}, &models.Reaction{}); err != nil {
		t.Fatal(err)
	}
	previous := DB
	DB = database
	t.Cleanup(func() { DB = previous })

	base := time.Unix(100, 0).UTC()
	messages := []models.Message{
		{ProtocolConvID: "provider-1::conversation", ProtocolMsgID: "old", Timestamp: base},
		{ProtocolConvID: "provider-1::conversation", ProtocolMsgID: "latest", Timestamp: base.Add(time.Minute)},
		{ProtocolConvID: "provider-1::conversation", ProtocolMsgID: "mine", Timestamp: base.Add(2 * time.Minute), IsFromMe: true, SenderID: "self"},
	}
	if err := database.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}
	reactedAt := base.Add(3 * time.Minute)
	if err := database.Create(&models.Reaction{MessageID: messages[0].ID, UserID: "self", Emoji: "👍", CreatedAt: reactedAt}).Error; err != nil {
		t.Fatal(err)
	}

	got := LatestOwnActivityAt("provider-1::conversation", "SELF")
	if !got.Equal(reactedAt) {
		t.Fatalf("latest activity = %v, want reaction time %v", got, reactedAt)
	}
	readThrough := MessagesReadThrough("provider-1::conversation", got, 100)
	if len(readThrough) != 3 {
		t.Fatalf("read-through messages = %d, want 3", len(readThrough))
	}
}
