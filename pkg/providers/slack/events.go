// Package slack provides event handling for the Slack provider.
package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/slack-go/slack"
)

// StreamEvents returns a channel that emits provider events (messages, reactions, etc.).
// This uses polling to periodically check for new messages since RTM is deprecated.
func (p *SlackProvider) StreamEvents() (<-chan core.ProviderEvent, error) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	// Start polling if not already started
	p.mu.Lock()
	if !p.eventStreamStarted {
		ctx, cancel := context.WithCancel(context.Background())
		p.eventStreamCtx = ctx
		p.eventStreamCancel = cancel
		p.eventStreamStarted = true
		go p.startPolling(ctx)
		p.mu.Unlock()
	} else {
		p.mu.Unlock()
	}

	// Return the existing event channel
	return p.eventChan, nil
}

// startPolling starts polling for new messages, reactions, and files.
// This polls all conversations periodically to check for new content.
func (p *SlackProvider) startPolling(ctx context.Context) {
	p.log("SlackProvider.startPolling: Starting message polling with Search API (5s interval)\n")
	fmt.Printf("SlackProvider.startPolling: Starting Slack message polling\n")

	// Poll every 5 seconds for messages (Tier 2 rate limit allows 20 req/min, 5s = 12 req/min)
	messageTicker := time.NewTicker(5 * time.Second)
	defer messageTicker.Stop()

	// Poll every 2 minutes for reactions on recent messages (less frequent)
	reactionTicker := time.NewTicker(2 * time.Minute)
	defer reactionTicker.Stop()

	// Track last poll timestamp
	lastPollTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			p.log("SlackProvider.startPolling: Context cancelled, stopping polling\n")
			return
		case <-p.stopChan:
			p.log("SlackProvider.startPolling: Stop signal received, stopping polling\n")
			return
		case <-messageTicker.C:
			// Poll for all new messages using Search API
			newLastPollTime, err := p.pollGlobalUpdates(ctx, lastPollTime)
			if err != nil {
				p.log("SlackProvider.startPolling: Error polling global updates: %v\n", err)
			} else if !newLastPollTime.IsZero() {
				lastPollTime = newLastPollTime
			}
		case <-reactionTicker.C:
			// Poll for new reactions on recent messages
			p.pollNewReactions()
		}
	}
}

// pollGlobalUpdates searches for all messages received since the last poll time.
// It returns the timestamp of the latest message found (or the input time if none/error).
func (p *SlackProvider) pollGlobalUpdates(ctx context.Context, since time.Time) (time.Time, error) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return since, nil
	}

	// Query: "after:<unix_timestamp>"
	query := fmt.Sprintf("after:%d", since.Unix())

	// Search parameters
	params := slack.NewSearchParameters()
	params.Sort = "timestamp"
	params.SortDirection = "asc"
	params.Count = 100 // Get up to 100 recent matching messages

	search, err := client.SearchMessages(query, params)
	if err != nil {
		return since, err
	}

	if len(search.Matches) == 0 {
		return since, nil
	}

	p.log("SlackProvider.pollGlobalUpdates: Found %d new messages via search (since %s)\n", len(search.Matches), since.Format(time.RFC3339))

	latestTimestamp := since
	newConversationsFound := false

	// Process matches
	for _, match := range search.Matches {
		// Parse timestamp
		tsFloat, err := strconv.ParseFloat(match.Timestamp, 64)
		if err != nil {
			continue
		}
		secs := int64(tsFloat)
		nsecs := int64((tsFloat - float64(secs)) * 1e9)
		ts := time.Unix(secs, nsecs)

		// Update latest timestamp seen
		if ts.After(latestTimestamp) {
			latestTimestamp = ts
		}

		// Skip messages older than or equal to 'since' (overlap safety)
		if !ts.After(since) {
			continue
		}

		conversationID := match.Channel.ID
		if conversationID == "" {
			continue
		}

		// Check if we know this conversation (users or channels)
		// We can check if it exists in DB, or just assume if we don't have it locally we might need to refresh
		if db.DB != nil {
			var count int64
			db.DB.Model(&models.LinkedAccount{}).Where("user_id = ?", conversationID).Count(&count)
			if count == 0 {
				newConversationsFound = true
				p.log("SlackProvider.pollGlobalUpdates: Discovering new conversation: %s\n", conversationID)
			}
		}

		// Convert to Model Message
		msg := models.Message{
			ProtocolMsgID:  match.Timestamp, // Match TS is unique ID
			ProtocolConvID: conversationID,
			Body:           match.Text,
			SenderID:       match.User,
			SenderName:     match.Username, // Search returns username
			Timestamp:      ts,
			IsFromMe:       false, // We'll double check below
		}

		// Refine Sender Name/Avatar using cache
		if msg.SenderID != "" {
			p.userCacheMu.RLock()
			user, ok := p.userCache[msg.SenderID]
			p.userCacheMu.RUnlock()
			if ok {
				if user.RealName != "" {
					msg.SenderName = user.RealName
				}
				if user.Profile.Image48 != "" {
					msg.SenderAvatarURL = user.Profile.Image48
				}
			}
		}

		// Check IsFromMe
		authTest, err := client.AuthTest()
		if err == nil && authTest.UserID == msg.SenderID {
			msg.IsFromMe = true
		}

		// Deduplicate: Check if message exists in DB
		if db.DB != nil {
			var exists int64
			db.DB.Model(&models.Message{}).Where("protocol_msg_id = ?", msg.ProtocolMsgID).Count(&exists)
			if exists > 0 {
				continue // Already have it
			}

			// Store new message
			if err := db.DB.Create(&msg).Error; err != nil {
				p.log("SlackProvider.pollGlobalUpdates: Failed to save message %s: %v\n", msg.ProtocolMsgID, err)
			}
		}

		// Emit event
		select {
		case p.eventChan <- core.MessageEvent{Message: msg}:
		default:
			p.log("SlackProvider.pollGlobalUpdates: Event channel full, dropping message %s\n", msg.ProtocolMsgID)
		}
	}

	// If we found messages in new conversations, trigger a contact refresh
	if newConversationsFound {
		p.log("SlackProvider.pollGlobalUpdates: Triggering contact refresh due to new conversations\n")
		// Emit refresh event
		select {
		case p.eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "message_received"}:
		default:
		}
	}

	return latestTimestamp, nil
}

