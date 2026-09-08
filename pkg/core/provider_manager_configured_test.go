package core

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestGetConfiguredProvidersUsesPersistedConfigurationBeforeRestore(t *testing.T) {
	previousDB := db.DB
	t.Cleanup(func() { db.DB = previousDB })
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.ProviderConfiguration{}); err != nil {
		t.Fatal(err)
	}
	db.DB = database
	config := models.ProviderConfiguration{
		ProviderID: "example", InstanceID: "example-1", InstanceName: "Work",
		ConfigJSON: `{"endpoint":"local"}`, IsActive: true,
	}
	if err := database.Create(&config).Error; err != nil {
		t.Fatal(err)
	}

	manager := NewProviderManager()
	manager.RegisterProvider("example", ProviderInfo{ID: "example", Name: "Example"}, nil)
	providers := manager.GetConfiguredProviders()
	if len(providers) != 1 {
		t.Fatalf("got %d configured providers before restore, want 1", len(providers))
	}
	if providers[0].InstanceID != "example-1" || providers[0].InstanceName != "Work" || !providers[0].IsActive {
		t.Fatalf("unexpected persisted provider: %+v", providers[0])
	}
	if providers[0].Config["endpoint"] != "local" {
		t.Fatalf("persisted config was not exposed: %+v", providers[0].Config)
	}
}
