package core

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"Loom/pkg/db"
	"Loom/pkg/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReactionActivityCandidate is shared by live and recovered reactions. History
// imports never create attention; a removal is still allowed to dismiss it.
func ReactionActivityCandidate(message models.Message, event ReactionEvent, currentUserID string) (models.ReactionActivity, bool) {
	if event.Historical || !event.Added || !message.IsFromMe || event.UserID == "" || event.Emoji == "" ||
		strings.EqualFold(event.UserID, currentUserID) || strings.EqualFold(event.UserID, message.SenderID) {
		return models.ReactionActivity{}, false
	}
	timestamp := time.Unix(event.Timestamp, 0)
	if event.Timestamp <= 0 {
		timestamp = time.Now()
	}
	activity := models.ReactionActivity{
		ProviderInstanceID: event.InstanceID, ConversationID: event.ConversationID,
		MessageID: message.ID, ProtocolMessageID: message.ProtocolMsgID,
		UserID: event.UserID, Emoji: event.Emoji, Timestamp: timestamp, Active: true,
	}
	if message.ThreadID != nil && *message.ThreadID != message.ProtocolMsgID {
		activity.ThreadID = *message.ThreadID
	}
	return activity, true
}

type reactionIdentity struct {
	messageID uint
	userID    string
	emoji     string
}

type reactionEventIdentity struct {
	reactionIdentity
	timestamp int64
}

