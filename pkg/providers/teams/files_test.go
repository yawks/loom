package teams

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"testing"
)

func TestFirstNonEmptySharedFileURLFallsBackToStoredAttachmentURL(t *testing.T) {
	const storedURL = "https://tenant.sharepoint.com/personal/user/Documents/video.mov"
	if got := firstNonEmptySharedFileURL("", "", storedURL); got != storedURL {
		t.Fatalf("unexpected fallback URL: %q", got)
	}
}

func TestFirstNonEmptySharedFileURLPrefersShareURL(t *testing.T) {
	const shareURL = "https://tenant.sharepoint.com/:v:/g/shared"
	if got := firstNonEmptySharedFileURL(shareURL, "https://tenant.sharepoint.com/file", "stored"); got != shareURL {
		t.Fatalf("unexpected preferred URL: %q", got)
	}
}

func TestSharePointUploadLocationDoesNotReadOtherProviders(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous; sqlDB, _ := database.DB(); _ = sqlDB.Close() })
	if err := database.AutoMigrate(&models.Message{}); err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&models.Message{ProtocolConvID: "other::chat", ProtocolMsgID: "foreign", Attachments: "[]"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, instance := range []string{"teams-own", ""} {
		p := NewProvider()
		p.instance = instance
		// A nil client would panic if a foreign message were fetched as work.
		if _, err := p.sharePointUploadLocation(nil); err == nil {
			t.Fatal("expected no upload location")
		}
	}
}
