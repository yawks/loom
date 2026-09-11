package main

import (
	"fmt"
	"strings"
	"time"

	"Loom/pkg/db"
	"Loom/pkg/models"
	"Loom/pkg/screenprotection"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CaptureProtectionSettings struct {
	Supported       bool     `json:"supported"`
	Limited         bool     `json:"limited"`
	ConversationIDs []string `json:"conversationIds"`
}

func (a *App) GetCaptureProtectionSettings() (CaptureProtectionSettings, error) {
	settings := CaptureProtectionSettings{
		Supported: screenprotection.Supported(), Limited: screenprotection.Limited(),
		ConversationIDs: []string{},
	}
	// Wails can serve binding calls while OnStartup is still migrating SQLite.
	// Wait for database publication, not for provider/network synchronization.
	if a.databaseReady != nil {
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		select {
		case <-a.databaseReady:
		case <-timer.C:
			return settings, fmt.Errorf("timed out waiting for database initialization")
		}
	}
	if db.DB == nil {
		return settings, fmt.Errorf("database unavailable")
	}
	err := db.DB.Model(&models.ConversationCaptureProtection{}).
		Order("conversation_id").Pluck("conversation_id", &settings.ConversationIDs).Error
	return settings, err
}

// SetConversationCaptureProtection is a local preference, kept separately from
// provider-owned conversation rows so synchronization cannot overwrite it.
func (a *App) SetConversationCaptureProtection(conversationID string, enabled bool) error {
	if strings.TrimSpace(conversationID) == "" {
		return fmt.Errorf("conversation ID is required")
	}
	if enabled && !screenprotection.Supported() {
		return fmt.Errorf("capture protection is unavailable on this operating system")
	}
	if db.DB == nil {
		return fmt.Errorf("database unavailable")
	}
	return db.Transaction(db.DB, func(tx *gorm.DB) error {
		if !enabled {
			return tx.Where("conversation_id = ?", conversationID).Delete(&models.ConversationCaptureProtection{}).Error
		}
		preference := models.ConversationCaptureProtection{ConversationID: conversationID}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&preference).Error
	})
}

// SetWindowCaptureProtection acknowledges the native operation, not just its
// scheduling. The renderer gates content until this call succeeds.
func (a *App) SetWindowCaptureProtection(enabled bool) error {
	return screenprotection.Set(enabled)
}
