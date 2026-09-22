package slack

import (
	"Loom/pkg/models"
	"encoding/json"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
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

func TestReconcileSentFilePlaceholderWithSlackMessage(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}
	placeholder := models.Message{
		ProtocolMsgID: "F123", ProtocolConvID: "slack-1::U123",
		Attachments: `[{"type":"video","url":"","fileName":"clip.mov"}]`,
	}
	if err := database.Create(&placeholder).Error; err != nil {
		t.Fatal(err)
	}
	real := models.Message{
		ProtocolMsgID: "1700000000.123", ProtocolConvID: placeholder.ProtocolConvID,
		Attachments: `[{"type":"video","url":"https://files.slack.com/files-pri/T-F123/download/clip.mov","fileName":"clip.mov"}]`,
	}
	provider := &SlackProvider{}
	superseded, err := provider.reconcileSentFilePlaceholder(database, &real, []string{"F123"})
	if err != nil {
		t.Fatal(err)
	}
	if superseded != "F123" || real.ID != placeholder.ID {
		t.Fatalf("unexpected reconciliation: superseded=%q message=%+v", superseded, real)
	}
	var rows []models.Message
	if err := database.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ProtocolMsgID != real.ProtocolMsgID || rows[0].Attachments != real.Attachments {
		t.Fatalf("placeholder was not replaced: %+v", rows)
	}
}

func TestStoreSentFileUsesEarlierSlackMessage(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}
	real := models.Message{
		ProtocolMsgID: "1700000000.123", ProtocolConvID: "slack-1::U123",
		Attachments: `[{"type":"video","url":"https://files.slack.com/files-pri/T-F123/download/clip.mov","fileName":"clip.mov"}]`,
	}
	if err := database.Create(&real).Error; err != nil {
		t.Fatal(err)
	}
	placeholder := models.Message{
		ProtocolMsgID: "F123", ProtocolConvID: real.ProtocolConvID,
		Attachments: `[{"type":"video","url":"","fileName":"clip.mov"}]`,
	}
	provider := &SlackProvider{}
	if err := provider.storeSentFile(database, &placeholder); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := database.Model(&models.Message{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 || placeholder.ProtocolMsgID != real.ProtocolMsgID || placeholder.ID != real.ID {
		t.Fatalf("created duplicate instead of using real message: count=%d message=%+v", count, placeholder)
	}
}
