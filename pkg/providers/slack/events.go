// Package slack provides event handling for the Slack provider.
package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"fmt"
	"time"
)

// StreamEvents returns a channel for receiving real-time events.
func (p *SlackProvider) StreamEvents() (<-chan core.ProviderEvent, error) {
	// Return the event channel that is populated by polling goroutine
	if p.eventChan == nil {
		p.eventChan = make(chan core.ProviderEvent, 100)
	}
	return p.eventChan, nil
}

// emitSyncStatus emits a synchronization status event.
func (p *SlackProvider) emitSyncStatus(status core.SyncStatusType, message string, progress int) {
	// Use recover to prevent panic if channel is closed
	defer func() {
		if r := recover(); r != nil {
			p.log("SlackProvider: PANIC in emitSyncStatus (channel may be closed): %v, status=%s, message=%s\n", r, status, message)
		}
	}()

	if p.eventChan == nil {
		p.log("SlackProvider: Warning - eventChan is nil, cannot emit sync status: %s\n", message)
		return
	}

	// Log the event being emitted for debugging
	p.log("SlackProvider: Emitting sync status: status=%s, message=%s, progress=%d\n", status, message, progress)

	// Use a timeout to ensure important events (like "completed") are not lost
	// For "completed" and "error" status, we use a longer timeout to ensure delivery
	timeout := 100 * time.Millisecond
	if status == core.SyncStatusCompleted || status == core.SyncStatusError {
		timeout = 1 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	select {
	case p.eventChan <- core.SyncStatusEvent{
		Status:   status,
		Message:  message,
		Progress: progress,
	}:
		// Event sent successfully
		fmt.Printf("SlackProvider.emitSyncStatus: Event sent successfully: status=%s, message=%s\n", status, message)
		p.log("SlackProvider: Sync status event sent successfully: %s\n", message)
	case <-ctx.Done():
		// Timeout - log but don't fail
		fmt.Printf("SlackProvider.emitSyncStatus: Timeout emitting sync status: status=%s, message=%s\n", status, message)
		p.log("SlackProvider: Timeout emitting sync status: status=%s, message=%s\n", status, message)
	}
}

// SyncHistory retrieves message history since a certain date for all conversations.
// It fetches messages from Slack API and stores them in the database.
// New messages (after the last sync) will be marked as unread by the frontend.
func (p *SlackProvider) SyncHistory(since time.Time) error {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	p.log("SlackProvider.SyncHistory: Starting sync for messages since %s\n", since.Format("2006-01-02 15:04:05"))
	fmt.Printf("SlackProvider.SyncHistory: Starting sync for messages since %s\n", since.Format("2006-01-02 15:04:05"))

	// Emit sync status: fetching contacts
	fmt.Printf("SlackProvider.SyncHistory: Emitting fetching_contacts status\n")
	p.emitSyncStatus(core.SyncStatusFetchingContacts, "Fetching conversations...", -1)

	// Get all conversations (users and channels)
	contacts, err := p.GetContacts()
	if err != nil {
		p.emitSyncStatus(core.SyncStatusError, fmt.Sprintf("Failed to get contacts: %v", err), -1)
		return fmt.Errorf("failed to get contacts: %w", err)
	}

	p.log("SlackProvider.SyncHistory: Found %d conversations to sync\n", len(contacts))

	// Emit sync status: fetching history
	fmt.Printf("SlackProvider.SyncHistory: Emitting fetching_history status for %d conversations\n", len(contacts))
	p.emitSyncStatus(core.SyncStatusFetchingHistory, fmt.Sprintf("Syncing message history for %d conversations...", len(contacts)), -1)

	totalSynced := 0
	// Sync messages for each conversation
	for idx, contact := range contacts {
		conversationID := contact.UserID

		// Check if we already have messages in DB for this conversation since 'since'
		var existingCount int64
		if db.DB != nil {
			db.DB.Model(&models.Message{}).
				Where("protocol_conv_id = ? AND timestamp >= ?", conversationID, since).
				Count(&existingCount)
		}

		// If we already have recent messages, skip (they're already in DB)
		if existingCount > 0 {
			p.log("SlackProvider.SyncHistory: Conversation %s already has %d messages since %s, skipping\n",
				conversationID, existingCount, since.Format("2006-01-02 15:04:05"))
			// Update progress even for skipped conversations
			if (idx+1)%10 == 0 || idx == len(contacts)-1 {
				progress := -1
				if len(contacts) > 0 {
					progress = int((float64(idx+1) / float64(len(contacts))) * 100)
				}
				p.emitSyncStatus(core.SyncStatusFetchingHistory, fmt.Sprintf("Syncing messages (%d/%d conversations)...", idx+1, len(contacts)), progress)
			}
			continue
		}

		// Fetch messages from Slack API since 'since'
		// Use a reasonable limit (100 messages per conversation)
		messages, err := p.GetConversationHistory(conversationID, 100, &since)
		if err != nil {
			p.log("SlackProvider.SyncHistory: WARNING - failed to get history for conversation %s: %v\n", conversationID, err)
			// Update progress even on error
			if (idx+1)%10 == 0 || idx == len(contacts)-1 {
				progress := -1
				if len(contacts) > 0 {
					progress = int((float64(idx+1) / float64(len(contacts))) * 100)
				}
				p.emitSyncStatus(core.SyncStatusFetchingHistory, fmt.Sprintf("Syncing messages (%d/%d conversations)...", idx+1, len(contacts)), progress)
			}
			continue
		}

		if len(messages) > 0 {
			p.log("SlackProvider.SyncHistory: Fetched %d messages for conversation %s\n", len(messages), conversationID)
			// Messages are already stored in DB by GetConversationHistory via storeMessagesForConversation
			totalSynced++
		}

		// Update progress periodically (every 10 conversations or at the end)
		if (idx+1)%10 == 0 || idx == len(contacts)-1 {
			progress := -1
			if len(contacts) > 0 {
				progress = int((float64(idx+1) / float64(len(contacts))) * 100)
			}
			p.emitSyncStatus(core.SyncStatusFetchingHistory, fmt.Sprintf("Syncing messages (%d/%d conversations)...", idx+1, len(contacts)), progress)
		}
	}

	// Emit sync status: completed
	fmt.Printf("SlackProvider.SyncHistory: Emitting completed status (%d conversations synced)\n", totalSynced)
	p.emitSyncStatus(core.SyncStatusCompleted, fmt.Sprintf("Sync completed - %d conversations synced", totalSynced), 100)

	p.log("SlackProvider.SyncHistory: Sync completed\n")
	fmt.Printf("SlackProvider.SyncHistory: Sync completed\n")
	return nil
}

// SendStatusMessage sends a status message (broadcast).
func (p *SlackProvider) SendStatusMessage(text string, file *core.Attachment) (*models.Message, error) {
	// Slack status is user status, not a broadcast message usually.
	return nil, nil
}
