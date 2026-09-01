package db

import (
	"sort"
	"strings"
	"time"

	"Loom/pkg/models"
)

// LatestOwnActivityAt returns the latest persisted message or reaction from the
// current user in one conversation. Providers use this as a read-through
// boundary when a catch-up batch does not itself contain the user's activity.
func LatestOwnActivityAt(conversationID, currentUserID string) time.Time {
	if DB == nil || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(currentUserID) == "" {
		return time.Time{}
	}

	var latestMessage models.Message
	var activityAt time.Time
	// Find with an explicit limit treats "no own activity" as the normal empty
	// result it is, instead of making GORM log a misleading record-not-found.
	if err := DB.Where(
		"protocol_conv_id = ? AND (is_from_me = ? OR lower(sender_id) = lower(?))",
		conversationID, true, currentUserID,
	).Order("timestamp DESC").Limit(1).Find(&latestMessage).Error; err == nil && latestMessage.ID != 0 {
		activityAt = latestMessage.Timestamp
	}

	type reactionActivity struct {
		CreatedAt        time.Time
		MessageTimestamp time.Time
	}
	var reactions []reactionActivity
	DB.Table("reactions").
		Select("reactions.created_at, messages.timestamp AS message_timestamp").
		Joins("JOIN messages ON messages.id = reactions.message_id").
		Where("messages.protocol_conv_id = ? AND messages.deleted_at IS NULL AND lower(reactions.user_id) = lower(?)", conversationID, currentUserID).
		Find(&reactions)
	for _, reaction := range reactions {
		reactedAt := reaction.CreatedAt
		if reactedAt.IsZero() {
			reactedAt = reaction.MessageTimestamp
		}
		if reactedAt.After(activityAt) {
			activityAt = reactedAt
		}
	}
	return activityAt
}

// MessagesReadThrough returns a bounded chronological slice through the given
// local read boundary. It is used to repair stale renderer-owned unread flags
// without sending receipts back to the remote service.
func MessagesReadThrough(conversationID string, activityAt time.Time, limit int) []models.Message {
	if DB == nil || conversationID == "" || activityAt.IsZero() {
		return nil
	}
	if limit <= 0 {
		limit = 1000
	}
	var messages []models.Message
	if err := DB.Where("protocol_conv_id = ? AND timestamp <= ?", conversationID, activityAt).
		Order("timestamp DESC").Limit(limit).Find(&messages).Error; err != nil {
		return nil
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i].Timestamp.Before(messages[j].Timestamp) })
	return messages
}