// pollNewReactions polls for new reactions on recent messages (last 1 hour)
// This is less frequent than message polling since reactions change less often
func (p *SlackProvider) pollNewReactions() {
	if db.DB == nil {
		return
	}

	// Check reactions on messages from last 1 hour (more recent = more likely to have new reactions)
	since := time.Now().Add(-1 * time.Hour)

	// Get recent messages with reactions from all conversations
	var recentMessages []models.Message
	if err := db.DB.Where("timestamp >= ?", since).
		Preload("Reactions").
		Order("timestamp DESC").
		Limit(50). // Check last 50 messages across all conversations
		Find(&recentMessages).Error; err != nil {
		return
	}

	// Group messages by conversation
	messagesByConv := make(map[string][]models.Message)
	for _, msg := range recentMessages {
		messagesByConv[msg.ProtocolConvID] = append(messagesByConv[msg.ProtocolConvID], msg)
	}

	// Check each conversation
	for conversationID, messages := range messagesByConv {
		// Fetch recent messages from API to get latest reactions
		apiMessages, err := p.GetConversationHistory(conversationID, len(messages), nil, &since)
		if err != nil {
			continue
		}

		// Create map of API messages by protocol message ID
		apiMessagesMap := make(map[string]models.Message)
		for _, apiMsg := range apiMessages {
			apiMessagesMap[apiMsg.ProtocolMsgID] = apiMsg
		}

		// For each message in DB, check if reactions have changed
		for _, dbMsg := range messages {
			apiMsg, exists := apiMessagesMap[dbMsg.ProtocolMsgID]
			if !exists {
				continue
			}

			// Compare reactions
			dbReactions := make(map[string]map[string]bool) // emoji -> userID -> true
			for _, r := range dbMsg.Reactions {
				if dbReactions[r.Emoji] == nil {
					dbReactions[r.Emoji] = make(map[string]bool)
				}
				dbReactions[r.Emoji][r.UserID] = true
			}

			// Check for new reactions
			apiReactions := make(map[string]map[string]bool) // emoji -> userID -> true
			for _, r := range apiMsg.Reactions {
				if apiReactions[r.Emoji] == nil {
					apiReactions[r.Emoji] = make(map[string]bool)
				}
				apiReactions[r.Emoji][r.UserID] = true
			}

			// Check for new reactions
			for emoji, users := range apiReactions {
				for userID := range users {
					if dbReactions[emoji] == nil || !dbReactions[emoji][userID] {
						// New reaction - emit event
						select {
						case p.eventChan <- core.ReactionEvent{
							ConversationID: conversationID,
							MessageID:      dbMsg.ProtocolMsgID,
							UserID:         userID,
							Emoji:          emoji,
							Added:          true,
						}:
							// Event sent
						default:
							// Channel full, skip
						}
					}
				}
			}

			// Check for removed reactions
			for emoji, users := range dbReactions {
				for userID := range users {
					if apiReactions[emoji] == nil || !apiReactions[emoji][userID] {
						// Reaction removed - emit event
						select {
						case p.eventChan <- core.ReactionEvent{
							ConversationID: conversationID,
							MessageID:      dbMsg.ProtocolMsgID,
							UserID:         userID,
							Emoji:          emoji,
							Added:          false,
						}:
							// Event sent
						default:
							// Channel full, skip
						}
					}
				}
			}
		}

		// Small delay between conversations
		time.Sleep(200 * time.Millisecond)
	}
}

