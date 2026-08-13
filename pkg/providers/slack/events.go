// Package slack provides event handling for the Slack provider.
package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/slack-go/slack"
	"gorm.io/gorm"
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

	// conversations.list exposes the latest message of each joined conversation.
	// Use it as a low-cost fallback for huddle starts when search.messages is not
	// available to the current user token (notably when search:read is missing).
	huddleStartTicker := time.NewTicker(10 * time.Second)
	defer huddleStartTicker.Stop()

	// Poll every 2 minutes for reactions on recent messages (less frequent)
	reactionTicker := time.NewTicker(2 * time.Minute)
	defer reactionTicker.Stop()

	// Track last poll timestamp
	lastPollTime := time.Now()
	// Slack search does not reliably return messages from the special DM with
	// yourself. Keep an independent cursor for that conversation so messages
	// sent from another Slack client still reach Loom without a manual sync.
	lastSelfDMPollTime := lastPollTime.Add(-10 * time.Second)

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
			newSelfDMPollTime, err := p.pollSelfDMUpdates(lastSelfDMPollTime)
			if err != nil {
				p.log("SlackProvider.startPolling: Error polling self DM: %v\n", err)
			} else if newSelfDMPollTime.After(lastSelfDMPollTime) {
				lastSelfDMPollTime = newSelfDMPollTime
			}
			// Huddle updates keep the original message timestamp and are therefore
			// invisible to forward-only message polling. Check only locally active
			// huddles so their end and duration are still captured without RTM.
			p.pollActiveHuddles(ctx)
		case <-huddleStartTicker.C:
			p.pollLatestHuddles(ctx)
		case <-reactionTicker.C:
			// Poll for new reactions on recent messages
			p.pollNewReactions()
		}
	}
}

