package googlemessages

import (
	"fmt"
	"strings"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"gorm.io/gorm"
)

const participantPrefix = "participant:"

var _ core.ParticipantIdentityNormalizer = (*Provider)(nil)

// Google uses independent numeric sequences for conversations and participants.
// LinkedAccount.UserID identifies a conversation; it must never be a sender key.
func (p *Provider) NormalizeParticipantID(id string) string {
	return googleMessagesParticipantID(id)
}

func googleMessagesParticipantID(id string) string {
	if id == "" || strings.HasPrefix(id, participantPrefix) || strings.Contains(id, "::") {
		return id
	}
	return participantPrefix + id
}

// Repair old participant IDs in bounded SQL batches. Ambiguous unqualified
// cached profiles are intentionally NOT copied: many contain conversation names.
// Names already stored on messages remain authoritative fallbacks until refreshed.
func (p *Provider) repairParticipantIDs() error {
	if p.instance == "" {
		return fmt.Errorf("provider instance ID is required")
	}
	if db.DB == nil {
		return nil
	}
	return db.Transaction(db.DB, func(tx *gorm.DB) error {
		for _, column := range []string{"sender_id", "quoted_sender_id"} {
			if err := db.ForProvider(tx, p.instance).Messages().Where(column+" <> '' AND "+column+" NOT LIKE ? AND "+column+" NOT LIKE ?", participantPrefix+"%", "%::%").Update(column, gorm.Expr("? || "+column, participantPrefix)).Error; err != nil {
				return err
			}
		}
		messageIDs := db.ForProvider(tx, p.instance).Messages().Select("messages.id")
		for _, model := range []any{&models.Reaction{}, &models.MessageReceipt{}} {
			if err := tx.Model(model).Where("message_id IN (?)", messageIDs).Where("user_id <> '' AND user_id NOT LIKE ? AND user_id NOT LIKE ?", participantPrefix+"%", "%::%").Update("user_id", gorm.Expr("? || user_id", participantPrefix)).Error; err != nil {
				return err
			}
		}
		conversationIDs := db.ForProvider(tx, p.instance).Conversations().Select("conversations.id")
		return tx.Model(&models.GroupParticipant{}).Where("conversation_id IN (?)", conversationIDs).Where("user_id <> '' AND user_id NOT LIKE ? AND user_id NOT LIKE ?", participantPrefix+"%", "%::%").Update("user_id", gorm.Expr("? || user_id", participantPrefix)).Error
	})
}
