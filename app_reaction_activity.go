package main

import (
	"fmt"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func persistReactionActivity(database *gorm.DB, message models.Message, event core.ReactionEvent, currentUserID string) error {
	if !db.ForProvider(database, event.InstanceID).OwnsConversation(event.ConversationID) {
		return fmt.Errorf("reaction activity has no conversation owner")
	}
	if message.ProtocolConvID != event.ConversationID || message.ProtocolMsgID != event.MessageID {
		return fmt.Errorf("reaction activity targets another message")
	}
	if event.Added {
		if _, eligible := core.ReactionActivityCandidate(message, event, currentUserID); !eligible {
			return nil
		}
	}
	if !message.IsFromMe {
		return nil
	}
	return db.Transaction(database, func(tx *gorm.DB) error {
		query := tx.Model(&models.ReactionActivity{}).Where("message_id = ? AND user_id = ?", message.ID, event.UserID)
		if event.Emoji != "" {
			query = query.Where("emoji = ?", event.Emoji)
		}
		if !event.Added {
			return query.Update("active", false).Error
		}
		if event.Emoji == "" {
			return nil
		}
		var active int64
		if err := query.Where("active = ?", true).Count(&active).Error; err != nil || active > 0 {
			return err
		}
		// Construct inside the callback: a retried transaction must start without
		// an ID assigned by a rolled-back Create.
		activity, _ := core.ReactionActivityCandidate(message, event, currentUserID)
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&activity).Error
	})
}

// GetReactionActivitySummaries returns one preview and an unread total per
// conversation. Names come from local, instance-scoped identities; no network
// enrichment can block rendering the conversation list.
func (a *App) GetReactionActivitySummaries() (map[string]models.ReactionActivitySummary, error) {
	result := map[string]models.ReactionActivitySummary{}
	if db.DB == nil {
		return result, nil
	}
	var rows []struct {
		models.ReactionActivity
		UnreadCount int
		ThroughID   uint
	}
	err := db.DB.Raw(`
		WITH ranked AS (
			SELECT ra.*,
				SUM(CASE WHEN ra.seen = 0 THEN 1 ELSE 0 END) OVER (PARTITION BY ra.conversation_id) AS unread_count,
				MAX(ra.id) OVER (PARTITION BY ra.conversation_id) AS through_id,
				ROW_NUMBER() OVER (PARTITION BY ra.conversation_id ORDER BY julianday(ra.timestamp) DESC, ra.id DESC) AS rank
			FROM reaction_activities ra JOIN messages m ON m.id = ra.message_id
			WHERE ra.active = 1 AND m.deleted_at IS NULL
		)
		SELECT ranked.*,
			COALESCE(
				(SELECT alias FROM contact_aliases WHERE user_id = ranked.user_id LIMIT 1),
				(SELECT NULLIF(display_name, '') FROM participant_profiles WHERE provider_instance_id = ranked.provider_instance_id AND user_id = ranked.user_id LIMIT 1),
				(SELECT NULLIF(username, '') FROM linked_accounts WHERE provider_instance_id = ranked.provider_instance_id AND user_id = ranked.user_id LIMIT 1),
				''
			) AS user_name
		FROM ranked WHERE rank = 1
	`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.ConversationID] = models.ReactionActivitySummary{Latest: row.ReactionActivity, UnreadCount: row.UnreadCount, ThroughID: row.ThroughID}
	}
	return result, nil
}

// MarkReactionActivitiesSeen acknowledges only the fetched snapshot. A reaction
// inserted while the renderer was opening the conversation remains unread.
func (a *App) MarkReactionActivitiesSeen(conversationID string, throughID uint) ([]models.ReactionActivity, error) {
	activities := []models.ReactionActivity{}
	if db.DB == nil || conversationID == "" || throughID == 0 {
		return activities, nil
	}
	err := db.Transaction(db.DB, func(tx *gorm.DB) error {
		// Return exactly the newly acknowledged reactions so the renderer can
		// highlight every affected pastille, even after the unread badge clears.
		activities = []models.ReactionActivity{}
		if err := tx.Where("conversation_id = ? AND id <= ? AND seen = ? AND active = ?", conversationID, throughID, false, true).
			Find(&activities).Error; err != nil {
			return err
		}
		return tx.Model(&models.ReactionActivity{}).
			Where("conversation_id = ? AND id <= ? AND seen = ?", conversationID, throughID, false).
			Update("seen", true).Error
	})
	if err != nil {
		return nil, err
	}
	return activities, nil
}
