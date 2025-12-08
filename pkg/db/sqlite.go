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

	// Manual migration for ProviderConfiguration: add instance_id and instance_name columns
	// This handles the case where the table already has data
	err = migrateProviderConfiguration(db)
	if err != nil {
		return fmt.Errorf("failed to migrate provider configuration: %w", err)
	}

	// Manual migration for LinkedAccount: add provider_instance_id column
	err = migrateLinkedAccount(db)
	if err != nil {
		return fmt.Errorf("failed to migrate linked account: %w", err)
	}

	DB = db
	fmt.Println("Database connection successful and schema migrated.")
	return nil
}

// migrateProviderConfiguration handles the migration of ProviderConfiguration table
// to add instance_id and instance_name columns for existing data
func migrateProviderConfiguration(db *gorm.DB) error {
	// Check if table exists first
	var tableExists int
	err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='provider_configurations'").Scan(&tableExists).Error
	if err != nil || tableExists == 0 {
		// Table doesn't exist yet, AutoMigrate will create it with the new schema
		fmt.Println("Migration: provider_configurations table doesn't exist yet, will be created by AutoMigrate")
		return nil
	}

	// Check if instance_id column exists by trying to query it
	var testValue string
	err = db.Raw("SELECT instance_id FROM provider_configurations LIMIT 1").Scan(&testValue).Error
	
	// If the query fails, the column doesn't exist yet
	if err != nil {
		fmt.Println("Migration: Adding instance_id and instance_name columns to provider_configurations...")
		
		// Step 1: Add columns as nullable first
		err = db.Exec("ALTER TABLE provider_configurations ADD COLUMN instance_id TEXT").Error
		if err != nil {
			// Column might already exist from a previous failed migration
			fmt.Printf("Migration: Warning - Could not add instance_id column: %v\n", err)
		}
		
		err = db.Exec("ALTER TABLE provider_configurations ADD COLUMN instance_name TEXT").Error
		if err != nil {
			fmt.Printf("Migration: Warning - Could not add instance_name column: %v\n", err)
		}
	}

	// Step 2: Fill existing rows with default values (always check, in case migration was partial)
	var configs []models.ProviderConfiguration
	if err := db.Find(&configs).Error; err == nil {
		for i, config := range configs {
			// Generate instance_id if not set
			if config.InstanceID == "" {
				instanceID := fmt.Sprintf("%s-%d", config.ProviderID, i+1)
				instanceName := config.ProviderID
				if config.InstanceName == "" {
					instanceName = config.ProviderID
				}
				
				db.Model(&config).Updates(map[string]interface{}{
					"instance_id":   instanceID,
					"instance_name": instanceName,
				})
				fmt.Printf("Migration: Updated provider %s with instance_id=%s, instance_name=%s\n", config.ProviderID, instanceID, instanceName)
			}
		}
		fmt.Println("Migration: ProviderConfiguration migration completed")
	}

	return nil
}

// migrateLinkedAccount handles the migration of LinkedAccount table
// to add provider_instance_id column for existing data
func migrateLinkedAccount(db *gorm.DB) error {
	// Check if table exists first
	var tableExists int
	err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='linked_accounts'").Scan(&tableExists).Error
	if err != nil || tableExists == 0 {
		// Table doesn't exist yet, AutoMigrate will create it with the new schema
		fmt.Println("Migration: linked_accounts table doesn't exist yet, will be created by AutoMigrate")
		return nil
	}

	// Check if provider_instance_id column exists by trying to query it
	var testValue string
	err = db.Raw("SELECT provider_instance_id FROM linked_accounts LIMIT 1").Scan(&testValue).Error
	
	// If the query fails, the column doesn't exist yet
	if err != nil {
		fmt.Println("Migration: Adding provider_instance_id column to linked_accounts...")
		
		// Add column as nullable
		err = db.Exec("ALTER TABLE linked_accounts ADD COLUMN provider_instance_id TEXT").Error
		if err != nil {
			fmt.Printf("Migration: Warning - Could not add provider_instance_id column: %v\n", err)
		} else {
			fmt.Println("Migration: provider_instance_id column added successfully")
		}
	}

	return nil
}
