package whatsapp

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// reconcileContactConversations also repairs contacts persisted by older versions
// without a conversation. The frontend needs this canonical mapping to associate
// contacts with their namespaced messages, including messages received before discovery.
func (w *WhatsAppProvider) reconcileContactConversations() error {
	instanceID := w.getInstanceId()
	if strings.TrimSpace(instanceID) == "" {
		return fmt.Errorf("missing provider instance ID")
	}
	scope := db.ForProvider(db.DB, instanceID)
	var accounts []models.LinkedAccount
	if err := scope.LinkedAccounts().Find(&accounts).Error; err != nil {
		return err
	}
	var existing []models.Conversation
	if err := scope.Conversations().Find(&existing).Error; err != nil {
		return err
	}
	byID := make(map[string]models.Conversation, len(existing))
	for _, conv := range existing {
		byID[conv.ProtocolConvID] = conv
	}
	var missing []models.LinkedAccount
	for _, account := range accounts {
		id := core.BuildConvID(instanceID, account.UserID)
		if conv, ok := byID[id]; !ok || conv.LinkedAccountID != account.ID {
			missing = append(missing, account)
		}
	}
	if len(missing) > 0 {
		if err := db.Transaction(db.DB, func(tx *gorm.DB) error {
			// Rebuild GORM-mutated values on every retry.
			conversations := make([]models.Conversation, 0, len(missing))
			ids := make([]string, 0, len(missing))
			for _, account := range missing {
				id := core.BuildConvID(instanceID, account.UserID)
				conversations = append(conversations, models.Conversation{LinkedAccountID: account.ID, ProtocolConvID: id, IsGroup: account.IsGroup, GroupName: account.Username})
				ids = append(ids, id)
			}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "protocol_conv_id"}}, DoUpdates: clause.AssignmentColumns([]string{"linked_account_id"})}).CreateInBatches(&conversations, 100).Error; err != nil {
				return err
			}
			// Messages can arrive before the group directory. Repair their relational
			// link in batches without changing their contents or read state.
			for start := 0; start < len(ids); start += 100 {
				end := start + 100
				if end > len(ids) {
					end = len(ids)
				}
				if err := db.ForProvider(tx, instanceID).Messages().Where("protocol_conv_id IN ? AND (conversation_id = 0 OR conversation_id IS NULL)", ids[start:end]).Update("conversation_id", gorm.Expr("(SELECT id FROM conversations WHERE conversations.protocol_conv_id = messages.protocol_conv_id)")).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	// Publish only after commit, including mappings absent from a stale cache.
	for _, account := range accounts {
		db.ContactStore.UpsertConversation(account.ID, core.BuildConvID(instanceID, account.UserID))
	}
	return nil
}
