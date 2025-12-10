// Package slack provides message handling for the Slack provider.
package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// CleanSlackEmoji removes skin-tone modifiers from Slack emoji strings.
// This is exported so it can be used by app.go when processing reaction events.
// Examples:
//
//	":santa::skin-tone-2:" -> ":santa:"
//	":+1::skin-tone-2:" -> ":+1:"
//	":thumbsup::skin-tone-3:" -> ":thumbsup:"
func CleanSlackEmoji(emoji string) string {
	// Remove skin-tone modifiers (skin-tone-2 through skin-tone-6)
	// Pattern matches :skin-tone-X: anywhere in the string
	re := regexp.MustCompile(`:skin-tone-[2-6]:`)
	return re.ReplaceAllString(emoji, "")
}

// cleanSlackEmoji is an alias for CleanSlackEmoji for internal use
func cleanSlackEmoji(emoji string) string {
	return CleanSlackEmoji(emoji)
}

// getCurrentUserInfo gets the current authenticated user's ID, name, and avatar
// Uses cache to avoid repeated API calls
func (p *SlackProvider) getCurrentUserInfo() (userID string, userName string, avatarURL string, err error) {
	// Check cache first
	p.currentUserIDMu.RLock()
	if p.currentUserID != "" {
		userID = p.currentUserID
		p.currentUserIDMu.RUnlock()
		// Get user info from cache
		p.userCacheMu.RLock()
		user, cached := p.userCache[userID]
		p.userCacheMu.RUnlock()
		if cached && user != nil {
			userName = user.RealName
			if userName == "" && user.Profile.DisplayName != "" {
				userName = user.Profile.DisplayName
			}
			if userName == "" {
				userName = user.Name
			}
			// Get avatar URL with fallback to different sizes
			if user.Profile.Image512 != "" {
				avatarURL = user.Profile.Image512
			} else if user.Profile.Image192 != "" {
				avatarURL = user.Profile.Image192
			} else if user.Profile.Image72 != "" {
				avatarURL = user.Profile.Image72
			} else if user.Profile.Image48 != "" {
				avatarURL = user.Profile.Image48
			} else if user.Profile.Image32 != "" {
				avatarURL = user.Profile.Image32
			}
			return userID, userName, avatarURL, nil
		}
	} else {
		p.currentUserIDMu.RUnlock()
	}

	// Not in cache, get from API
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return "", "", "", fmt.Errorf("slack client not initialized")
	}

	authTest, err := client.AuthTest()
	if err != nil || authTest == nil {
		return "", "", "", fmt.Errorf("failed to get current user: %w", err)
	}

	userID = authTest.UserID
	// Cache the user ID
	p.currentUserIDMu.Lock()
	p.currentUserID = userID
	p.currentUserIDMu.Unlock()

	// Get user info and cache it
	user, err := client.GetUserInfo(userID)
	if err == nil && user != nil {
		// Cache the user info
		p.userCacheMu.Lock()
		p.userCache[userID] = user
		p.userCacheMu.Unlock()

		userName = user.RealName
		if userName == "" && user.Profile.DisplayName != "" {
			userName = user.Profile.DisplayName
		}
		if userName == "" {
			userName = user.Name
		}
		// Get avatar URL with fallback to different sizes
		if user.Profile.Image512 != "" {
			avatarURL = user.Profile.Image512
		} else if user.Profile.Image192 != "" {
			avatarURL = user.Profile.Image192
		} else if user.Profile.Image72 != "" {
			avatarURL = user.Profile.Image72
		} else if user.Profile.Image48 != "" {
			avatarURL = user.Profile.Image48
		} else if user.Profile.Image32 != "" {
			avatarURL = user.Profile.Image32
		}
	}

	return userID, userName, avatarURL, nil
}

