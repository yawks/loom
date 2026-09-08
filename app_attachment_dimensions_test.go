package main

import (
	"Loom/pkg/models"
	"encoding/json"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestPersistAttachmentDimensions(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:attachment-dimensions?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}

	message := models.Message{
		ProtocolMsgID: "remote-message",
		Attachments:   `[{"type":"image","url":"image-one","fileName":"one.png","fileSize":12,"mimeType":"image/png"},{"type":"image","url":"image-two","fileName":"two.png","fileSize":34,"mimeType":"image/png","width":80,"height":60}]`,
	}
	if err := database.Create(&message).Error; err != nil {
		t.Fatal(err)
	}

	if err := persistAttachmentDimensions(database, "remote-message", "image-one", 1600, 200); err != nil {
		t.Fatal(err)
	}

	var stored models.Message
	if err := database.First(&stored, message.ID).Error; err != nil {
		t.Fatal(err)
	}
	var attachments []models.Attachment
	if err := json.Unmarshal([]byte(stored.Attachments), &attachments); err != nil {
		t.Fatal(err)
	}
	if attachments[0].Width != 1600 || attachments[0].Height != 200 {
		t.Fatalf("dimensions = %dx%d, want 1600x200", attachments[0].Width, attachments[0].Height)
	}

	// Provider-supplied dimensions are authoritative and must not be overwritten.
	if err := persistAttachmentDimensions(database, "remote-message", "image-two", 800, 600); err != nil {
		t.Fatal(err)
	}
	if err := database.First(&stored, message.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(stored.Attachments), &attachments); err != nil {
		t.Fatal(err)
	}
	if attachments[1].Width != 80 || attachments[1].Height != 60 {
		t.Fatalf("provider dimensions were overwritten: %dx%d", attachments[1].Width, attachments[1].Height)
	}
}
