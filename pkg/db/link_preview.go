package db

import (
	"Loom/pkg/models"
	"context"
	"log"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func LoadLinkPreview(database *gorm.DB, url string, now time.Time) (models.LinkPreviewCache, error) {
	var row models.LinkPreviewCache
	err := database.Where("url = ? AND expires_at > ?", url, now).Take(&row).Error
	return row, err
}

func SaveLinkPreview(database *gorm.DB, url, payload string, expires time.Time) error {
	return Transaction(database, func(tx *gorm.DB) error {
		row := models.LinkPreviewCache{URL: url, Payload: payload, ExpiresAt: expires}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "url"}}, DoUpdates: clause.AssignmentColumns([]string{"payload", "expires_at"})}).Create(&row).Error
	})
}

func DeleteExpiredLinkPreviews(database *gorm.DB, now time.Time) error {
	return Transaction(database, func(tx *gorm.DB) error {
		return tx.Where("expires_at <= ?", now).Delete(&models.LinkPreviewCache{}).Error
	})
}

// Purge on startup and hourly even when no conversations are opened. Expired
// entries are excluded from reads immediately, independently of this sweep.
func MaintainLinkPreviewCache(ctx context.Context, database *gorm.DB) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := DeleteExpiredLinkPreviews(database, time.Now()); err != nil {
			log.Printf("[link preview] cache cleanup failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