// SyncHistory synchronizes contacts and missing messages from Slack.
// Uses Search API to efficiently catch up on all messages since the last sync.
func (p *SlackProvider) SyncHistory(since time.Time) error {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	p.log("SlackProvider.SyncHistory: Starting sync (since %s)\n", since.Format(time.RFC3339))
	fmt.Printf("SlackProvider.SyncHistory: Starting contacts sync\n")

	// Emit sync status: fetching contacts
	fmt.Printf("SlackProvider.SyncHistory: Emitting sync status\n")
	p.emitSyncStatus(core.SyncStatusFetchingHistory, "Syncing recent messages via Search...", 10)

	// Add a small delay to ensure footer is visible before starting the actual work
	time.Sleep(200 * time.Millisecond)

	// 1. Recover missing messages using Search API
	// This acts like a "catch-up" poll
	ctx := context.Background()
	p.log("SlackProvider.SyncHistory: Calling pollGlobalUpdates...\n")
	_, err := p.pollGlobalUpdates(ctx, since)
	p.log("SlackProvider.SyncHistory: pollGlobalUpdates returned (err=%v)\n", err)
	if err != nil {
		p.log("SlackProvider.SyncHistory: Warning: Search sync failed: %v. Continuing to contact sync.\n", err)
	} else {
		p.emitSyncStatus(core.SyncStatusFetchingHistory, "Recent messages synced.", 50)
		time.Sleep(500 * time.Millisecond)
	}

	// 2. Refresh Contacts (in case of new channels/users)
	p.emitSyncStatus(core.SyncStatusFetchingContacts, "Fetching conversations...", 60)

	// Get all conversations (users and channels) - only members
	contacts, err := p.GetContacts()
	if err != nil {
		p.emitSyncStatus(core.SyncStatusError, fmt.Sprintf("Failed to get contacts: %v", err), -1)
		return fmt.Errorf("failed to get contacts: %w", err)
	}

	p.log("SlackProvider.SyncHistory: Found %d conversations\n", len(contacts))

	// Get instance ID for database updates
	p.mu.RLock()
	instanceID := ""
	if p.config != nil {
		if id, ok := p.config["_instance_id"].(string); ok {
			instanceID = id
		}
	}
	p.mu.RUnlock()

	// Save contacts to database if DB is initialized and we have an instance ID
	if db.DB != nil && instanceID != "" {
		p.log("SlackProvider.SyncHistory: Saving %d contacts to database\n", len(contacts))
		for _, contact := range contacts {
			// Ensure ProviderInstanceID is set
			contact.ProviderInstanceID = instanceID

			// We use a custom upsert logic here to preserve existing data if needed,
			// or just overwrite with latest from Slack (which is safe as GetContacts is authoritative)
			// However, we must be careful not to overwrite custom aliases if we supported them via a different table (ContactAlias).
			// LinkedAccount is pure provider data, so we can overwrite.

			var existing models.LinkedAccount
			// We check for existence, ignoring "record not found" error as it's expected for new contacts
			// Also ignore error if not found, we'll handle create case
			err := db.DB.Where("provider_instance_id = ? AND user_id = ?", instanceID, contact.UserID).First(&existing).Error

			if err == nil {
				// Update existing
				contact.ID = existing.ID                       // Keep the DB ID
				contact.MetaContactID = existing.MetaContactID // Keep association
				contact.CreatedAt = existing.CreatedAt
				contact.UpdatedAt = time.Now()
				db.DB.Save(&contact)
			} else {
				// Record does not exist, create new MetaContact and LinkedAccount

				// 1. Create MetaContact
				metaContact := models.MetaContact{
					DisplayName: contact.Username,
					AvatarURL:   contact.AvatarURL,
					CreatedAt:   time.Now(),
					UpdatedAt:   time.Now(),
				}
				if err := db.DB.Create(&metaContact).Error; err != nil {
					p.log("SlackProvider.SyncHistory: Failed to create MetaContact for %s: %v\n", contact.Username, err)
					continue
				}

				// 2. Create LinkedAccount linked to MetaContact
				contact.MetaContactID = metaContact.ID
				contact.CreatedAt = time.Now()
				contact.UpdatedAt = time.Now()
				db.DB.Create(&contact)
			}
		}
		p.log("SlackProvider.SyncHistory: Contacts saved successfully\n")
	} else {
		p.log("SlackProvider.SyncHistory: WARNING - Skipping contact save (DB=%v, InstanceID=%s)\n", db.DB != nil, instanceID)
	}

	// Emit contact refresh event immediately to save contacts to database
	// This ensures contacts are available right after sync
	select {
	case p.eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "sync_complete"}:
		p.log("SlackProvider.SyncHistory: Emitted contacts-refresh event\n")
	default:
		p.log("SlackProvider.SyncHistory: Failed to emit contacts-refresh event (channel full)\n")
	}

	// Fetch and emit last_read timestamp for each conversation
	// OPTIMIZATION: Use metadata from Extra field instead of calling API for each contact
	p.log("SlackProvider.SyncHistory: Emitting last_read timestamps from cached metadata\n")
	for _, contact := range contacts {
		conversationID := contact.UserID

		// Parse Extra field to get last_read
		if contact.Extra == "" {
			continue
		}

		var extra map[string]interface{}
		if err := json.Unmarshal([]byte(contact.Extra), &extra); err != nil {
			continue
		}

		if lastRead, ok := extra["last_read"].(string); ok && lastRead != "" {
			select {
			case p.eventChan <- core.ConversationReadStatusEvent{
				ConversationID: conversationID,
				LastReadTS:     lastRead,
			}:
			default:
			}
		}
	}

	// Wait for MPIM processing to complete (with timeout)
	// Check if there are MPIMs being processed
	p.mpimCountMu.RLock()
	mpimCount := p.mpimCount
	hasMPIMs := mpimCount > 0
	p.mpimCountMu.RUnlock()

	if hasMPIMs {
		p.log("SlackProvider.SyncHistory: Waiting for %d MPIMs to be processed\n", mpimCount)
		// Emit status that we're processing MPIMs
		p.emitSyncStatus(core.SyncStatusFetchingContacts, fmt.Sprintf("Processing %d group conversations...", mpimCount), 80)

		// Wait for MPIM processing to complete, with a timeout
		// Estimate: 1.2 seconds per MPIM, but cap at 60 seconds total
		timeout := time.Duration(mpimCount) * 1200 * time.Millisecond
		if timeout > 60*time.Second {
			timeout = 60 * time.Second
		}

		select {
		case <-p.mpimProcessingChan:
			p.log("SlackProvider.SyncHistory: All MPIMs processed\n")
		case <-time.After(timeout):
			p.log("SlackProvider.SyncHistory: Timeout waiting for MPIMs, continuing anyway\n")
		}
	}

	// Add a small delay to ensure footer is visible
	time.Sleep(500 * time.Millisecond)

	// Emit completed status - no history sync needed
	fmt.Printf("SlackProvider.SyncHistory: Emitting completed status (%d conversations)\n", len(contacts))
	if len(contacts) == 0 {
		p.emitSyncStatus(core.SyncStatusCompleted, "Sync completed - no conversations", 100)
		return nil
	}
	p.emitSyncStatus(core.SyncStatusCompleted, fmt.Sprintf("Sync completed - %d conversations available", len(contacts)), 100)

	p.log("SlackProvider.SyncHistory: Sync completed\n")
	fmt.Printf("SlackProvider.SyncHistory: Sync completed\n")
	return nil
}

// emitSyncStatus emits a sync status event to the frontend
func (p *SlackProvider) emitSyncStatus(status core.SyncStatusType, message string, progress int) {
	p.log("SlackProvider: Emitting sync status: status=%s, message=%s, progress=%d\n", status, message, progress)
	select {
	case p.eventChan <- core.SyncStatusEvent{
		Status:   status,
		Message:  message,
		Progress: progress,
	}:
		p.log("SlackProvider.emitSyncStatus: Event sent successfully: status=%s, message=%s\n", status, message)
		p.log("SlackProvider: Sync status event sent successfully: %s\n", message)
	default:
		p.log("SlackProvider.emitSyncStatus: WARNING - Failed to emit sync status (channel full): status=%s, message=%s\n", status, message)
	}
}

// SendStatusMessage sends a status message (broadcast).
func (p *SlackProvider) SendStatusMessage(text string, file *core.Attachment) (*models.Message, error) {
	// Slack status is user status, not a broadcast message usually.
	return nil, nil
}
