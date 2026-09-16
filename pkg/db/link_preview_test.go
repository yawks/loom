package db

import (
	"Loom/pkg/models"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestLinkPreviewPersistsAndExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "previews.db")
	open := func() *gorm.DB {
		database, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		sql, _ := database.DB()
		t.Cleanup(func() { _ = sql.Close() })
		return database
	}
	database := open()
	if err := database.AutoMigrate(&models.LinkPreviewCache{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expires := now.Add(7 * 24 * time.Hour)
	if err := SaveLinkPreview(database, "https://example.com", "thumbnail", expires); err != nil {
		t.Fatal(err)
	}
	reopened := open()
	row, err := LoadLinkPreview(reopened, "https://example.com", expires.Add(-time.Second))
	if err != nil || row.Payload != "thumbnail" || !row.ExpiresAt.Equal(expires) {
		t.Fatalf("persistent cache: %+v %v", row, err)
	}
	if _, err := LoadLinkPreview(reopened, row.URL, expires); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expired cache returned: %v", err)
	}
	if err := SaveLinkPreview(database, "https://other.example", "fresh", expires.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := DeleteExpiredLinkPreviews(database, expires); err != nil {
		t.Fatal(err)
	}
	var rows []models.LinkPreviewCache
	if err := database.Find(&rows).Error; err != nil || len(rows) != 1 || rows[0].URL != "https://other.example" {
		t.Fatalf("unexpected cleanup: %+v %v", rows, err)
	}
	if err := SaveLinkPreview(database, row.URL, "recomputed", expires.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadLinkPreview(reopened, row.URL, expires); err != nil || got.Payload != "recomputed" {
		t.Fatalf("recompute: %+v %v", got, err)
	}
}