// SendMessage sends a text message to a given conversation.
func (p *SlackProvider) SendMessage(conversationID string, text string, file *core.Attachment, threadID *string) (*models.Message, error) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	if file != nil {
		return p.SendFile(conversationID, file, threadID)
	}

	opts := []slack.MsgOption{
		slack.MsgOptionText(text, false),
	}
	if threadID != nil {
		opts = append(opts, slack.MsgOptionTS(*threadID))
	}

	_, timestamp, err := client.PostMessage(conversationID, opts...)
	if err != nil {
		return nil, err
	}

	ts := parseSlackTimestamp(timestamp)

	// Get current user info for sender details
	currentUserID, currentUserName, currentAvatarURL, err := p.getCurrentUserInfo()
	if err != nil {
		p.log("SlackProvider.SendMessage: WARNING - failed to get current user info: %v\n", err)
		// Continue without user info - IsFromMe will still be true
	}

	sentMessage := &models.Message{
		ProtocolMsgID:   timestamp,
		ProtocolConvID:  conversationID,
		Body:            text,
		SenderID:        currentUserID,
		SenderName:      currentUserName,
		SenderAvatarURL: currentAvatarURL,
		Timestamp:       ts,
		IsFromMe:        true,
	}

	// Store message in database
	if db.DB != nil {
		if err := db.DB.Create(sentMessage).Error; err != nil {
			p.log("SlackProvider.SendMessage: Failed to store sent message %s: %v\n", timestamp, err)
		} else {
			p.log("SlackProvider.SendMessage: Stored sent message %s to database\n", timestamp)
		}
	}

	// Emit MessageEvent to notify frontend (similar to WhatsApp)
	select {
	case p.eventChan <- core.MessageEvent{Message: *sentMessage}:
		p.log("SlackProvider.SendMessage: MessageEvent emitted successfully for sent message %s\n", timestamp)
	default:
		p.log("SlackProvider.SendMessage: WARNING - Failed to emit MessageEvent (channel full) for sent message %s\n", timestamp)
	}

	return sentMessage, nil
}

// SendReply sends a text message as a reply to another message.
func (p *SlackProvider) SendReply(conversationID string, text string, quotedMessageID string) (*models.Message, error) {
	return p.SendMessage(conversationID, text, nil, &quotedMessageID)
}

// SendFile sends a file to a given conversation without text.
func (p *SlackProvider) SendFile(conversationID string, file *core.Attachment, threadID *string) (*models.Message, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	params := slack.UploadFileV2Parameters{
		Channel:  conversationID,
		File:     file.FileName,
		Reader:   bytes.NewReader(file.Data),
		FileSize: file.FileSize,
		Filename: file.FileName,
	}
	if threadID != nil {
		params.ThreadTimestamp = *threadID
	}

	fileUpload, err := p.client.UploadFileV2(params)
	if err != nil {
		return nil, err
	}

	// UploadFileV2 returns a generic File object, not a message ts.
	// This is a known limitation of V2 upload helper not returning the message ts immediately easily
	// without extra call or if we assume it creates one.
	// For now we use the file ID as a placeholder if we must return something unique.

	// Get current user info for sender details
	currentUserID, currentUserName, currentAvatarURL, err := p.getCurrentUserInfo()
	if err != nil {
		p.log("SlackProvider.SendFile: WARNING - failed to get current user info: %v\n", err)
		// Continue without user info - IsFromMe will still be true
	}

	return &models.Message{
		ProtocolMsgID:   fileUpload.ID, // Warning based on above limitation
		ProtocolConvID:  conversationID,
		Body:            fmt.Sprintf("Sent file: %s", file.FileName),
		SenderID:        currentUserID,
		SenderName:      currentUserName,
		SenderAvatarURL: currentAvatarURL,
		Timestamp:       time.Now(),
		IsFromMe:        true,
	}, nil
}