// pollSelfDMUpdates checks Slack's special "DM with yourself" conversation.
// search.messages can omit that conversation, while conversations.history (the
// endpoint used by manual sync) includes it.
func (p *SlackProvider) pollSelfDMUpdates(since time.Time) (time.Time, error) {
	p.mu.RLock()
	client := p.client
	selfUserID := p.selfUserID
	selfDMChannelID := p.selfDMChannelID
	p.mu.RUnlock()
	if client == nil || selfUserID == "" {
		return since, nil
	}

	if selfDMChannelID == "" {
		channel, _, _, err := client.OpenConversation(&slack.OpenConversationParameters{
			Users:    []string{selfUserID},
			ReturnIM: true,
		})
		if err != nil {
			return since, fmt.Errorf("open self DM: %w", err)
		}
		if channel == nil || channel.ID == "" {
			return since, nil
		}
		selfDMChannelID = channel.ID
		p.mu.Lock()
		p.selfDMChannelID = selfDMChannelID
		p.mu.Unlock()
	}

	oldest := float64(since.Unix()) + float64(since.Nanosecond())/1e9
	history, err := client.GetConversationHistory(&slack.GetConversationHistoryParameters{
		ChannelID: selfDMChannelID,
		Oldest:    fmt.Sprintf("%.6f", oldest),
		Inclusive: false,
		Limit:     100,
	})
	if err != nil {
		return since, fmt.Errorf("get self DM history: %w", err)
	}

	latest := since
	// Slack returns history newest-first. Emit oldest-first for stable UI order.
	for i := len(history.Messages) - 1; i >= 0; i-- {
		slackMessage := history.Messages[i]
		timestamp := parseSlackTimestamp(slackMessage.Timestamp)
		if !timestamp.After(since) {
			continue
		}
		if timestamp.After(latest) {
			latest = timestamp
		}

		msg := p.convertMessage(slackMessage, selfDMChannelID)
		exists := false
		if db.DB != nil {
			var count int64
			if err := db.DB.Model(&models.Message{}).
				Where("protocol_msg_id = ? AND protocol_conv_id = ?", msg.ProtocolMsgID, msg.ProtocolConvID).
				Count(&count).Error; err != nil {
				p.log("SlackProvider.pollSelfDMUpdates: failed checking message %s: %v\n", msg.ProtocolMsgID, err)
			} else {
				exists = count > 0
			}
		}
		if exists {
			continue
		}

		p.storeMessagesForConversation(msg.ProtocolConvID, []models.Message{msg})
		select {
		case p.eventChan <- core.MessageEvent{InstanceID: p.getInstanceId(), Message: msg}:
		default:
			p.log("SlackProvider.pollSelfDMUpdates: event channel full for message %s\n", msg.ProtocolMsgID)
		}
	}

	return latest, nil
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
	params.Count = 100 // Get up to 100 messages per page

	latestTimestamp := since
	newConversationsFound := false
	page := 1

	for {
		params.Page = page
		search, err := client.SearchMessages(query, params)
		if err != nil {
			return latestTimestamp, err
		}

		if len(search.Matches) == 0 {
			break
		}

		p.log("SlackProvider.pollGlobalUpdates: Found %d new messages on page %d via search (since %s)\n",
			len(search.Matches), page, since.Format(time.RFC3339))

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

			// Normalize DM channel IDs ("D...") to User IDs ("U...") for consistency
			// This ensures messages match the conversationId stored in contacts
			conversationID = core.BuildConvID(p.getInstanceId(), p.normalizeDMConversationID(conversationID))

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

			// Detect thread replies via permalink: thread reply URLs contain ?thread_ts=<parent_ts>
			// The SearchMessage struct has no ThreadTimestamp field, so the permalink is the only
			// way to know at search time whether this is a reply (vs a parent that started a thread).
			if match.Permalink != "" {
				if u, err := url.Parse(match.Permalink); err == nil {
					if threadTS := u.Query().Get("thread_ts"); threadTS != "" && threadTS != match.Timestamp {
						msg.ThreadID = &threadTS
					}
				}
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
			case p.eventChan <- core.MessageEvent{InstanceID: p.getInstanceId(), Message: msg}:
			default:
				// Avoid logging per-message drops to save CPU/logs
			}
		}

		if page >= search.Paging.Pages || page >= 10 { // Limit to 10 pages (1000 messages) to prevent infinite loop
			break
		}
		page++
		time.Sleep(100 * time.Millisecond) // Respect rate limits
	}

	// If we found messages in new conversations, trigger a contact refresh
	if newConversationsFound {
		p.log("SlackProvider.pollGlobalUpdates: Triggering contact refresh due to new conversations\n")
		// Emit refresh event
		select {
		case p.eventChan <- core.ContactStatusEvent{InstanceID: p.getInstanceId(), UserID: "refresh", Status: "message_received"}:
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
						case p.eventChan <- core.ReactionEvent{InstanceID: p.getInstanceId(),
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
						case p.eventChan <- core.ReactionEvent{InstanceID: p.getInstanceId(),
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

	// 1. Refresh Contacts (in case of new channels/users)
	// We do this first so we have read markers (LastRead) before catch-up messages arrive
	p.emitSyncStatus(core.SyncStatusFetchingContacts, "Fetching conversations...", 20)

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

	// Save contacts to database and keep the in-memory store in sync.
	if db.DB != nil && instanceID != "" {
		p.log("SlackProvider.SyncHistory: Saving %d contacts to database\n", len(contacts))

		// Use the in-memory store instead of a SELECT to find existing accounts.
		existingByUserID := make(map[string]models.LinkedAccount)
		for _, la := range db.ContactStore.FindByProvider(instanceID) {
			existingByUserID[la.UserID] = la
		}

		now := time.Now()
		var toUpdate []models.LinkedAccount
		var newContacts []models.LinkedAccount

		for i := range contacts {
			contacts[i].ProviderInstanceID = instanceID
			if existing, found := existingByUserID[contacts[i].UserID]; found {
				contacts[i].ID = existing.ID
				contacts[i].MetaContactID = existing.MetaContactID
				contacts[i].CreatedAt = existing.CreatedAt
				contacts[i].UpdatedAt = now
				toUpdate = append(toUpdate, contacts[i])
			} else {
				contacts[i].CreatedAt = now
				contacts[i].UpdatedAt = now
				newContacts = append(newContacts, contacts[i])
			}
		}

		// Batch all updates in a single transaction, then sync the store.
		if len(toUpdate) > 0 {
			if err := db.DB.Transaction(func(tx *gorm.DB) error {
				for i := range toUpdate {
					if err := tx.Save(&toUpdate[i]).Error; err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				p.log("SlackProvider.SyncHistory: Failed to batch-update contacts: %v\n", err)
			} else {
				for _, la := range toUpdate {
					db.ContactStore.UpsertLinkedAccount(la)
				}
			}
		}

		// Create new contacts individually (rare after first sync) — each needs a MetaContact first.
		for i := range newContacts {
			mc := models.MetaContact{
				DisplayName: newContacts[i].Username,
				AvatarURL:   newContacts[i].AvatarURL,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			if err := db.DB.Create(&mc).Error; err != nil {
				p.log("SlackProvider.SyncHistory: Failed to create MetaContact for %s: %v\n", newContacts[i].Username, err)
				continue
			}
			db.ContactStore.UpsertMetaContact(mc)
			newContacts[i].MetaContactID = mc.ID
			newContacts[i].ID = 0
			if err := db.DB.Create(&newContacts[i]).Error; err != nil {
				p.log("SlackProvider.SyncHistory: Failed to create LinkedAccount for %s: %v\n", newContacts[i].UserID, err)
				continue
			}
			db.ContactStore.UpsertLinkedAccount(newContacts[i])
		}

		p.log("SlackProvider.SyncHistory: Contacts saved (%d updated, %d created)\n", len(toUpdate), len(newContacts))
	} else {
		p.log("SlackProvider.SyncHistory: WARNING - Skipping contact save (DB=%v, InstanceID=%s)\n", db.DB != nil, instanceID)
	}

	// Emit contact refresh event immediately to save contacts to database
	// This ensures contacts are available right after sync
	select {
	case p.eventChan <- core.ContactStatusEvent{InstanceID: p.getInstanceId(), UserID: "refresh", Status: "sync_complete"}:
		p.log("SlackProvider.SyncHistory: Emitted contacts-refresh event\n")
	default:
		p.log("SlackProvider.SyncHistory: Failed to emit contacts-refresh event (channel full)\n")
	}

	// Fetch real-time presence for DM contacts using users.getPresence.
	// users.list presence=true and users.info are both deprecated/unreliable for presence.
	// users.getPresence is the only Slack endpoint that returns accurate real-time presence.
	// We combine it with cached status text/emoji (from initializeStatusCache) to also
	// detect "meeting", "busy", "holiday" etc. for away users with custom status.
	go func(dmContacts []models.LinkedAccount, instanceID string) {
		p.mu.RLock()
		client := p.client
		p.mu.RUnlock()
		if client == nil {
			return
		}

		seen := make(map[string]bool)
		for _, contact := range dmContacts {
			if contact.IsGroup || contact.UserID == "" || seen[contact.UserID] {
				continue
			}
			seen[contact.UserID] = true

			userPresence, err := client.GetUserPresence(contact.UserID)
			if err != nil {
				p.log("SlackProvider.SyncHistory: GetUserPresence failed for %s: %v\n", contact.UserID, err)
				continue
			}

			// Combine real-time presence with cached status text/emoji.
			p.statusCacheMu.RLock()
			cached := p.statusCache[contact.UserID]
			p.statusCacheMu.RUnlock()

			newStatus := p.determineStatus(userPresence.Presence, cached.statusText, cached.statusEmoji)
			p.log("SlackProvider.SyncHistory: presence for %s: presence=%s status=%s (cached text=%q emoji=%q)\n",
				contact.UserID, userPresence.Presence, newStatus, cached.statusText, cached.statusEmoji)

			if contact.Status == newStatus {
				continue
			}
			contact.Status = newStatus
			db.ContactStore.UpsertLinkedAccount(contact)
			if db.DB != nil {
				db.DB.Model(&contact).Update("status", newStatus)
			}
		}
		select {
		case p.eventChan <- core.ContactStatusEvent{
			InstanceID: p.getInstanceId(),
			UserID:     "refresh",
			Status:     "sync_complete",
		}:
		default:
		}
	}(contacts, instanceID)

	// Fetch and emit last_read timestamp for each conversation.
	// Also build maps for incrementalSyncExistingConversations to avoid per-conversation API calls.
	p.log("SlackProvider.SyncHistory: Emitting last_read timestamps from cached metadata\n")
	contactLastRead := make(map[string]string, len(contacts))
	contactLatestTS := make(map[string]string, len(contacts))
	for _, contact := range contacts {
		rawConvID := contact.UserID
		conversationID := core.BuildConvID(p.getInstanceId(), rawConvID)

		if contact.Extra == "" {
			continue
		}

		var extra map[string]interface{}
		if err := json.Unmarshal([]byte(contact.Extra), &extra); err != nil {
			continue
		}

		if lastRead, ok := extra["last_read"].(string); ok && lastRead != "" {
			contactLastRead[rawConvID] = lastRead
			select {
			case p.eventChan <- core.ConversationReadStatusEvent{InstanceID: p.getInstanceId(),
				ConversationID: conversationID,
				LastReadTS:     lastRead,
			}:
			default:
			}
		}
		if latestTS, ok := extra["latest_ts"].(string); ok && latestTS != "" {
			contactLatestTS[rawConvID] = latestTS
		}
	}

	// 2. Recover missing messages using Search API
	// This acts like a "catch-up" poll. Since we emitted LastRead markers above,
	// incoming catch-up messages will be correctly categorized as read/unread by the frontend.
	p.emitSyncStatus(core.SyncStatusFetchingHistory, "Syncing recent messages via Search...", 50)
	ctx := context.Background()
	p.log("SlackProvider.SyncHistory: Calling pollGlobalUpdates...\n")
	_, err = p.pollGlobalUpdates(ctx, since)
	p.log("SlackProvider.SyncHistory: pollGlobalUpdates returned (err=%v)\n", err)
	if err != nil {
		p.log("SlackProvider.SyncHistory: Warning: Search sync failed: %v.\n", err)
	} else {
		p.emitSyncStatus(core.SyncStatusFetchingHistory, "Recent messages synced.", 70)
		time.Sleep(500 * time.Millisecond)
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

	if len(contacts) == 0 {
		p.emitSyncStatus(core.SyncStatusCompleted, "Sync completed - no conversations", 100)
		p.log("SlackProvider.SyncHistory: Sync completed (no conversations)\n")
		return nil
	}

	// Run incremental sync to catch any messages missed in conversations already in the DB.
	// incrementalSyncExistingConversations emits its own final "completed" status.
	p.log("SlackProvider.SyncHistory: Starting incremental sync for existing conversations\n")
	p.incrementalSyncExistingConversations(contactLatestTS, contactLastRead)

	p.log("SlackProvider.SyncHistory: Sync fully completed\n")
	return nil
}

// emitSyncStatus emits a sync status event to the frontend
func (p *SlackProvider) emitSyncStatus(status core.SyncStatusType, message string, progress int) {
	p.log("SlackProvider: Emitting sync status: status=%s, message=%s, progress=%d\n", status, message, progress)
	select {
	case p.eventChan <- core.SyncStatusEvent{InstanceID: p.getInstanceId(),
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
