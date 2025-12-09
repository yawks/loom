package whatsapp

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"fmt"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
)

func (w *WhatsAppProvider) loadLastSyncTimestamp() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadLastSyncTimestampLocked()
}

func (w *WhatsAppProvider) loadLastSyncTimestampLocked() {
	if db.DB == nil {
		return
	}

	var config models.ProviderConfiguration
	err := db.DB.Where("provider_id = ?", "whatsapp").First(&config).Error
	if err == nil && config.LastSyncAt != nil {
		w.lastSyncTimestamp = config.LastSyncAt
		w.log("WhatsApp: Loaded last sync timestamp: %s\n", config.LastSyncAt.Format("2006-01-02 15:04:05"))
	} else {
		w.log("WhatsApp: No previous sync timestamp found (first sync)\n")
	}
}

func (w *WhatsAppProvider) saveLastSyncTimestamp(timestamp time.Time) {
	if db.DB == nil {
		return
	}

	w.mu.Lock()
	w.lastSyncTimestamp = &timestamp
	w.mu.Unlock()

	var config models.ProviderConfiguration
	err := db.DB.Where("provider_id = ?", "whatsapp").First(&config).Error
	if err == nil {
		// Update existing
		config.LastSyncAt = &timestamp
		config.UpdatedAt = time.Now()
		if err := db.DB.Save(&config).Error; err != nil {
			w.log("WhatsApp: Failed to save last sync timestamp: %v\n", err)
		} else {
			w.log("WhatsApp: Saved last sync timestamp: %s\n", timestamp.Format("2006-01-02 15:04:05"))
		}
	} else {
		// Create new
		config = models.ProviderConfiguration{
			ProviderID: "whatsapp",
			IsActive:   true,
			LastSyncAt: &timestamp,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		if err := db.DB.Create(&config).Error; err != nil {
			w.log("WhatsApp: Failed to create provider configuration: %v\n", err)
		} else {
			w.log("WhatsApp: Created provider configuration with last sync timestamp: %s\n", timestamp.Format("2006-01-02 15:04:05"))
		}
	}
}

func (w *WhatsAppProvider) SyncHistory(since time.Time) error {
	w.mu.RLock()
	client := w.client
	w.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("client not initialized")
	}

	// Check if client is connected (Store.ID is set after successful login)
	w.mu.RLock()
	store := client.Store
	w.mu.RUnlock()

	if store == nil || store.ID == nil {
		// Client is not connected yet, return without error
		w.log("WhatsApp: SyncHistory called but client not connected yet, skipping...\n")
		return nil
	}

	// Use last sync timestamp if since is zero or very old
	w.mu.RLock()
	lastSync := w.lastSyncTimestamp
	w.mu.RUnlock()

	if since.IsZero() || since.Before(time.Now().Add(-24*time.Hour)) {
		if lastSync != nil {
			since = *lastSync
			w.log("WhatsApp: Using last sync timestamp: %s\n", since.Format("2006-01-02 15:04:05"))
		} else {
			// First sync - sync from 30 days ago
			since = time.Now().Add(-30 * 24 * time.Hour)
			w.log("WhatsApp: First sync - syncing from 30 days ago: %s\n", since.Format("2006-01-02 15:04:05"))
		}
	}

	// Emit sync status event
	w.emitSyncStatus(core.SyncStatusFetchingHistory, fmt.Sprintf("Syncing history since %s...", since.Format("2006-01-02 15:04:05")), -1)

	w.log("WhatsApp: SyncHistory called for messages since %s\n", since.Format("2006-01-02 15:04:05"))

	// WhatsApp automatically syncs history when connected via HistorySync events
	// We need to force a refresh of contacts to get the latest conversations
	// The actual message history sync happens automatically through whatsmeow's event system

	// Trigger a refresh of contacts in a goroutine to avoid blocking
	go func() {
		// Wait a bit for whatsmeow to process any pending sync events
		time.Sleep(2 * time.Second)

		w.emitSyncStatus(core.SyncStatusFetchingContacts, "Refreshing conversations...", 90)

		contacts, err := w.GetContacts()
		if err != nil {
			w.log("WhatsApp: Failed to refresh contacts during sync: %v\n", err)
			w.emitSyncStatus(core.SyncStatusError, fmt.Sprintf("Failed to refresh conversations: %v", err), -1)
			return
		}

		w.log("WhatsApp: Refreshed %d conversations during sync\n", len(contacts))

		// Emit contact refresh event
		select {
		case w.eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "sync_complete"}:
		default:
		}

		// Emit completed status - this is the final event for manual sync
		w.log("WhatsApp: Emitting completed sync status for manual sync with %d conversations\n", len(contacts))
		w.emitSyncStatus(core.SyncStatusCompleted, fmt.Sprintf("Sync completed - %d conversations available", len(contacts)), 100)
		w.log("WhatsApp: Completed sync status emitted for manual sync\n")
	}()

	return nil
}

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
			w.log("WhatsApp: Reaction has no key, skipping\n")
			continue
		}

		// Extract sender information
		senderJID := key.GetFromMe()
		var userID string
		if senderJID {
			// Reaction from current user
			w.mu.RLock()
			if w.client != nil && w.client.Store != nil && w.client.Store.ID != nil {
				userID = w.client.Store.ID.String()
			}
			w.mu.RUnlock()
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
			w.log("WhatsApp: Could not determine user ID for reaction, skipping\n")
			continue
		}

		// Get emoji text
		emoji := reaction.GetText()

		// Empty emoji means the reaction was removed - skip it
		if emoji == "" {
			w.log("WhatsApp: Reaction with empty emoji (removed), skipping\n")
			continue
		}

		// Get timestamp
		timestampMS := reaction.GetSenderTimestampMS()
		timestamp := time.Unix(0, timestampMS*int64(time.Millisecond))

		w.log("WhatsApp: Converting history reaction: user=%s, emoji=%s, timestamp=%v\n", userID, emoji, timestamp)

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
		w.log("WhatsApp: Skipping receipt creation for group chat %s (status: %v)\n", conversationID, status)
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

	w.log("WhatsApp: Created %s receipt for message %s from status %v\n", receiptType, messageID, status)

	return []models.MessageReceipt{receipt}
}
