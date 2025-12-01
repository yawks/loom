package db

import (
	"Loom/pkg/models"
	"fmt"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

// InitDatabase initializes the connection to the SQLite database.
// The database will be stored in the application's configuration directory.
func InitDatabase() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("could not get user config dir: %w", err)
	}

	dbPath := filepath.Join(configDir, "Loom", "loom.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0750); err != nil {
		return fmt.Errorf("could not create db directory: %w", err)
	}

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// Auto-migrate schemas
	err = db.AutoMigrate(
		&models.MetaContact{},
		&models.LinkedAccount{},
		&models.Conversation{},
		&models.GroupParticipant{},
		&models.Message{},
		&models.Reaction{},
		&models.MessageReceipt{},
		&models.ProviderConfiguration{},
		&models.ContactAlias{},
		&models.LIDMapping{},
	)
	if err != nil {
		return fmt.Errorf("failed to auto-migrate database schema: %w", err)
	}

	DB = db
	fmt.Println("Database connection successful and schema migrated.")
	return nil
}
