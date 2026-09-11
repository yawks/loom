package main

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"Loom/pkg/screenprotection"
	"reflect"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestConversationCaptureProtectionPersistenceAndIsolation(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.AutoMigrate(&models.ConversationCaptureProtection{}); err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous })
	app := &App{}
	settings, err := app.GetCaptureProtectionSettings()
	if err != nil || len(settings.ConversationIDs) != 0 {
		t.Fatalf("initial settings: %+v, %v", settings, err)
	}
	if settings.Supported != screenprotection.Supported() || settings.Limited != screenprotection.Limited() {
		t.Fatal("incorrect platform capability")
	}
	if err := app.SetConversationCaptureProtection(" ", true); err == nil {
		t.Fatal("accepted empty ID")
	}
	for _, id := range []string{"account-1::chat", "account-2::chat"} {
		if screenprotection.Supported() {
			for range 2 {
				if err := app.SetConversationCaptureProtection(id, true); err != nil {
					t.Fatal(err)
				}
			}
		} else {
			if err := app.SetConversationCaptureProtection(id, true); err == nil {
				t.Fatal("enabled on unsupported platform")
			}
			// Simulate preferences retained from a supported OS.
			if err := database.Create(&models.ConversationCaptureProtection{ConversationID: id}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	// A fresh application instance reads the persisted choices.
	restarted := &App{}
	settings, err = restarted.GetCaptureProtectionSettings()
	if err != nil || !reflect.DeepEqual(settings.ConversationIDs, []string{"account-1::chat", "account-2::chat"}) {
		t.Fatalf("persisted settings: %+v, %v", settings, err)
	}
	for range 2 {
		if err := restarted.SetConversationCaptureProtection("account-1::chat", false); err != nil {
			t.Fatal(err)
		}
	}
	settings, err = restarted.GetCaptureProtectionSettings()
	if err != nil || !reflect.DeepEqual(settings.ConversationIDs, []string{"account-2::chat"}) {
		t.Fatalf("other account preference changed: %+v, %v", settings, err)
	}
}

func TestConversationCaptureProtectionFailsWithoutDatabase(t *testing.T) {
	previous := db.DB
	db.DB = nil
	t.Cleanup(func() { db.DB = previous })
	app := &App{}
	if _, err := app.GetCaptureProtectionSettings(); err == nil {
		t.Fatal("missing database treated as empty settings")
	}
	if err := app.SetConversationCaptureProtection("account-1::chat", false); err == nil {
		t.Fatal("missing database treated as saved preference")
	}
}
