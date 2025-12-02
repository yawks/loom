package providers

import (
	"Loom/pkg/models"
	"fmt"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
)

// convertHistoryReactions converts WhatsApp reaction data from history sync to our Reaction model.
func (w *WhatsAppProvider) convertHistoryReactions(reactions []*waProto.Reaction, messageID string) []models.Reaction {
	if len(reactions) == 0 {
		return nil
	}

	converted := make([]models.Reaction, 0, len(reactions))
	for _, reaction := range reactions {
		if reaction == nil {
			continue
		}

		// Get the reaction key to identify the sender
		key := reaction.GetKey()
		if key == nil {
			fmt.Printf("WhatsApp: Reaction has no key, skipping\n")
			continue
		}

		// Extract sender information
		senderJID := key.GetFromMe()
		var userID string
		if senderJID {
			// Reaction from current user
			if w.client != nil && w.client.Store != nil && w.client.Store.ID != nil {
				userID = w.client.Store.ID.String()
			}
		} else {
			// Reaction from another user - get participant ID
			participantJID := key.GetParticipant()
			if participantJID != "" {
				userID = participantJID
			} else {
				// For 1-on-1 chats, the sender might be in the RemoteJID
				remoteJID := key.GetRemoteJID()
				if remoteJID != "" {
					userID = remoteJID
				}
			}
		}

		if userID == "" {
			fmt.Printf("WhatsApp: Could not determine user ID for reaction, skipping\n")
			continue
		}

		// Get emoji text
		emoji := reaction.GetText()

		// Empty emoji means the reaction was removed - skip it
		if emoji == "" {
			fmt.Printf("WhatsApp: Reaction with empty emoji (removed), skipping\n")
			continue
		}

		// Get timestamp
		timestampMS := reaction.GetSenderTimestampMS()
		timestamp := time.Unix(0, timestampMS*int64(time.Millisecond))

		fmt.Printf("WhatsApp: Converting history reaction: user=%s, emoji=%s, timestamp=%v\n", userID, emoji, timestamp)

		converted = append(converted, models.Reaction{
			UserID:    userID,
			Emoji:     emoji,
			CreatedAt: timestamp,
			UpdatedAt: timestamp,
		})
	}

	return converted
}

