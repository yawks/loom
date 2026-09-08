package db

import (
	"Loom/pkg/models"
	"strings"

	"gorm.io/gorm"
)

// ProviderScope is the database boundary for background work owned by one
// configured provider instance. It fails closed when the instance ID is empty.
// Provider workers that enumerate shared tables must start from this scope.
type ProviderScope struct {
	database   *gorm.DB
	instanceID string
	prefix     string
	upperBound string
}

func ForProvider(database *gorm.DB, instanceID string) ProviderScope {
	instanceID = strings.TrimSpace(instanceID)
	prefix := instanceID + "::"
	return ProviderScope{
		database:   database,
		instanceID: instanceID,
		prefix:     prefix,
		// A range predicate preserves the protocol_conv_id index. Functions such
		// as substr(protocol_conv_id, ...) force SQLite to scan every provider's
		// rows during frequent polling and create cross-provider DB contention.
		upperBound: prefix + "\U0010ffff",
	}
}

func (scope ProviderScope) InstanceID() string { return scope.instanceID }

func (scope ProviderScope) OwnsConversation(conversationID string) bool {
	return scope.instanceID != "" && strings.HasPrefix(conversationID, scope.prefix)
}

func (scope ProviderScope) ConversationID(rawConversationID string) string {
	if rawConversationID == "" || scope.instanceID == "" || strings.Contains(rawConversationID, "::") {
		return rawConversationID
	}
	return scope.prefix + rawConversationID
}

func (scope ProviderScope) Messages() *gorm.DB {
	query := scope.database.Model(&models.Message{})
	if scope.instanceID == "" {
		return query.Where("1 = 0")
	}
	return query.Where(
		"messages.protocol_conv_id >= ? AND messages.protocol_conv_id < ?",
		scope.prefix, scope.upperBound,
	)
}

func (scope ProviderScope) Conversations() *gorm.DB {
	query := scope.database.Model(&models.Conversation{})
	if scope.instanceID == "" {
		return query.Where("1 = 0")
	}
	return query.Where(
		"conversations.protocol_conv_id >= ? AND conversations.protocol_conv_id < ?",
		scope.prefix, scope.upperBound,
	)
}

func (scope ProviderScope) LinkedAccounts() *gorm.DB {
	query := scope.database.Model(&models.LinkedAccount{})
	if scope.instanceID == "" {
		return query.Where("1 = 0")
	}
	return query.Where("linked_accounts.provider_instance_id = ?", scope.instanceID)
}
