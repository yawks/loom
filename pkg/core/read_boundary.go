package core

import (
	"strings"
	"time"

	"Loom/pkg/models"
)

// SplitRecoveredMessagesByOwnActivity classifies a chronological history batch
// using the last message sent, or reaction added, by the current user. Messages
// through that activity are read; later messages are unread. With no own
// activity in the recovered window, the entire batch is after the last known
// boundary and remains unread.
func SplitRecoveredMessagesByOwnActivity(messages []models.Message, currentUserID string) (read, unread []models.Message) {
	return SplitRecoveredMessagesAtOwnActivity(messages, currentUserID, time.Time{})
}

// SplitRecoveredMessagesAtOwnActivity also considers activity already persisted
// outside the recovered batch. This is required when an outgoing message or a
// reaction was stored before Loom discovers older incoming messages.
func SplitRecoveredMessagesAtOwnActivity(messages []models.Message, currentUserID string, activityAt time.Time) (read, unread []models.Message) {
	for _, message := range messages {
		if message.IsFromMe || sameProviderUser(message.SenderID, currentUserID) {
			if message.Timestamp.After(activityAt) {
				activityAt = message.Timestamp
			}
		}
		for _, reaction := range message.Reactions {
			if sameProviderUser(reaction.UserID, currentUserID) {
				reactedAt := reaction.CreatedAt
				if reactedAt.IsZero() {
					reactedAt = message.Timestamp
				}
				if reactedAt.After(activityAt) {
					activityAt = reactedAt
				}
				break
			}
		}
	}
	if activityAt.IsZero() {
		return nil, messages
	}
	boundary := -1
	for index, message := range messages {
		if !message.Timestamp.After(activityAt) {
			boundary = index
		}
	}
	if boundary < 0 {
		return nil, messages
	}
	return messages[:boundary+1], messages[boundary+1:]
}

func sameProviderUser(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	return left != "" && right != "" && strings.EqualFold(left, right)
}
