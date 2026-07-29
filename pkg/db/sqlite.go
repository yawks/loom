package db

import (
	"Loom/pkg/models"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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

	// Configure SQLite with WAL mode and a long busy timeout.
	// cache=shared keeps a single shared page cache across the connection pool.
	db, err := gorm.Open(sqlite.Open(dbPath+"?_busy_timeout=30000&_journal_mode=WAL&cache=shared"), &gorm.Config{
		Logger: logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), logger.Config{
			SlowThreshold: 350 * time.Millisecond,
			LogLevel:      logger.Warn,
			Colorful:      true,
		}),
	})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// WAL mode allows concurrent readers + 1 writer.  Three open connections let
	// the frontend's read queries (GetAllLastMessages, GetAllLastMessageTimestamps,
	// GetAllMessageCounts) run in parallel with provider write goroutines without
	// serialising behind each other.  busy_timeout=30000 handles write-write
	// contention gracefully.
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(3)
	sqlDB.SetMaxIdleConns(2)

	// Enable WAL mode and tune pragmas.
	if err := db.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
		fmt.Printf("Warning: Failed to enable WAL mode: %v\n", err)
	}
	if err := db.Exec("PRAGMA synchronous=NORMAL").Error; err != nil {
		fmt.Printf("Warning: Failed to set synchronous=NORMAL: %v\n", err)
	}
	if err := db.Exec("PRAGMA busy_timeout=30000").Error; err != nil {
		fmt.Printf("Warning: Failed to set busy timeout: %v\n", err)
	}
	if err := db.Exec("PRAGMA cache_size=-32000").Error; err != nil { // 32 MB page cache
		fmt.Printf("Warning: Failed to set cache_size: %v\n", err)
	}
	if err := db.Exec("PRAGMA mmap_size=134217728").Error; err != nil { // 128 MB memory-mapped I/O
		fmt.Printf("Warning: Failed to set mmap_size: %v\n", err)
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

	// Namespace protocol_conv_id values with the provider instance ID
	if err := migrateNamespaceProtocolConvID(db); err != nil {
		fmt.Printf("Warning: Failed to namespace protocol_conv_id: %v\n", err)
	}

	// Performance optimization: ensure crucial indices exist
	err = ensureIndices(db)
	if err != nil {
		fmt.Printf("Warning: Failed to ensure some indices: %v\n", err)
	}

	DB = db
	fmt.Println("Database connection successful and schema migrated.")

	if err := ContactStore.Load(); err != nil {
		fmt.Printf("Warning: Failed to load contact store: %v\n", err)
	}

	return nil
}