// GetConversationHistory retrieves the message history for a specific conversation.
// It first checks the database, and if not enough messages are found, fetches from Slack API and stores them.
func (p *SlackProvider) GetConversationHistory(conversationID string, limit int, beforeTimestamp *time.Time) ([]models.Message, error) {
	if conversationID == "" {
		return []models.Message{}, fmt.Errorf("conversation ID is required")
	}

	// Default limit to 20 if not specified
	if limit <= 0 {
		limit = 20
	}

	// First, try to load from database
	if db.DB != nil {
		var dbMessages []models.Message
		query := db.DB.Where("protocol_conv_id = ?", conversationID)

		// If beforeTimestamp is specified, only get messages before that timestamp
		if beforeTimestamp != nil {
			query = query.Where("timestamp < ?", *beforeTimestamp)
		}

		// Order by timestamp descending to get newest first, then reverse
		// Load messages first without Preload to reduce DB contention
		query = query.Order("timestamp DESC").Limit(limit)

		if err := query.Find(&dbMessages).Error; err == nil && len(dbMessages) > 0 {
			// Reverse to get oldest first
			for i, j := 0, len(dbMessages)-1; i < j; i, j = i+1, j-1 {
				dbMessages[i], dbMessages[j] = dbMessages[j], dbMessages[i]
			}

			// Load receipts and reactions separately in a single batch query to reduce DB contention
			if len(dbMessages) > 0 {
				messageIDs := make([]uint, len(dbMessages))
				for i, msg := range dbMessages {
					messageIDs[i] = msg.ID
				}

				// Batch load receipts
				var receipts []models.MessageReceipt
				if err := db.DB.Where("message_id IN ?", messageIDs).Find(&receipts).Error; err == nil {
					receiptsMap := make(map[uint][]models.MessageReceipt)
					for _, receipt := range receipts {
						receiptsMap[receipt.MessageID] = append(receiptsMap[receipt.MessageID], receipt)
					}
					for i := range dbMessages {
						dbMessages[i].Receipts = receiptsMap[dbMessages[i].ID]
					}
				}

				// Batch load reactions
				var reactions []models.Reaction
				if err := db.DB.Where("message_id IN ?", messageIDs).Find(&reactions).Error; err == nil {
					reactionsMap := make(map[uint][]models.Reaction)
					for _, reaction := range reactions {
						reactionsMap[reaction.MessageID] = append(reactionsMap[reaction.MessageID], reaction)
					}
					for i := range dbMessages {
						dbMessages[i].Reactions = reactionsMap[dbMessages[i].ID]
					}
				}
			}
			// Reverse to get oldest first
			for i, j := 0, len(dbMessages)-1; i < j; i, j = i+1, j-1 {
				dbMessages[i], dbMessages[j] = dbMessages[j], dbMessages[i]
			}

			// Enrich messages with sender names and avatars from cache
			p.enrichMessagesWithSenderInfo(dbMessages)

			// If beforeTimestamp is nil (initial load) and we have enough messages, return them
			// If beforeTimestamp is set (loading older messages), return what we have
			if beforeTimestamp != nil || len(dbMessages) >= limit {
				p.log("SlackProvider.GetConversationHistory: Loaded %d messages from database for conversation %s\n", len(dbMessages), conversationID)
				return dbMessages, nil
			}
		}
	}

	// Not enough messages in database or beforeTimestamp specified, fetch from Slack API
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	// Handle different ID types for Slack conversations
	actualChannelID := conversationID

	// If conversationID is a user ID (starts with "U"), we need to open the DM conversation
	// to get the actual channel ID (which starts with "D")
	if len(conversationID) > 0 && conversationID[0] == 'U' {
		// Open the DM conversation with this user to get the channel ID
		channel, _, _, err := p.client.OpenConversation(&slack.OpenConversationParameters{
			Users: []string{conversationID},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to open DM conversation with user %s: %w", conversationID, err)
		}
		if channel == nil || channel.ID == "" {
			return nil, fmt.Errorf("failed to get DM channel ID for user %s", conversationID)
		}
		actualChannelID = channel.ID
		p.log("SlackProvider.GetConversationHistory: Opened DM conversation, user ID %s -> channel ID %s\n", conversationID, actualChannelID)
	} else if len(conversationID) > 0 && conversationID[0] == 'D' {
		// For DM channel IDs, ensure the conversation is open
		// This is required before we can retrieve message history
		_, _, _, err := p.client.OpenConversation(&slack.OpenConversationParameters{
			ChannelID: conversationID,
		})
		if err != nil {
			// Log but don't fail - the conversation might already be open
			p.log("SlackProvider.GetConversationHistory: Warning - failed to open DM conversation %s: %v (may already be open)\n", conversationID, err)
		}
	}

	params := &slack.GetConversationHistoryParameters{
		ChannelID: actualChannelID,
		Limit:     limit,
	}
	if beforeTimestamp != nil {
		// If beforeTimestamp is provided, it means we want messages BEFORE that timestamp
		// This is used for pagination (loading older messages)
		params.Latest = fmt.Sprintf("%f", float64(beforeTimestamp.Unix()))
	}
	// Note: To get messages SINCE a date, we would use Oldest parameter
	// But GetConversationHistory is designed for pagination (beforeTimestamp)
	// For SyncHistory, we'll fetch recent messages and filter client-side

	history, err := p.client.GetConversationHistory(params)
	if err != nil {
		return nil, err
	}

	var messages []models.Message
	for _, msg := range history.Messages {
		messages = append(messages, p.convertSlackMessage(msg, actualChannelID))
	}

	// Reverse to oldest first
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	// Store messages in database
	if len(messages) > 0 {
		p.storeMessagesForConversation(conversationID, messages)
	}

	return messages, nil
}

// storeMessagesForConversation stores messages in the database for a conversation
func (p *SlackProvider) storeMessagesForConversation(convID string, messages []models.Message) int {
	if convID == "" || len(messages) == 0 {
		return 0
	}

	// Persist messages to database using batch operations to reduce DB contention
	if db.DB != nil && len(messages) > 0 {
		// Filter out messages without ProtocolMsgID
		validMessages := make([]models.Message, 0, len(messages))
		protocolMsgIDs := make([]string, 0, len(messages))
		for i := range messages {
			if messages[i].ProtocolMsgID != "" {
				messages[i].ProtocolConvID = convID
				validMessages = append(validMessages, messages[i])
				protocolMsgIDs = append(protocolMsgIDs, messages[i].ProtocolMsgID)
			}
		}

		if len(validMessages) == 0 {
			return 0
		}

		// Batch check which messages already exist
		var existingMessages []models.Message
		if err := db.DB.Where("protocol_msg_id IN ?", protocolMsgIDs).Find(&existingMessages).Error; err != nil {
			p.log("SlackProvider.storeMessagesForConversation: Failed to check existing messages: %v\n", err)
			return 0
		}

		// Create a map of existing messages by ProtocolMsgID
		existingMap := make(map[string]models.Message)
		for _, existing := range existingMessages {
			existingMap[existing.ProtocolMsgID] = existing
		}

		// Separate new messages from updates
		var toCreate []models.Message
		var toUpdate []models.Message
		for i := range validMessages {
			if existing, exists := existingMap[validMessages[i].ProtocolMsgID]; exists {
				validMessages[i].ID = existing.ID
				toUpdate = append(toUpdate, validMessages[i])
			} else {
				toCreate = append(toCreate, validMessages[i])
			}
		}

		// Batch create new messages
		if len(toCreate) > 0 {
			// Use CreateInBatches to reduce transaction overhead
			if err := db.DB.CreateInBatches(toCreate, 50).Error; err != nil {
				p.log("SlackProvider.storeMessagesForConversation: Failed to batch create messages: %v\n", err)
			} else {
				p.log("SlackProvider.storeMessagesForConversation: Batch created %d new messages for conversation %s\n", len(toCreate), convID)
			}
		}

		// Batch update existing messages (only if there are updates)
		if len(toUpdate) > 0 {
			// Update in smaller batches to avoid long transactions
			batchSize := 20
			for i := 0; i < len(toUpdate); i += batchSize {
				end := i + batchSize
				if end > len(toUpdate) {
					end = len(toUpdate)
				}
				batch := toUpdate[i:end]
				for j := range batch {
					if err := db.DB.Model(&models.Message{}).Where("id = ?", batch[j].ID).Updates(map[string]interface{}{
						"body":              batch[j].Body,
						"timestamp":         batch[j].Timestamp,
						"is_from_me":        batch[j].IsFromMe,
						"attachments":       batch[j].Attachments,
						"is_status_message": batch[j].IsStatusMessage,
						"is_deleted":        batch[j].IsDeleted,
						"is_edited":         batch[j].IsEdited,
						"edited_timestamp":  batch[j].EditedTimestamp,
					}).Error; err != nil {
						p.log("SlackProvider.storeMessagesForConversation: Failed to update message %s: %v\n", batch[j].ProtocolMsgID, err)
					}
				}
			}
			if len(toUpdate) > 0 {
				p.log("SlackProvider.storeMessagesForConversation: Batch updated %d existing messages for conversation %s\n", len(toUpdate), convID)
			}
		}
	}

	return len(messages)
}

// GetThreads loads all messages in a discussion thread.
func (p *SlackProvider) GetThreads(parentMessageID string) ([]models.Message, error) {
	return nil, fmt.Errorf("not implemented: requires conversationID")
}

// EditMessage edits an existing message.
func (p *SlackProvider) EditMessage(conversationID string, messageID string, newText string) (*models.Message, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	_, _, _, err := p.client.UpdateMessage(conversationID, messageID, slack.MsgOptionText(newText, false))
	if err != nil {
		return nil, err
	}

	return &models.Message{
		ProtocolMsgID:  messageID,
		ProtocolConvID: conversationID,
		Body:           newText,
		Timestamp:      time.Now(), // rough estimate
		IsFromMe:       true,
	}, nil
}

// DeleteMessage deletes a message.
func (p *SlackProvider) DeleteMessage(conversationID string, messageID string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	_, _, err := p.client.DeleteMessage(conversationID, messageID)
	return err
}

func (p *SlackProvider) convertSlackMessage(msg slack.Message, conversationID string) models.Message {
	ts := parseSlackTimestamp(msg.Timestamp)

	// Get sender name and avatar
	senderName := ""
	senderAvatarURL := ""
	if msg.User != "" {
		// Check cache first
		p.userCacheMu.RLock()
		user, cached := p.userCache[msg.User]
		p.userCacheMu.RUnlock()

		if !cached {
			// Try to get user info from Slack API
			var err error
			user, err = p.client.GetUserInfo(msg.User)
			if err == nil && user != nil {
				// Cache the user info
				p.userCacheMu.Lock()
				p.userCache[msg.User] = user
				p.userCacheMu.Unlock()
			} else {
				// Fallback: use user ID if we can't get user info
				senderName = msg.User
				p.log("SlackProvider.convertSlackMessage: WARNING - failed to get user info for %s: %v\n", msg.User, err)
			}
		}

		if user != nil {
			// Use RealName if available, fallback to DisplayName, then Name
			senderName = user.RealName
			if senderName == "" && user.Profile.DisplayName != "" {
				senderName = user.Profile.DisplayName
			}
			if senderName == "" {
				senderName = user.Name
			}

			// Get avatar URL with fallback to different sizes
			if user.Profile.Image512 != "" {
				senderAvatarURL = user.Profile.Image512
			} else if user.Profile.Image192 != "" {
				senderAvatarURL = user.Profile.Image192
			} else if user.Profile.Image72 != "" {
				senderAvatarURL = user.Profile.Image72
			} else if user.Profile.Image48 != "" {
				senderAvatarURL = user.Profile.Image48
			} else if user.Profile.Image32 != "" {
				senderAvatarURL = user.Profile.Image32
			}
		}
	}

	// Check if message is from me (compare with authenticated user)
	// Use cached currentUserID to avoid repeated API calls
	isFromMe := false
	if msg.User != "" {
		p.currentUserIDMu.RLock()
		cachedUserID := p.currentUserID
		p.currentUserIDMu.RUnlock()

		if cachedUserID != "" {
			// Use cached ID
			isFromMe = (cachedUserID == msg.User)
		} else if p.client != nil {
			// Not cached, get from API and cache it
			authTest, err := p.client.AuthTest()
			if err == nil && authTest != nil {
				// Cache the user ID
				p.currentUserIDMu.Lock()
				p.currentUserID = authTest.UserID
				p.currentUserIDMu.Unlock()
				isFromMe = (authTest.UserID == msg.User)
			}
		}
	}

	// Convert Slack reactions to our Reaction model
	var reactions []models.Reaction
	if len(msg.Reactions) > 0 {
		for _, slackReaction := range msg.Reactions {
			// Each Slack reaction has a Name (emoji), Count, and Users (user IDs)
			// We need to create a Reaction for each user who reacted

			// Clean emoji name by removing skin-tone modifiers
			// Slack stores emojis like "+1::skin-tone-2:" or "thumbsup::skin-tone-3:"
			emojiName := slackReaction.Name
			cleanedEmoji := cleanSlackEmoji(emojiName)

			for _, userID := range slackReaction.Users {
				reactions = append(reactions, models.Reaction{
					UserID:    userID,
					Emoji:     cleanedEmoji,
					CreatedAt: ts, // Use message timestamp as fallback (Slack doesn't provide individual reaction timestamps)
					UpdatedAt: ts,
				})
			}
		}
	}

	return models.Message{
		ProtocolMsgID:   msg.Timestamp,
		ProtocolConvID:  conversationID,
		Body:            msg.Text,
		SenderID:        msg.User,
		SenderName:      senderName,
		SenderAvatarURL: senderAvatarURL,
		Timestamp:       ts,
		IsFromMe:        isFromMe,
		Reactions:       reactions,
	}
}

func parseSlackTimestamp(tsStr string) time.Time {
	f, err := strconv.ParseFloat(tsStr, 64)
	if err != nil {
		return time.Now()
	}
	sec := int64(f)
	nsec := int64((f - float64(sec)) * 1e9)
	return time.Unix(sec, nsec)
}

// enrichMessagesWithSenderInfo enriches messages with sender names and avatars from the user cache.
// This is used when loading messages from the database to ensure sender information is up to date.
func (p *SlackProvider) enrichMessagesWithSenderInfo(messages []models.Message) {
	if len(messages) == 0 {
		return
	}

	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return
	}

	for i := range messages {
		msg := &messages[i]

		// Skip only if both name and avatar are already populated
		// We still enrich if one is missing
		if msg.SenderName != "" && msg.SenderAvatarURL != "" {
			continue
		}

		// Only enrich if we have a SenderID
		if msg.SenderID == "" {
			continue
		}

		// Check cache first
		var user *slack.User
		var cached bool
		p.userCacheMu.RLock()
		user, cached = p.userCache[msg.SenderID]
		p.userCacheMu.RUnlock()

		if !cached {
			// Try to get user info from Slack API
			var err error
			p.mu.RLock()
			if p.client != nil {
				user, err = p.client.GetUserInfo(msg.SenderID)
			}
			p.mu.RUnlock()

			if err == nil && user != nil {
				// Cache the user info
				p.userCacheMu.Lock()
				p.userCache[msg.SenderID] = user
				p.userCacheMu.Unlock()
			}
		}

		if user != nil {
			// Update sender name if not set
			if msg.SenderName == "" {
				// Use RealName if available, fallback to DisplayName, then Name
				msg.SenderName = user.RealName
				if msg.SenderName == "" && user.Profile.DisplayName != "" {
					msg.SenderName = user.Profile.DisplayName
				}
				if msg.SenderName == "" {
					msg.SenderName = user.Name
				}
			}

			// Update avatar URL if not set
			if msg.SenderAvatarURL == "" {
				// Get avatar URL with fallback to different sizes
				if user.Profile.Image512 != "" {
					msg.SenderAvatarURL = user.Profile.Image512
				} else if user.Profile.Image192 != "" {
					msg.SenderAvatarURL = user.Profile.Image192
				} else if user.Profile.Image72 != "" {
					msg.SenderAvatarURL = user.Profile.Image72
				} else if user.Profile.Image48 != "" {
					msg.SenderAvatarURL = user.Profile.Image48
				} else if user.Profile.Image32 != "" {
					msg.SenderAvatarURL = user.Profile.Image32
				}
			}
		}
	}
}

// SendTypingIndicator sends a typing indicator.
func (p *SlackProvider) SendTypingIndicator(conversationID string, isTyping bool) error {
	return nil
}

// AddReaction adds a reaction (emoji) to a message.
func (p *SlackProvider) AddReaction(conversationID string, messageID string, emoji string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	item := slack.ItemRef{
		Channel:   conversationID,
		Timestamp: messageID,
	}
	err := p.client.AddReaction(emoji, item)
	// Ignore "already_reacted" error as it's not really an error for our use case
	if err != nil && strings.Contains(err.Error(), "already_reacted") {
		return nil
	}
	return err
}

// RemoveReaction removes a reaction (emoji) from a message.
func (p *SlackProvider) RemoveReaction(conversationID string, messageID string, emoji string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	item := slack.ItemRef{
		Channel:   conversationID,
		Timestamp: messageID,
	}
	err := p.client.RemoveReaction(emoji, item)
	// Ignore "no_reaction" error as it's not really an error for our use case
	if err != nil && strings.Contains(err.Error(), "no_reaction") {
		return nil
	}
	return err
}
