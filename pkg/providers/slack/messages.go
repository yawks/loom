// Package slack provides message handling for the Slack provider.
package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"bytes"
	"fmt"
	"strconv"
	"time"

	"github.com/slack-go/slack"
)

// SendMessage sends a text message to a given conversation.
func (p *SlackProvider) SendMessage(conversationID string, text string, file *core.Attachment, threadID *string) (*models.Message, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
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

	_, timestamp, err := p.client.PostMessage(conversationID, opts...)
	if err != nil {
		return nil, err
	}

	ts := parseSlackTimestamp(timestamp)

	return &models.Message{
		ProtocolMsgID:  timestamp,
		ProtocolConvID: conversationID,
		Body:           text,
		Timestamp:      ts,
		IsFromMe:       true,
	}, nil
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

	return &models.Message{
		ProtocolMsgID:  fileUpload.ID, // Warning based on above limitation
		ProtocolConvID: conversationID,
		Body:           fmt.Sprintf("Sent file: %s", file.FileName),
		Timestamp:      time.Now(),
		IsFromMe:       true,
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
		query = query.Preload("Receipts").Preload("Reactions").Order("timestamp DESC").Limit(limit)

		if err := query.Find(&dbMessages).Error; err == nil && len(dbMessages) > 0 {
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
		params.Latest = fmt.Sprintf("%f", float64(beforeTimestamp.Unix()))
	}

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

	// Persist messages to database
	if db.DB != nil {
		for _, msg := range messages {
			if msg.ProtocolMsgID == "" {
				continue
			}
			var existingMsg models.Message
			err := db.DB.Where("protocol_msg_id = ?", msg.ProtocolMsgID).First(&existingMsg).Error
			if err != nil {
				// Message doesn't exist, create it
				msg.ProtocolConvID = convID
				if err := db.DB.Create(&msg).Error; err != nil {
					p.log("SlackProvider.storeMessagesForConversation: Failed to persist message %s: %v\n", msg.ProtocolMsgID, err)
				} else {
					p.log("SlackProvider.storeMessagesForConversation: Stored message %s to database for conversation %s\n", msg.ProtocolMsgID, convID)
				}
			} else {
				// Message exists, update it if needed
				msg.ID = existingMsg.ID
				msg.ProtocolConvID = convID
				if err := db.DB.Save(&msg).Error; err != nil {
					p.log("SlackProvider.storeMessagesForConversation: Failed to update message %s: %v\n", msg.ProtocolMsgID, err)
				}
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
	isFromMe := false
	if p.client != nil {
		authTest, err := p.client.AuthTest()
		if err == nil && authTest != nil && authTest.UserID == msg.User {
			isFromMe = true
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
	return p.client.AddReaction(emoji, item)
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
	return p.client.RemoveReaction(emoji, item)
}