// ensureIndices adds crucial performance indices that might be missing
func ensureIndices(db *gorm.DB) error {
	indices := []struct {
		Name  string
		Table string
		Cols  string
	}{
		{"idx_linked_accounts_meta_contact_id", "linked_accounts", "meta_contact_id"},
		{"idx_conversations_linked_account_id", "conversations", "linked_account_id"},
		{"idx_conversations_protocol_conv_id", "conversations", "protocol_conv_id"},
		{"idx_messages_protocol_conv_id_ts", "messages", "protocol_conv_id, timestamp"},
		{"idx_reactions_message_id", "reactions", "message_id"},
	}

	for _, idx := range indices {
		err := db.Exec(fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(%s)", idx.Name, idx.Table, idx.Cols)).Error
		if err != nil {
			fmt.Printf("Error creating index %s: %v\n", idx.Name, err)
		}
	}

	// Partial index: covers only non-deleted messages, enabling an index-only
	// scan for the GROUP BY + MAX(timestamp) pattern used in GetAllLastMessages.
	err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_conv_latest ON messages(protocol_conv_id, timestamp DESC) WHERE deleted_at IS NULL`).Error
	if err != nil {
		fmt.Printf("Error creating index idx_messages_conv_latest: %v\n", err)
	}

	return nil
}

// ParseTime converts various time representations from SQLite to int64 Unix timestamp.
func ParseTime(v interface{}) int64 {
	if v == nil {
		return 0
	}

	// Handle pointer types
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return 0
		}
		return ParseTime(rv.Elem().Interface())
	}

	switch val := v.(type) {
	case time.Time:
		return val.Unix()
	case int64:
		return val
	case float64:
		return int64(val)
	case int:
		return int64(val)
	case string:
		// SQLite often uses '2006-01-02 15:04:05.999999999+00:00'
		// or '2006-01-02 15:04:05' or RFC3339
		formats := []string{
			time.RFC3339,
			"2006-01-02 15:04:05.999999999-07:00",
			"2006-01-02 15:04:05.999999999",
			"2006-01-02 15:04:05",
			time.DateTime,
		}
		for _, f := range formats {
			if t, err := time.Parse(f, val); err == nil {
				return t.Unix()
			}
		}
		// Try to parse as float/int string
		var f float64
		if _, err := fmt.Sscanf(val, "%f", &f); err == nil {
			return int64(f)
		}
	case []byte:
		s := string(val)
		return ParseTime(s)
	}
	return 0
}

// ParseTimeMillis converts the SQLite time representations accepted by ParseTime
// to Unix milliseconds. Conversation ordering needs the extra precision: using
// whole seconds makes messages sent close together compare equal after restart.
func ParseTimeMillis(v interface{}) int64 {
	if v == nil {
		return 0
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return 0
		}
		return ParseTimeMillis(rv.Elem().Interface())
	}

	switch val := v.(type) {
	case time.Time:
		return val.UnixMilli()
	case string:
		formats := []string{
			time.RFC3339Nano,
			"2006-01-02 15:04:05.999999999-07:00",
			"2006-01-02 15:04:05.999999999",
			"2006-01-02 15:04:05",
			time.DateTime,
		}
		for _, f := range formats {
			if t, err := time.Parse(f, val); err == nil {
				return t.UnixMilli()
			}
		}
	case []byte:
		return ParseTimeMillis(string(val))
	}

	// Numeric database values historically represent Unix seconds.
	if seconds := ParseTime(v); seconds > 0 {
		return seconds * 1000
	}
	return 0
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

	// Check if unique constraint exists on (provider_instance_id, user_id)
	var constraintExists int
	err = db.Raw(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='index'
		AND name='idx_linked_accounts_provider_user'
	`).Scan(&constraintExists).Error

	if err == nil && constraintExists == 0 {
		fmt.Println("Migration: Adding unique index on (provider_instance_id, user_id) to linked_accounts...")
		// Create unique index (SQLite doesn't support adding constraints to existing tables easily)
		err = db.Exec(`
			CREATE UNIQUE INDEX IF NOT EXISTS idx_linked_accounts_provider_user
			ON linked_accounts(provider_instance_id, user_id)
		`).Error
		if err != nil {
			fmt.Printf("Migration: Warning - Could not create unique index: %v\n", err)
		} else {
			fmt.Println("Migration: Unique index created successfully")
		}
	}

	return nil
}

// migrateNamespaceProtocolConvID prefixes existing protocol_conv_id values in
// conversations and messages with the provider instance ID (e.g. "whatsapp-1::").
// This ensures conversations from different instances of the same provider are
// never conflated in the unique index.
func migrateNamespaceProtocolConvID(db *gorm.DB) error {
	// Check if any conversations already have the '::' separator — if so, the
	// migration has already been applied (at least partially).
	var already int64
	if err := db.Raw("SELECT COUNT(*) FROM conversations WHERE protocol_conv_id LIKE '%::%'").Scan(&already).Error; err != nil {
		return fmt.Errorf("namespace migration probe: %w", err)
	}
	if already > 0 {
		fmt.Println("Migration: protocol_conv_id already namespaced, skipping")
		return nil
	}

	// Prefix conversations: instanceID::rawConvID
	if err := db.Exec(`
		UPDATE conversations
		SET protocol_conv_id = (
			SELECT la.provider_instance_id || '::' || conversations.protocol_conv_id
			FROM linked_accounts la
			WHERE la.id = conversations.linked_account_id
			  AND la.provider_instance_id != ''
		)
		WHERE protocol_conv_id NOT LIKE '%::%'
		  AND EXISTS (
			SELECT 1 FROM linked_accounts la
			WHERE la.id = linked_account_id AND la.provider_instance_id != ''
		  )
	`).Error; err != nil {
		return fmt.Errorf("namespace conversations: %w", err)
	}

	// Prefix messages that are linked to a conversation (conversation_id > 0)
	if err := db.Exec(`
		UPDATE messages
		SET protocol_conv_id = (
			SELECT la.provider_instance_id || '::' || messages.protocol_conv_id
			FROM conversations c
			JOIN linked_accounts la ON la.id = c.linked_account_id
			WHERE c.id = messages.conversation_id
			  AND la.provider_instance_id != ''
		)
		WHERE protocol_conv_id NOT LIKE '%::%'
		  AND conversation_id > 0
		  AND EXISTS (
			SELECT 1 FROM conversations c
			JOIN linked_accounts la ON la.id = c.linked_account_id
			WHERE c.id = conversation_id AND la.provider_instance_id != ''
		  )
	`).Error; err != nil {
		return fmt.Errorf("namespace messages: %w", err)
	}

	fmt.Println("Migration: protocol_conv_id namespacing completed")
	return nil
}
