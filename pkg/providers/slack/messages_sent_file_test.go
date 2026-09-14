package slack

import (
	"Loom/pkg/models"
	"encoding/json"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"testing"
)

func TestStoreSentFileReconcilesEarlySocketEvent(t *testing.T) {
	for _, initial := range []string{"", "[]", "null", `[{"type":"document","url":"","fileName":"report.pdf"}]`} {
		t.Run(initial, func(t *testing.T) {
			database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			if err := database.AutoMigrate(&models.Message{}); err != nil {
				t.Fatal(err)
			}
			existing := models.Message{ProtocolMsgID: "123.456", ProtocolConvID: "slack-1::U123", Attachments: initial, Body: "keep caption"}
			if err := database.Create(&existing).Error; err != nil {
				t.Fatal(err)
			}
			sent := models.Message{ProtocolMsgID: existing.ProtocolMsgID, ProtocolConvID: existing.ProtocolConvID, Attachments: `[{"type":"document","url":"https://files.slack.com/report.pdf","fileName":"report.pdf"}]`}
			provider := &SlackProvider{}
			for i := 0; i < 2; i++ {
				if err := provider.storeSentFile(database, &sent); err != nil {
					t.Fatal(err)
				}
			}
			var rows []models.Message
			if err := database.Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("got %d messages", len(rows))
			}
			var attachments []models.Attachment
			if err := json.Unmarshal([]byte(rows[0].Attachments), &attachments); err != nil {
				t.Fatal(err)
			}
			if len(attachments) != 1 || attachments[0].URL == "" {
				t.Fatalf("missing or duplicated file: %+v", attachments)
			}
			if sent.ID != existing.ID || sent.Attachments != rows[0].Attachments || sent.Body != "keep caption" {
				t.Fatalf("event differs from stored message: %+v", sent)
			}
		})
	}
}
