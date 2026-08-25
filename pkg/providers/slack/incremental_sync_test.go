package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestSlackConversationSyncRowsIncludesStaleConversationBeyondFirstFifty(t *testing.T) {
	previousDB := db.DB
	t.Cleanup(func() { db.DB = previousDB })

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.DB = database
	if err := db.DB.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}

	provider := NewSlackProvider()
	provider.config = core.ProviderConfig{"_instance_id": "slack-1"}

	messages := make([]models.Message, 0, 53)
	for i := 0; i < 51; i++ {
		messages = append(messages, models.Message{
			ProtocolConvID: fmt.Sprintf("slack-1::C%03d", i),
			ProtocolMsgID:  fmt.Sprintf("recent-%03d", i),
			Timestamp:      time.Now().Add(-time.Duration(i) * time.Minute),
		})
	}
	messages = append(messages,
		models.Message{
			ProtocolConvID: "slack-1::C01J3DSNSKY",
			ProtocolMsgID:  "stale-target",
			Timestamp:      time.Now().AddDate(0, -2, 0),
		},
		models.Message{
			ProtocolConvID: "teams-1::C-foreign",
			ProtocolMsgID:  "foreign",
			Timestamp:      time.Now(),
		},
	)
	if err := db.DB.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}

	rows, err := provider.slackConversationSyncRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 52 {
		t.Fatalf("got %d Slack conversations, want 52", len(rows))
	}
	foundTarget := false
	for _, row := range rows {
		if row.ProtocolConvID == "teams-1::C-foreign" {
			t.Fatal("sync query included a conversation owned by another provider")
		}
		if row.ProtocolConvID == "slack-1::C01J3DSNSKY" {
			foundTarget = true
		}
	}
	if !foundTarget {
		t.Fatal("stale Slack conversation was excluded from incremental sync")
	}
}