// StoreReactionBatch applies a conversation's history reactions in one retryable
// transaction. Existing keys are loaded once and writes are batched, including
// attention state, so a crash cannot save a reaction but lose its unread signal.
func StoreReactionBatch(database *gorm.DB, batch ReactionBatchEvent, currentUserID string) error {
	if database == nil || !db.ForProvider(database, batch.InstanceID).OwnsConversation(batch.ConversationID) {
		return fmt.Errorf("reaction batch has no database or conversation owner")
	}
	if err := ValidateProviderEventOwnership(batch.InstanceID, batch); err != nil {
		return err
	}
	if len(batch.Reactions) == 0 {
		return nil
	}
	changes := append([]ReactionEvent(nil), batch.Reactions...)
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Timestamp < changes[j].Timestamp })
	return db.Transaction(database, func(tx *gorm.DB) error {
		protocolIDs := make([]string, 0, len(changes))
		unique := map[string]bool{}
		for _, change := range changes {
			if !unique[change.MessageID] {
				protocolIDs = append(protocolIDs, change.MessageID)
				unique[change.MessageID] = true
			}
		}
		messages := map[string]models.Message{}
		var ids []uint
		var reactions []models.Reaction
		var receipts []models.MessageReceipt
		var activities []models.ReactionActivity
		for start := 0; start < len(protocolIDs); start += 200 {
			var rows []models.Message
			if err := db.ForProvider(tx, batch.InstanceID).MessagesForConversation(batch.ConversationID).
				Where("protocol_msg_id IN ?", protocolIDs[start:min(start+200, len(protocolIDs))]).Find(&rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				messages[row.ProtocolMsgID] = row
				ids = append(ids, row.ID)
			}
		}
		for start := 0; start < len(ids); start += 200 {
			chunk := ids[start:min(start+200, len(ids))]
			var rs []models.Reaction
			var rcs []models.MessageReceipt
			var as []models.ReactionActivity
			if err := tx.Where("message_id IN ?", chunk).Find(&rs).Error; err != nil {
				return err
			}
			if err := tx.Where("message_id IN ?", chunk).Find(&rcs).Error; err != nil {
				return err
			}
			if err := tx.Where("message_id IN ?", chunk).Find(&as).Error; err != nil {
				return err
			}
			reactions = append(reactions, rs...)
			receipts = append(receipts, rcs...)
			activities = append(activities, as...)
		}
		current := map[reactionIdentity]models.Reaction{}
		for _, row := range reactions {
			current[reactionIdentity{row.MessageID, row.UserID, row.Emoji}] = row
		}
		read := map[reactionIdentity]bool{}
		for _, row := range receipts {
			if row.ReceiptType == string(ReceiptTypeRead) {
				read[reactionIdentity{row.MessageID, row.UserID, ""}] = true
			}
		}
		active := map[reactionIdentity]int{}
		recorded := map[reactionEventIdentity]int{}
		byParticipant := map[reactionIdentity]map[reactionIdentity]bool{}
		indexKey := func(key reactionIdentity) {
			participant := reactionIdentity{key.messageID, key.userID, ""}
			if byParticipant[participant] == nil {
				byParticipant[participant] = map[reactionIdentity]bool{}
			}
			byParticipant[participant][key] = true
		}
		for key := range current {
			indexKey(key)
		}
		for index, row := range activities {
			key := reactionIdentity{row.MessageID, row.UserID, row.Emoji}
			recorded[reactionEventIdentity{key, row.Timestamp.Unix()}] = index
			if row.Active {
				active[key] = index
				indexKey(key)
			}
		}
		deactivated := map[uint]bool{}
		deactivate := func(key reactionIdentity, index int) {
			activities[index].Active = false
			if activities[index].ID != 0 {
				deactivated[activities[index].ID] = true
			}
			delete(active, key)
		}
		var newReceipts []models.MessageReceipt
		for _, change := range changes {
			message, exists := messages[change.MessageID]
			if !exists || change.UserID == "" {
				continue
			}
			key := reactionIdentity{message.ID, change.UserID, change.Emoji}
			participant := reactionIdentity{message.ID, change.UserID, ""}
			if !change.Added {
				for existing := range byParticipant[participant] {
					if change.Emoji == "" || existing.emoji == change.Emoji {
						if index, exists := active[existing]; exists && activities[index].Timestamp.Unix() > change.Timestamp {
							continue // a delayed removal must not erase a newer re-add
						}
						delete(current, existing)
						if index, exists := active[existing]; exists {
							deactivate(existing, index)
						}
					}
				}
				continue
			}
			if change.Emoji == "" {
				continue
			}
			eventKey := reactionEventIdentity{key, change.Timestamp}
			if index, known := recorded[eventKey]; known && !activities[index].Active {
				continue
			}
			if index, exists := active[key]; exists {
				previous := activities[index].Timestamp.Unix()
				if previous > change.Timestamp {
					continue
				}
				if previous < change.Timestamp {
					// The same emoji can be removed and re-added while Loom is off.
					// A newer sender timestamp identifies a new activity even if the
					// snapshot no longer contains the intermediate removal.
					deactivate(key, index)
				}
			}
			indexKey(key)
			if _, exists := current[key]; !exists {
				timestamp := time.Unix(change.Timestamp, 0)
				current[key] = models.Reaction{MessageID: message.ID, UserID: change.UserID, Emoji: change.Emoji, CreatedAt: timestamp, UpdatedAt: timestamp}
			} else if row := current[key]; time.Unix(change.Timestamp, 0).After(row.CreatedAt) {
				row.CreatedAt = time.Unix(change.Timestamp, 0)
				row.UpdatedAt = row.CreatedAt
				current[key] = row
			}
			readKey := reactionIdentity{message.ID, change.UserID, ""}
			if !read[readKey] && change.UserID != message.SenderID {
				newReceipts = append(newReceipts, models.MessageReceipt{MessageID: message.ID, UserID: change.UserID, ReceiptType: string(ReceiptTypeRead), Timestamp: time.Unix(change.Timestamp, 0)})
				read[readKey] = true
			}
			if _, exists := active[key]; !exists {
				if candidate, eligible := ReactionActivityCandidate(message, change, currentUserID); eligible {
					// Keep removed/seen event tombstones: replaying a history chunk
					// must not resurrect an old activity, even after another restart.
					if _, duplicate := recorded[eventKey]; !duplicate {
						active[key] = len(activities)
						recorded[eventKey] = len(activities)
						activities = append(activities, candidate)
					}
				}
			}
		}
		var removed []uint
		var updated []models.Reaction
		for _, old := range reactions {
			if row, exists := current[reactionIdentity{old.MessageID, old.UserID, old.Emoji}]; !exists {
				removed = append(removed, old.ID)
			} else if row.ID == old.ID && !row.CreatedAt.Equal(old.CreatedAt) {
				updated = append(updated, row)
			}
		}
		for start := 0; start < len(removed); start += 200 {
			if err := tx.Where("id IN ?", removed[start:min(start+200, len(removed))]).Delete(&models.Reaction{}).Error; err != nil {
				return err
			}
		}
		var newReactions []models.Reaction
		for _, row := range current {
			if row.ID == 0 {
				newReactions = append(newReactions, row)
			}
		}
		if len(newReactions) > 0 {
			if err := tx.CreateInBatches(&newReactions, 100).Error; err != nil {
				return err
			}
		}
		if len(newReceipts) > 0 {
			if err := tx.CreateInBatches(&newReceipts, 100).Error; err != nil {
				return err
			}
		}
		if len(updated) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "id"}},
				DoUpdates: clause.AssignmentColumns([]string{"created_at", "updated_at"}),
			}).CreateInBatches(&updated, 100).Error; err != nil {
				return err
			}
		}
		var newActivities []models.ReactionActivity
		for _, row := range activities {
			if row.ID == 0 {
				newActivities = append(newActivities, row)
			}
		}
		var inactive []uint
		for id := range deactivated {
			inactive = append(inactive, id)
		}
		for start := 0; start < len(inactive); start += 200 {
			if err := tx.Model(&models.ReactionActivity{}).Where("id IN ?", inactive[start:min(start+200, len(inactive))]).Update("active", false).Error; err != nil {
				return err
			}
		}
		if len(newActivities) > 0 {
			return tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&newActivities, 100).Error
		}
		return nil
	})
}