// convertMessageStatus converts WhatsApp message status from history sync to MessageReceipt models.
// Status values: ERROR=0, PENDING=1, SERVER_ACK=2, DELIVERY_ACK=3, READ=4, PLAYED=5
func (w *WhatsAppProvider) convertMessageStatus(status waProto.WebMessageInfo_Status, messageID string, conversationID string, messageTimestamp time.Time) []models.MessageReceipt {
	// Only create receipts for delivered or read status
	// For 1-on-1 chats, we create a receipt for the other participant
	// For group chats, we would need participant info which isn't available in status alone

	if status != waProto.WebMessageInfo_DELIVERY_ACK && status != waProto.WebMessageInfo_READ && status != waProto.WebMessageInfo_PLAYED {
		// Status is ERROR, PENDING, or SERVER_ACK - no receipts to create
		return nil
	}

	// Determine receipt type
	var receiptType string
	if status == waProto.WebMessageInfo_READ || status == waProto.WebMessageInfo_PLAYED {
		receiptType = "read"
	} else if status == waProto.WebMessageInfo_DELIVERY_ACK {
		receiptType = "delivery"
	}

	// For 1-on-1 chats, extract the other participant's ID from conversation ID
	// For group chats, we can't determine individual participants from status alone
	// so we skip creating receipts (they would need to come from individual receipt events)

	// Check if this is a group chat (ends with @g.us)
	if len(conversationID) > 5 && conversationID[len(conversationID)-5:] == "@g.us" {
		// Group chat - we can't determine individual participants from status alone
		// Skip creating receipts for groups from history sync status
		fmt.Printf("WhatsApp: Skipping receipt creation for group chat %s (status: %v)\n", conversationID, status)
		return nil
	}

	// 1-on-1 chat - create a receipt for the other participant
	userID := conversationID

	// Use message timestamp as receipt timestamp (we don't have exact receipt time from status)
	receipt := models.MessageReceipt{
		UserID:      userID,
		ReceiptType: receiptType,
		Timestamp:   messageTimestamp,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	fmt.Printf("WhatsApp: Created %s receipt for message %s from status %v\n", receiptType, messageID, status)

	return []models.MessageReceipt{receipt}
}

// inferGroupReceipts infers delivery/read receipts for group messages based on participant activity.
// Logic: If at least 1 participant replied/reacted → all get "delivered"
//
//	If ALL participants replied/reacted → all get "read"
func (w *WhatsAppProvider) inferGroupReceipts(messages []models.Message, conversationID string) {
	fmt.Printf("WhatsApp: inferGroupReceipts called for %s with %d messages\n", conversationID, len(messages))

	if len(messages) == 0 {
		return
	}

	// Only process group chats
	if len(conversationID) < 5 || conversationID[len(conversationID)-5:] != "@g.us" {
		fmt.Printf("WhatsApp: Skipping inference - not a group chat: %s\n", conversationID)
		return
	}

	// Get current user ID
	var currentUserID string
	if w.client != nil && w.client.Store != nil && w.client.Store.ID != nil {
		currentUserID = w.client.Store.ID.String()
	}
	if currentUserID == "" {
		return
	}

	// Build a map of participant activity (latest timestamp of reply or reaction)
	participantActivity := make(map[string]time.Time)

	// Collect all participants who have sent messages or reactions
	for _, msg := range messages {
		if msg.SenderID == currentUserID {
			continue // Skip current user's messages
		}

		// Track message timestamp as activity
		if existing, ok := participantActivity[msg.SenderID]; !ok || msg.Timestamp.After(existing) {
			participantActivity[msg.SenderID] = msg.Timestamp
		}

		// Track reactions as activity
		for _, reaction := range msg.Reactions {
			if reaction.UserID == currentUserID {
				continue // Skip current user's reactions
			}
			if existing, ok := participantActivity[reaction.UserID]; !ok || reaction.CreatedAt.After(existing) {
				participantActivity[reaction.UserID] = reaction.CreatedAt
			}
		}
	}

	if len(participantActivity) == 0 {
		fmt.Printf("WhatsApp: No participant activity found for group %s\n", conversationID)
		return
	}

	fmt.Printf("WhatsApp: Found %d active participants in group %s\n", len(participantActivity), conversationID)

	// Process each message sent by current user
	for i := range messages {
		msg := &messages[i]

		// Only process messages from current user
		if !msg.IsFromMe {
			continue
		}

		// Skip if message already has receipts from actual events
		if len(msg.Receipts) > 0 {
			continue
		}

		// Count participants with activity after this message
		participantsWithActivity := 0
		for _, activityTime := range participantActivity {
			if activityTime.After(msg.Timestamp) {
				participantsWithActivity++
			}
		}

		if participantsWithActivity == 0 {
			continue // No activity after this message
		}

		// Determine receipt type
		var receiptType string
		if participantsWithActivity == len(participantActivity) {
			// ALL participants have activity → "read"
			receiptType = "read"
		} else {
			// At least 1 participant has activity → "delivered"
			receiptType = "delivery"
		}

		// Create receipts for all participants
		receipts := make([]models.MessageReceipt, 0, len(participantActivity))
		for userID := range participantActivity {
			receipts = append(receipts, models.MessageReceipt{
				UserID:      userID,
				ReceiptType: receiptType,
				Timestamp:   msg.Timestamp, // Use message timestamp as we don't have exact receipt time
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			})
		}

		msg.Receipts = receipts
		fmt.Printf("WhatsApp: Inferred %d %s receipts for group message %s\n", len(receipts), receiptType, msg.ProtocolMsgID)
	}
}
