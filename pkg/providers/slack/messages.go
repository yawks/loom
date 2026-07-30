// Package slack provides message handling for the Slack provider.
package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"Loom/pkg/providers/messageformat"
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// huddleURLRegex extracts a huddle join URL from a raw Slack message text.
// Slack encodes URLs as <https://...|label>, so we match the URL part only.
var huddleURLRegex = regexp.MustCompile(`<(https://[^|>]*slack\.com/huddle[^|>]*)[|>]`)

// SendMessage sends a text message to a given conversation.
func (p *SlackProvider) SendMessage(conversationID string, text string, file *core.Attachment, threadID *string) (*models.Message, error) {
	canonicalText := text
	text = messageformat.Slack(text)
	// Normalize conversation ID early to ensure consistency
	normalizedConversationID := p.normalizeDMConversationID(core.StripConvID(conversationID))
	nsConvID := core.BuildConvID(p.getInstanceId(), normalizedConversationID)

	p.mu.RLock()
	client := p.client
	eventChan := p.eventChan
	p.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	if file != nil {
		return p.SendFile(normalizedConversationID, file, threadID)
	}

	// Handle different ID types for Slack conversations (user ID -> channel ID for DMs)
	actualChannelID := normalizedConversationID
	if len(normalizedConversationID) > 0 && normalizedConversationID[0] == 'U' {
		// Open DM to get channel ID
		channel, _, _, err := client.OpenConversation(&slack.OpenConversationParameters{
			Users:    []string{normalizedConversationID},
			ReturnIM: true,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to open DM conversation: %w", err)
		}
		if channel == nil || channel.ID == "" {
			return nil, fmt.Errorf("failed to get DM channel ID for user %s", normalizedConversationID)
		}
		actualChannelID = channel.ID
	}

	opts := []slack.MsgOption{
		slack.MsgOptionText(text, false),
	}
	if threadID != nil {
		opts = append(opts, slack.MsgOptionTS(*threadID))
	}

	_, timestamp, err := client.PostMessage(actualChannelID, opts...)
	if err != nil {
		return nil, err
	}

	ts := parseSlackTimestamp(timestamp)

	// Get current user info for sender details
	senderID := p.selfUserID
	if senderID == "" {
		authTest, err := client.AuthTest()
		if err == nil && authTest != nil {
			senderID = authTest.UserID
			p.mu.Lock()
			p.selfUserID = senderID
			p.mu.Unlock()
		}
	}
	senderName, senderAvatarURL := p.resolveUserInfo(senderID)

	sentMessage := &models.Message{
		ProtocolMsgID:   timestamp,
		ProtocolConvID:  nsConvID,
		SenderID:        senderID,
		SenderName:      senderName,
		SenderAvatarURL: senderAvatarURL,
		Body:            canonicalText,
		Timestamp:       ts,
		IsFromMe:        true,
	}
	// A quoted reply is encoded by Slack as Markdown text. Normalize it before
	// persisting or emitting the local send event; SendThreadReply will replace
	// this provisional quote ID with the real target ID immediately afterwards.
	if cleanText, quotedSenderName, quotedBody, isQuote := p.parseSlackBlockQuote(text); isQuote {
		quotedMessageID := timestamp + "-quote"
		sentMessage.Body = cleanText
		sentMessage.QuotedMessageID = &quotedMessageID
		sentMessage.QuotedSenderName = quotedSenderName
		sentMessage.QuotedBody = &quotedBody
	}
	if threadID != nil {
		sentMessage.ThreadID = threadID
	}

	// Store message in database first (so we can create receipts)
	if db.DB != nil {
		// Ensure Conversation exists and get ConversationID (using normalized ID)
		convID, err := p.ensureConversation(normalizedConversationID)
		if err != nil {
			p.log("SlackProvider.SendMessage: Failed to ensure conversation: %v\n", err)
		} else {
			sentMessage.ConversationID = convID
		}

		// Check if message already exists (shouldn't, but just in case)
		var existingMsg models.Message
		if err := db.DB.Where("protocol_msg_id = ? AND protocol_conv_id = ?", timestamp, nsConvID).First(&existingMsg).Error; err != nil {
			// Message doesn't exist, create it
			if err := db.DB.Create(sentMessage).Error; err != nil {
				p.log("SlackProvider.SendMessage: Failed to store message in database: %v\n", err)
			} else {
				p.log("SlackProvider.SendMessage: Stored sent message %s in database\n", timestamp)
				// Create a "delivery" receipt for the message (since it was successfully sent)
				// For DMs, we can create a receipt for the other participant
				// For now, we'll skip receipts for Slack as it doesn't have native delivery receipts
			}
		} else {
			// Message exists, update it
			sentMessage.ID = existingMsg.ID
			if err := db.DB.Save(sentMessage).Error; err != nil {
				p.log("SlackProvider.SendMessage: Failed to update message in database: %v\n", err)
			} else {
				p.log("SlackProvider.SendMessage: Updated sent message %s in database\n", timestamp)
			}
		}
	}

	// Emit MessageEvent to notify frontend
	if eventChan != nil {
		select {
		case eventChan <- core.MessageEvent{InstanceID: p.getInstanceId(), Message: *sentMessage}:
			p.log("SlackProvider.SendMessage: MessageEvent emitted successfully for sent message %s\n", timestamp)
		default:
			p.log("SlackProvider.SendMessage: WARNING - Failed to emit MessageEvent (channel full) for sent message %s\n", timestamp)
		}
	}

	return sentMessage, nil
}

// parseSlackBlockQuote extracts quoted message details from Slack block quote syntax (> *Name*\n> text...)
func (p *SlackProvider) parseSlackBlockQuote(rawText string) (cleanText string, quotedSenderName string, quotedBody string, isQuote bool) {
	// RTM encodes leading quote markers as &gt;. Decode before parsing so the
	// transport representation and the REST representation behave identically.
	rawText = html.UnescapeString(rawText)
	lines := strings.Split(rawText, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "> *") {
		return rawText, "", "", false
	}

	firstLine := lines[0]
	endIdx := strings.LastIndex(firstLine, "*")
	if endIdx <= 3 {
		return rawText, "", "", false
	}
	senderName := firstLine[3:endIdx]

	// If senderName looks like a user ID (e.g. U12345678), try to resolve it
	if (strings.HasPrefix(senderName, "U") || strings.HasPrefix(senderName, "W")) && len(senderName) >= 8 && len(senderName) <= 12 {
		if cachedName, _ := p.getUserNameFromCache(senderName); cachedName != "" {
			senderName = cachedName
		} else if p.client != nil {
			if user, err := p.client.GetUserInfo(senderName); err == nil && user != nil {
				senderName = getUserDisplayName(user)
			}
		}
	}

	var quoteLines []string
	var textLines []string
	inQuote := true

	for _, line := range lines[1:] {
		if inQuote && strings.HasPrefix(line, "> ") {
			quoteLines = append(quoteLines, strings.TrimPrefix(line, "> "))
		} else if inQuote && line == ">" {
			quoteLines = append(quoteLines, "")
		} else {
			inQuote = false
			textLines = append(textLines, line)
		}
	}

	if len(quoteLines) == 0 {
		return rawText, "", "", false
	}

	qBody := strings.Join(quoteLines, "\n")
	cText := strings.TrimSpace(strings.Join(textLines, "\n"))
	return cText, senderName, qBody, true
}

// SendReply sends a quoted reply in the main channel (not as a Slack thread reply).
// Slack has no native inline quote, so we format the original message as a block quote.
func (p *SlackProvider) SendReply(conversationID string, text string, quotedMessageID string) (*models.Message, error) {
	var prefix string
	var quotedMsg *models.Message
	var quotedSenderName string
	var quotedBody string

	if db.DB != nil {
		var quoted models.Message
		if err := db.DB.Where("protocol_msg_id = ?", quotedMessageID).First(&quoted).Error; err == nil {
			quotedMsg = &quoted
			quotedSenderName = quoted.SenderName
			if quotedSenderName == "" || quotedSenderName == quoted.SenderID {
				if cachedName, _ := p.getUserNameFromCache(quoted.SenderID); cachedName != "" {
					quotedSenderName = cachedName
				} else if p.client != nil {
					if user, err := p.client.GetUserInfo(quoted.SenderID); err == nil && user != nil {
						quotedSenderName = getUserDisplayName(user)
					} else {
						quotedSenderName = quoted.SenderID
					}
				} else {
					quotedSenderName = quoted.SenderID
				}
			}
			quotedBody = quoted.Body
			if quotedBody == "" && quoted.Attachments != "" {
				quotedBody = "📎 Attachment"
			}
			// Prefix every line with "> " so Slack renders it as a block quote
			lines := strings.Split(quotedBody, "\n")
			for i, l := range lines {
				lines[i] = "> " + l
			}
			prefix = fmt.Sprintf("> *%s*\n%s\n", quotedSenderName, strings.Join(lines, "\n"))
		}
	}
	sentMessage, err := p.SendMessage(conversationID, prefix+text, nil, nil)
	if err != nil {
		return nil, err
	}
	if sentMessage != nil {
		// Set clean body for Loom (without raw blockquote prefix)
		sentMessage.Body = text
		if quotedMsg != nil {
			sentMessage.QuotedMessageID = &quotedMessageID
			sentMessage.QuotedSenderID = &quotedMsg.SenderID
			sentMessage.QuotedBody = &quotedBody
			sentMessage.QuotedSenderName = quotedSenderName

			if db.DB != nil && sentMessage.ProtocolMsgID != "" {
				db.DB.Model(&models.Message{}).
					Where("protocol_msg_id = ?", sentMessage.ProtocolMsgID).
					Updates(map[string]interface{}{
						"body":               text,
						"quoted_message_id":  quotedMessageID,
						"quoted_sender_id":   quotedMsg.SenderID,
						"quoted_body":        quotedBody,
						"quoted_sender_name": quotedSenderName,
					})
			}
		}
	}
	return sentMessage, nil
}

// SendThreadReply sends a quoted reply to a specific message inside a Slack thread.
func (p *SlackProvider) SendThreadReply(conversationID string, text string, threadID string, quotedMessageID string) (*models.Message, error) {
	var prefix string
	var quotedMsg *models.Message
	var quotedSenderName string
	var quotedBody string

	if db.DB != nil {
		var quoted models.Message
		if err := db.DB.Where("protocol_msg_id = ?", quotedMessageID).First(&quoted).Error; err == nil {
			quotedMsg = &quoted
			quotedSenderName = quoted.SenderName
			if quotedSenderName == "" || quotedSenderName == quoted.SenderID {
				if cachedName, _ := p.getUserNameFromCache(quoted.SenderID); cachedName != "" {
					quotedSenderName = cachedName
				} else if p.client != nil {
					if user, err := p.client.GetUserInfo(quoted.SenderID); err == nil && user != nil {
						quotedSenderName = getUserDisplayName(user)
					} else {
						quotedSenderName = quoted.SenderID
					}
				} else {
					quotedSenderName = quoted.SenderID
				}
			}
			quotedBody = quoted.Body
			if quotedBody == "" && quoted.Attachments != "" {
				quotedBody = "📎 Attachment"
			}
			lines := strings.Split(quotedBody, "\n")
			for i, l := range lines {
				lines[i] = "> " + l
			}
			prefix = fmt.Sprintf("> *%s*\n%s\n", quotedSenderName, strings.Join(lines, "\n"))
		}
	}
	sentMessage, err := p.SendMessage(conversationID, prefix+text, nil, &threadID)
	if err != nil {
		return nil, err
	}
	if sentMessage != nil {
		sentMessage.Body = text
		sentMessage.ThreadID = &threadID
		if quotedMsg != nil {
			sentMessage.QuotedMessageID = &quotedMessageID
			sentMessage.QuotedSenderID = &quotedMsg.SenderID
			sentMessage.QuotedBody = &quotedBody
			sentMessage.QuotedSenderName = quotedSenderName

			if db.DB != nil && sentMessage.ProtocolMsgID != "" {
				db.DB.Model(&models.Message{}).
					Where("protocol_msg_id = ?", sentMessage.ProtocolMsgID).
					Updates(map[string]interface{}{
						"body":               text,
						"thread_id":          threadID,
						"quoted_message_id":  quotedMessageID,
						"quoted_sender_id":   quotedMsg.SenderID,
						"quoted_body":        quotedBody,
						"quoted_sender_name": quotedSenderName,
					})
			}
		}
	}
	return sentMessage, nil
}

// SendFile sends a file to a given conversation without text.
func (p *SlackProvider) SendFile(conversationID string, file *core.Attachment, threadID *string) (*models.Message, error) {
	// Normalize conversation ID early to ensure consistency
	normalizedConversationID := p.normalizeDMConversationID(core.StripConvID(conversationID))
	nsConvID := core.BuildConvID(p.getInstanceId(), normalizedConversationID)

	p.mu.RLock()
	client := p.client
	eventChan := p.eventChan
	p.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	// Handle different ID types for Slack conversations (user ID -> channel ID for DMs)
	actualChannelID := normalizedConversationID
	if len(normalizedConversationID) > 0 && normalizedConversationID[0] == 'U' {
		// Open DM to get channel ID
		channel, _, _, err := client.OpenConversation(&slack.OpenConversationParameters{
			Users:    []string{normalizedConversationID},
			ReturnIM: true,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to open DM conversation: %w", err)
		}
		if channel == nil || channel.ID == "" {
			return nil, fmt.Errorf("failed to get DM channel ID for user %s", normalizedConversationID)
		}
		actualChannelID = channel.ID
	}

	params := slack.UploadFileV2Parameters{
		Channel:  actualChannelID,
		Reader:   bytes.NewReader(file.Data),
		FileSize: file.FileSize,
		Filename: file.FileName,
	}
	if threadID != nil {
		params.ThreadTimestamp = *threadID
	}

	fileUpload, err := client.UploadFileV2(params)
	if err != nil {
		return nil, err
	}

	// Get current user info for sender details
	senderID := p.selfUserID
	if senderID == "" {
		authTest, err := client.AuthTest()
		if err == nil && authTest != nil {
			senderID = authTest.UserID
			p.mu.Lock()
			p.selfUserID = senderID
			p.mu.Unlock()
		}
	}
	senderName, senderAvatarURL := p.resolveUserInfo(senderID)

	// UploadFileV2 returns a generic File object, not a message ts.
	// We need to fetch the message that was created by the file upload to get its timestamp.
	// This prevents duplication when the real message event arrives via socket/polling.
	var protocolMsgID = fileUpload.ID // Fallback
	var timestamp = time.Now()

	// Poll for the message containing this file
	// Retry up to 5 times with short delays
	for i := 0; i < 5; i++ {
		time.Sleep(500 * time.Millisecond)
		historyParams := &slack.GetConversationHistoryParameters{
			ChannelID: actualChannelID,
			Limit:     10, // Check last 10 messages
		}
		history, err := client.GetConversationHistory(historyParams)
		if err != nil {
			p.log("SlackProvider.SendFile: Error fetching history to find file message: %v\n", err)
			continue
		}

		found := false
		for _, msg := range history.Messages {
			// Check if this message contains our file
			for _, f := range msg.Files {
				if f.ID == fileUpload.ID {
					protocolMsgID = msg.Timestamp
					ts := parseSlackTimestamp(msg.Timestamp)
					if !ts.IsZero() {
						timestamp = ts
					}
					found = true
					break
				}
			}
			if found {
				break
			}
		}

		if found {
			p.log("SlackProvider.SendFile: Found real message timestamp %s for file %s\n", protocolMsgID, fileUpload.ID)
			break
		}
	}

	// Determine attachment type from MIME type
	attachmentType := "document"
	if strings.HasPrefix(file.MimeType, "image/") {
		attachmentType = "image"
	} else if strings.HasPrefix(file.MimeType, "video/") {
		attachmentType = "video"
	} else if strings.HasPrefix(file.MimeType, "audio/") {
		// Check if it's a voice message (small audio files)
		if file.FileSize < 5*1024*1024 { // Less than 5MB
			attachmentType = "voice"
		} else {
			attachmentType = "audio"
		}
	} else if file.MimeType == "application/pdf" {
		attachmentType = "document"
	}

	// For now, create a message with file info
	// Note: We don't have a URL yet since the file was just uploaded
	// The URL will be available when we fetch the message via polling
	attachments := []models.Attachment{
		{
			Type:     attachmentType,
			FileName: file.FileName,
			FileSize: int64(file.FileSize),
			MimeType: file.MimeType,
		},
	}
	attachmentsJSON, _ := json.Marshal(attachments)

	sentMessage := &models.Message{
		ProtocolMsgID:   protocolMsgID,
		ProtocolConvID:  nsConvID,
		SenderID:        senderID,
		SenderName:      senderName,
		SenderAvatarURL: senderAvatarURL,
		Body:            "",
		Attachments:     string(attachmentsJSON),
		Timestamp:       timestamp,
		IsFromMe:        true,
	}
	if threadID != nil {
		sentMessage.ThreadID = threadID
	}

	// Store message in database — use upsert to avoid duplicating a record that
	// the RTM event handler may have already inserted concurrently.
	if db.DB != nil {
		convID, err := p.ensureConversation(normalizedConversationID)
		if err != nil {
			p.log("SlackProvider.SendFile: Failed to ensure conversation: %v\n", err)
		} else {
			sentMessage.ConversationID = convID
		}

		var existing models.Message
		if err := db.DB.Where("protocol_msg_id = ? AND protocol_conv_id = ?", protocolMsgID, nsConvID).First(&existing).Error; err != nil {
			// Not yet stored — create it.
			if err := db.DB.Create(sentMessage).Error; err != nil {
				p.log("SlackProvider.SendFile: Failed to store message in database: %v\n", err)
			}
		} else {
			// RTM already stored a record with this ID — merge our attachment data in.
			sentMessage.ID = existing.ID
			if existing.Attachments == "" {
				existing.Attachments = string(attachmentsJSON)
			}
			if err := db.DB.Save(&existing).Error; err != nil {
				p.log("SlackProvider.SendFile: Failed to update existing message: %v\n", err)
			}
			sentMessage.ID = existing.ID
		}
	}

	// Emit MessageEvent to notify frontend
	if eventChan != nil {
		select {
		case eventChan <- core.MessageEvent{InstanceID: p.getInstanceId(), Message: *sentMessage}:
			p.log("SlackProvider.SendFile: MessageEvent emitted successfully for sent file %s\n", fileUpload.ID)
		default:
			p.log("SlackProvider.SendFile: WARNING - Failed to emit MessageEvent (channel full) for sent file %s\n", fileUpload.ID)
		}
	}

	return sentMessage, nil
}

// GetConversationHistory retrieves the message history for a specific conversation.
// It first checks the database, and if not enough messages are found, fetches from Slack API and stores them.
// beforeTimestamp: if set, fetch messages before this timestamp (for pagination)
// sinceTimestamp: if set, fetch messages since this timestamp (for sync)
func (p *SlackProvider) GetConversationHistory(conversationID string, limit int, beforeTimestamp *time.Time, sinceTimestamp *time.Time) ([]models.Message, error) {
	if conversationID == "" {
		return []models.Message{}, fmt.Errorf("conversation ID is required")
	}

	// rawConvID is used for Slack API calls; nsConvID is used for DB queries.
	rawConvID := p.normalizeDMConversationID(core.StripConvID(conversationID))
	nsConvID := core.BuildConvID(p.getInstanceId(), rawConvID)

	// Default limit to 20 if not specified
	if limit <= 0 {
		limit = 20
	}

	// First, try to load from database
	if db.DB != nil {
		var dbMessages []models.Message
		query := db.DB.Where("protocol_conv_id = ?", nsConvID)

		// IMPORTANT: Only get main messages (not thread replies)
		// In Slack:
		// - Main message with no thread: thread_id IS NULL or ''
		// - Main message of a thread: thread_id = protocol_msg_id (parent message references itself)
		// - Reply in a thread: thread_id = parent's protocol_msg_id (different from its own ID)
		// So we want: thread_id IS NULL OR thread_id = '' OR thread_id = protocol_msg_id
		query = query.Where("thread_id IS NULL OR thread_id = '' OR thread_id = protocol_msg_id")

		// If beforeTimestamp is specified, only get messages before that timestamp
		if beforeTimestamp != nil {
			query = query.Where("timestamp < ?", *beforeTimestamp)
		}
		// If sinceTimestamp is specified, only get messages strictly after that timestamp
		// Add 1ms to avoid getting the last message we already have
		if sinceTimestamp != nil {
			excludeTimestamp := sinceTimestamp.Add(1 * time.Millisecond)
			query = query.Where("timestamp > ?", excludeTimestamp)
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
			// If sinceTimestamp is set (sync), we still fetch from API to ensure we have latest
			if (beforeTimestamp != nil || len(dbMessages) >= limit) && sinceTimestamp == nil {
				p.log("SlackProvider.GetConversationHistory: Loaded %d messages from database for conversation %s\n", len(dbMessages), conversationID)
				return dbMessages, nil
			}
		}
	}

	// Not enough messages in database or beforeTimestamp/sinceTimestamp specified, fetch from Slack API

	// Compute self name before acquiring the lock so we can detect corruption in convertMessage.
	p.mu.RLock()
	selfUserIDForCheck := p.selfUserID
	p.mu.RUnlock()
	selfNameForCheck, _ := p.resolveUserInfo(selfUserIDForCheck)

	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	// Handle different ID types for Slack conversations
	actualChannelID := rawConvID

	// If rawConvID is a user ID (starts with "U"), we need to open the DM conversation
	// to get the actual channel ID (which starts with "D")
	if len(rawConvID) > 0 && rawConvID[0] == 'U' {
		// Open the DM conversation with this user to get the channel ID
		channel, _, _, err := p.client.OpenConversation(&slack.OpenConversationParameters{
			Users:    []string{rawConvID},
			ReturnIM: true,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to open DM conversation with user %s: %w", rawConvID, err)
		}
		if channel == nil || channel.ID == "" {
			return nil, fmt.Errorf("failed to get DM channel ID for user %s", rawConvID)
		}
		actualChannelID = channel.ID
		p.log("SlackProvider.GetConversationHistory: Opened DM conversation, user ID %s -> channel ID %s\n", rawConvID, actualChannelID)
	} else if len(rawConvID) > 0 && rawConvID[0] == 'D' {
		// For DM channel IDs, ensure the conversation is open
		// This is required before we can retrieve message history
		_, _, _, err := p.client.OpenConversation(&slack.OpenConversationParameters{
			ChannelID: rawConvID,
		})
		if err != nil {
			// Log but don't fail - the conversation might already be open
			p.log("SlackProvider.GetConversationHistory: Warning - failed to open DM conversation %s: %v (may already be open)\n", rawConvID, err)
		}
	}

	params := &slack.GetConversationHistoryParameters{
		ChannelID: actualChannelID,
		Limit:     limit,
	}
	if beforeTimestamp != nil {
		// Latest: fetch messages before this timestamp
		params.Latest = fmt.Sprintf("%f", float64(beforeTimestamp.Unix()))
	}
	if sinceTimestamp != nil {
		// Oldest: fetch messages since this timestamp
		// Add a small epsilon (1 millisecond) to ensure we get messages strictly after the timestamp
		// This avoids fetching the last message we already have
		oldestTime := sinceTimestamp.Add(1 * time.Millisecond)
		// Slack API expects Unix timestamp in seconds with decimal precision
		// Convert to Unix timestamp (seconds since epoch) with nanosecond precision
		unixSeconds := float64(oldestTime.Unix()) + float64(oldestTime.Nanosecond())/1e9
		params.Oldest = fmt.Sprintf("%.6f", unixSeconds)
		params.Inclusive = false // Don't include messages with timestamp equal to Oldest
		p.log("SlackProvider.GetConversationHistory: Fetching messages since %s (Oldest=%s, Inclusive=false)\n",
			sinceTimestamp.Format(time.RFC3339Nano), params.Oldest)
	}

	history, err := p.client.GetConversationHistory(params)
	if err != nil {
		p.log("SlackProvider.GetConversationHistory: API call failed for %s: %v\n", conversationID, err)
		return nil, err
	}

	if sinceTimestamp != nil {
		p.log("SlackProvider.GetConversationHistory: API returned %d messages for %s (sinceTimestamp=%s, Oldest=%s)\n",
			len(history.Messages), conversationID, sinceTimestamp.Format(time.RFC3339Nano), params.Oldest)
		if len(history.Messages) > 0 {
			// Log first message timestamp to see what we got
			firstMsgTS := parseSlackTimestamp(history.Messages[0].Timestamp)
			p.log("SlackProvider.GetConversationHistory: First message timestamp: %s\n", firstMsgTS.Format(time.RFC3339Nano))
		}
	}

	var messages []models.Message
	var threadMessages []models.Message       // Store thread messages separately
	threadTimestamps := make(map[string]bool) // Track which messages have threads

	for _, msg := range history.Messages {
		convertedMsg := p.convertMessage(msg, actualChannelID)

		// Detect corruption: a non-self sender resolved to our own name via the DB cache.
		// p.client is stable here (we hold the read lock), so we call the API directly.
		if !convertedMsg.IsFromMe && selfNameForCheck != "" && convertedMsg.SenderName == selfNameForCheck && convertedMsg.SenderID != "" {
			if user, err := p.client.GetUserInfo(convertedMsg.SenderID); err == nil && user != nil {
				name := user.RealName
				if name == "" {
					name = user.Profile.DisplayName
				}
				if name == "" {
					name = user.Name
				}
				if name != "" && name != convertedMsg.SenderID {
					convertedMsg.SenderName = name
					if user.Profile.Image512 != "" {
						convertedMsg.SenderAvatarURL = user.Profile.Image512
					} else if user.Profile.Image48 != "" {
						convertedMsg.SenderAvatarURL = user.Profile.Image48
					}
					// Persist asynchronously to avoid recursive mutex acquisition.
					senderID := convertedMsg.SenderID
					go p.saveUserNameToCache(senderID, name, convertedMsg.SenderAvatarURL)
				}
			}
		}

		messages = append(messages, convertedMsg)

		// Check if this message has a thread (has replies)
		if msg.ReplyCount > 0 && msg.Timestamp != "" {
			threadTimestamps[msg.Timestamp] = true
		}
	}

	// Fetch thread replies for messages that have threads
	// These will be stored in DB but NOT returned in main message list
	for threadTS := range threadTimestamps {
		replies, err := p.getThreadReplies(actualChannelID, threadTS)
		if err != nil {
			p.log("SlackProvider.GetConversationHistory: Failed to get thread replies for %s: %v\n", threadTS, err)
			continue
		}
		threadMessages = append(threadMessages, replies...)
		p.log("SlackProvider.GetConversationHistory: Fetched %d thread replies for message %s\n", len(replies), threadTS)
	}

	// Reverse to oldest first
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	// Store main messages in database
	if len(messages) > 0 {
		if sinceTimestamp != nil {
			p.log("SlackProvider.GetConversationHistory: Storing %d new messages for %s (sync mode)\n", len(messages), nsConvID)
		}
		p.storeMessagesForConversation(nsConvID, messages)
	} else if sinceTimestamp != nil {
		p.log("SlackProvider.GetConversationHistory: No messages to store for %s (sync mode, last timestamp: %s)\n",
			nsConvID, sinceTimestamp.Format(time.RFC3339Nano))
	}

	// Store thread messages separately (they won't be returned in main list)
	if len(threadMessages) > 0 {
		p.log("SlackProvider.GetConversationHistory: Storing %d thread messages for conversation %s\n", len(threadMessages), nsConvID)
		p.storeMessagesForConversation(nsConvID, threadMessages)
	}

	return messages, nil
}

// getThreadReplies retrieves all messages in a thread.
// Pass an optional oldest time to fetch only replies strictly after that timestamp.
func (p *SlackProvider) getThreadReplies(channelID string, threadTS string, oldest ...time.Time) ([]models.Message, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	var allThreadMessages []models.Message
	cursor := ""

	for {
		params := &slack.GetConversationRepliesParameters{
			ChannelID: channelID,
			Timestamp: threadTS,
		}
		if len(oldest) > 0 && !oldest[0].IsZero() {
			// Add 1ms so we get only replies strictly after the last stored reply
			sinceTS := oldest[0].Add(time.Millisecond)
			params.Oldest = fmt.Sprintf("%.6f", float64(sinceTS.Unix())+float64(sinceTS.Nanosecond())/1e9)
			params.Inclusive = false
		}
		if cursor != "" {
			params.Cursor = cursor
		}

		messages, hasMore, nextCursor, err := p.client.GetConversationReplies(params)
		if err != nil {
			return nil, fmt.Errorf("failed to get thread replies: %w", err)
		}

		// Convert thread messages (skip the first one as it's the parent message)
		for i, msg := range messages {
			// Skip the parent message (first one) as it's already in the main messages
			if i == 0 {
				continue
			}
			convertedMsg := p.convertMessage(msg, channelID)
			// Ensure ThreadID is set
			if convertedMsg.ThreadID == nil {
				convertedMsg.ThreadID = &threadTS
			}
			allThreadMessages = append(allThreadMessages, convertedMsg)
		}

		if !hasMore {
			break
		}
		cursor = nextCursor
	}

	return allThreadMessages, nil
}

// storeMessagesForConversation stores messages in the database for a conversation
func (p *SlackProvider) storeMessagesForConversation(convID string, messages []models.Message) int {
	if convID == "" || len(messages) == 0 {
		return 0
	}

	// Normalize DM conversation IDs to ensure consistency (resolve channel IDs to User IDs)
	rawConvID := p.normalizeDMConversationID(core.StripConvID(convID))
	normalizedConvID := core.BuildConvID(p.getInstanceId(), rawConvID)
	if normalizedConvID != convID {
		p.log("SlackProvider.storeMessagesForConversation: Normalized conversation ID from %s to %s\n", convID, normalizedConvID)
	}

	// Persist messages to database
	if db.DB != nil {
		// Ensure Conversation exists and get ConversationID
		conversationID, err := p.ensureConversation(rawConvID)
		if err != nil {
			p.log("SlackProvider.storeMessagesForConversation: Failed to ensure conversation: %v\n", err)
			// Continue anyway, ConversationID will be 0
		}

		// Optimize: batch check which messages already exist
		protocolMsgIDs := make([]string, 0, len(messages))
		for _, msg := range messages {
			if msg.ProtocolMsgID != "" {
				protocolMsgIDs = append(protocolMsgIDs, msg.ProtocolMsgID)
			}
		}

		// Batch query existing messages
		var existingMessages []models.Message
		existingMap := make(map[string]models.Message)
		if len(protocolMsgIDs) > 0 {
			db.DB.Where("protocol_msg_id IN ?", protocolMsgIDs).Find(&existingMessages)
			for _, msg := range existingMessages {
				existingMap[msg.ProtocolMsgID] = msg
			}
		}

		// Separate new messages from updates
		newMessages := make([]models.Message, 0)
		updateMessages := make([]models.Message, 0)
		seenMsgIDs := make(map[string]bool) // Track message IDs to avoid duplicates

		for _, msg := range messages {
			if msg.ProtocolMsgID == "" {
				continue
			}

			// Skip if we've already processed this message ID in this batch
			if seenMsgIDs[msg.ProtocolMsgID] {
				p.log("SlackProvider.storeMessagesForConversation: SKIPPING duplicate message in batch (ProtocolMsgID: %s)\n", msg.ProtocolMsgID)
				continue
			}
			seenMsgIDs[msg.ProtocolMsgID] = true

			msg.ProtocolConvID = normalizedConvID // Use normalized ID for consistency
			msg.ConversationID = conversationID

			// If this message indicates a huddle ended, try to find and update the previous incoming_call message
			if msg.CallType == "missed_voice" || msg.CallType == "missed_group_voice" {
				// Look for an existing incoming_call or incoming_group_call message in this conversation
				var existingCallMsg models.Message
				callTypeToFind := "incoming_call"
				if msg.CallType == "missed_group_voice" {
					callTypeToFind = "incoming_group_call"
				}

				if err := db.DB.Where("protocol_conv_id = ? AND call_type = ?", normalizedConvID, callTypeToFind).
					Order("timestamp DESC").
					First(&existingCallMsg).Error; err == nil {
					// Found existing call message, update it instead of creating a new one
					existingCallMsg.CallType = msg.CallType
					existingCallMsg.Timestamp = msg.Timestamp // Update to end time
					updateMessages = append(updateMessages, existingCallMsg)
					p.log("SlackProvider.storeMessagesForConversation: Found existing call message %s, will update it to %s\n", existingCallMsg.ProtocolMsgID, msg.CallType)
					// Skip creating a new message for the huddle end
					continue
				}
			}

			if existingMsg, exists := existingMap[msg.ProtocolMsgID]; exists {
				// Message exists, update it
				msg.ID = existingMsg.ID
				// Slack has no native quoted-reply fields. During a later sync it
				// returns only the Markdown block quote (or, after an edit, no quote
				// information at all). Never let that incomplete payload erase the
				// metadata stored by Loom, otherwise the UI flips between a reply card
				// and raw `>` lines on successive refetches.
				if existingMsg.QuotedMessageID != nil &&
					(msg.QuotedMessageID == nil || strings.HasSuffix(*msg.QuotedMessageID, "-quote")) {
					msg.QuotedMessageID = existingMsg.QuotedMessageID
					msg.QuotedSenderID = existingMsg.QuotedSenderID
					msg.QuotedSenderName = existingMsg.QuotedSenderName
					msg.QuotedBody = existingMsg.QuotedBody
				}
				// Preserve existing attachments if new ones are empty (to avoid overwriting with empty)
				// But if new attachments exist, merge them with existing ones to avoid losing data
				if msg.Attachments == "" && existingMsg.Attachments != "" {
					msg.Attachments = existingMsg.Attachments
				} else if msg.Attachments != "" && existingMsg.Attachments != "" {
					// Merge attachments: combine existing and new, removing duplicates
					mergedAttachments := p.mergeAttachments(existingMsg.Attachments, msg.Attachments)
					msg.Attachments = mergedAttachments
				}
				// Preserve CallType if updating and new one is empty
				if msg.CallType == "" && existingMsg.CallType != "" {
					msg.CallType = existingMsg.CallType
				}
				// Preserve body if the new event carries none (e.g. RTM file_share has empty text)
				if msg.Body == "" && existingMsg.Body != "" {
					msg.Body = existingMsg.Body
				}
				// Preserve ThreadID: RTM/socket echoes arrive without thread_ts, which would
				// overwrite the correct value set by SendMessage and erase the thread association.
				if msg.ThreadID == nil && existingMsg.ThreadID != nil {
					msg.ThreadID = existingMsg.ThreadID
				}
				updateMessages = append(updateMessages, msg)
			} else {
				newMessages = append(newMessages, msg)
			}
		}

		// Batch insert new messages
		if len(newMessages) > 0 {
			// Use CreateInBatches for better performance
			batchSize := 100
			if err := db.DB.CreateInBatches(newMessages, batchSize).Error; err != nil {
				p.log("SlackProvider.storeMessagesForConversation: Failed to batch insert %d messages: %v\n", len(newMessages), err)
				// Fallback to individual inserts
				for _, msg := range newMessages {
					if err := db.DB.Create(&msg).Error; err != nil {
						p.log("SlackProvider.storeMessagesForConversation: Failed to persist message %s: %v\n", msg.ProtocolMsgID, err)
					}
				}
			} else {
				p.log("SlackProvider.storeMessagesForConversation: Batch inserted %d new messages for conversation %s\n", len(newMessages), convID)
			}
		}

		// Batch update existing messages
		for _, msg := range updateMessages {
			// Reactions returned by Slack have no local IDs. Letting GORM save this
			// association recreates every existing reaction on each history sync,
			// assigns it a fresh CreatedAt, and falsely makes the conversation recent.
			// Reaction add/remove events are persisted through their dedicated path.
			if err := db.DB.Omit("Reactions").Save(&msg).Error; err != nil {
				p.log("SlackProvider.storeMessagesForConversation: Failed to update message %s: %v\n", msg.ProtocolMsgID, err)
			}
		}

		if len(newMessages) > 0 || len(updateMessages) > 0 {
			p.log("SlackProvider.storeMessagesForConversation: Stored %d new, %d updated messages for conversation %s (normalized: %s)\n", len(newMessages), len(updateMessages), convID, normalizedConvID)
		}
	}

	return len(messages)
}

// lookbackSyncConversation fetches messages from the Slack API in the half-open window
// (since, before) and returns only those that were absent from the local database —
// i.e. messages that were missed during the forward-only incremental sync because they
// were delivered to (and read by) another client before this sync ran.
func (p *SlackProvider) lookbackSyncConversation(convID string, before, since time.Time) ([]models.Message, error) {
	if db.DB == nil {
		return nil, nil
	}

	// Snapshot ProtocolMsgIDs already stored for this window so we can identify newcomers.
	normalizedID := p.normalizeDMConversationID(convID)
	var existingIDs []string
	db.DB.Model(&models.Message{}).
		Where("protocol_conv_id = ? AND timestamp > ? AND timestamp < ? AND (thread_id IS NULL OR thread_id = '' OR thread_id = protocol_msg_id)",
			normalizedID, since, before).
		Pluck("protocol_msg_id", &existingIDs)
	existingSet := make(map[string]bool, len(existingIDs))
	for _, id := range existingIDs {
		existingSet[id] = true
	}

	// Call the API for the window. GetConversationHistory stores any missing messages in DB.
	allMessages, err := p.GetConversationHistory(convID, 1000, &before, &since)
	if err != nil {
		return nil, err
	}

	// Return only messages that were not in the DB before this call.
	var missed []models.Message
	for _, msg := range allMessages {
		if !existingSet[msg.ProtocolMsgID] {
			missed = append(missed, msg)
		}
	}
	return missed, nil
}

// attachmentFromSlackFile converts a slack.File into a models.Attachment.
// Used both by SendFile (after polling) and handleRTMMessageEvent (file_share events).
func attachmentFromSlackFile(f slack.File) (models.Attachment, bool) {
	if f.URLPrivate == "" {
		return models.Attachment{}, false
	}
	attachmentType := "document"
	switch {
	case strings.HasPrefix(f.Mimetype, "image/"):
		attachmentType = "image"
	case strings.HasPrefix(f.Mimetype, "video/"):
		attachmentType = "video"
	case strings.HasPrefix(f.Mimetype, "audio/"):
		if f.Size < 5*1024*1024 {
			attachmentType = "voice"
		} else {
			attachmentType = "audio"
		}
	case f.Mimetype == "application/pdf":
		attachmentType = "document"
	}

	thumbnail := ""
	if attachmentType == "image" || attachmentType == "video" {
		switch {
		case f.Thumb480 != "":
			thumbnail = f.Thumb480
		case f.Thumb360 != "":
			thumbnail = f.Thumb360
		case f.Thumb160 != "":
			thumbnail = f.Thumb160
		}
	}

	return models.Attachment{
		Type:      attachmentType,
		URL:       f.URLPrivate,
		Thumbnail: thumbnail,
		FileName:  f.Name,
		FileSize:  int64(f.Size),
		MimeType:  f.Mimetype,
	}, true
}

// mergeAttachments combines existing and new attachments, removing duplicates by URL.
// If all existing attachments are placeholders (empty URL) and the new ones have real
// URLs, the new list replaces the old one rather than being appended.
func (p *SlackProvider) mergeAttachments(existingJSON, newJSON string) string {
	if existingJSON == "" {
		return newJSON
	}
	if newJSON == "" {
		return existingJSON
	}

	var existingAttachments, newAttachments []models.Attachment

	if err := json.Unmarshal([]byte(existingJSON), &existingAttachments); err != nil {
		p.log("SlackProvider.mergeAttachments: Failed to parse existing attachments: %v\n", err)
		return newJSON
	}
	if err := json.Unmarshal([]byte(newJSON), &newAttachments); err != nil {
		p.log("SlackProvider.mergeAttachments: Failed to parse new attachments: %v\n", err)
		return existingJSON
	}

	// If existing entries are all URL-less placeholders (e.g. from SendFile before the RTM
	// event arrives) and the new entries carry real URLs, replace rather than append.
	allExistingEmpty := len(existingAttachments) > 0
	for _, a := range existingAttachments {
		if a.URL != "" {
			allExistingEmpty = false
			break
		}
	}
	anyNewHasURL := false
	for _, a := range newAttachments {
		if a.URL != "" {
			anyNewHasURL = true
			break
		}
	}
	if allExistingEmpty && anyNewHasURL {
		merged, err := json.Marshal(newAttachments)
		if err == nil {
			return string(merged)
		}
	}

	// Normal dedup-by-URL merge.
	seenURLs := make(map[string]bool)
	mergedAttachments := make([]models.Attachment, 0, len(existingAttachments)+len(newAttachments))
	for _, att := range existingAttachments {
		if !seenURLs[att.URL] {
			seenURLs[att.URL] = true
			mergedAttachments = append(mergedAttachments, att)
		}
	}
	for _, att := range newAttachments {
		if !seenURLs[att.URL] {
			seenURLs[att.URL] = true
			mergedAttachments = append(mergedAttachments, att)
		}
	}

	mergedJSON, err := json.Marshal(mergedAttachments)
	if err == nil {
		p.log("SlackProvider.mergeAttachments: Merged %d existing + %d new = %d unique attachments\n",
			len(existingAttachments), len(newAttachments), len(mergedAttachments))
		return string(mergedJSON)
	}
	p.log("SlackProvider.mergeAttachments: Failed to serialize merged attachments: %v\n", err)
	return existingJSON
}

// GetThreads loads all messages in a discussion thread.
func (p *SlackProvider) GetThreads(parentMessageID string) ([]models.Message, error) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	actualChannelID := ""
	convID := ""
	if db.DB != nil {
		var parentMsg models.Message
		if err := db.DB.Where("protocol_msg_id = ?", parentMessageID).First(&parentMsg).Error; err == nil {
			convID = parentMsg.ProtocolConvID // namespaced — used for storeMessagesForConversation
			actualChannelID = core.StripConvID(parentMsg.ProtocolConvID)
		}
	}

	if actualChannelID == "" {
		return []models.Message{}, nil
	}

	// Resolve user ID → actual DM channel ID
	if len(actualChannelID) > 0 && actualChannelID[0] == 'U' {
		channel, _, _, err := client.OpenConversation(&slack.OpenConversationParameters{
			Users:    []string{actualChannelID},
			ReturnIM: true,
		})
		if err == nil && channel != nil && channel.ID != "" {
			actualChannelID = channel.ID
		}
	}

	replies, err := p.getThreadReplies(actualChannelID, parentMessageID)
	if err != nil {
		return nil, err
	}

	if len(replies) > 0 && convID != "" {
		p.storeMessagesForConversation(convID, replies)
	}

	return replies, nil
}

// EditMessage edits an existing message.
func (p *SlackProvider) EditMessage(conversationID string, messageID string, newText string) (*models.Message, error) {
	canonicalText := newText
	providerText := messageformat.Slack(newText)
	p.mu.RLock()
	client := p.client
	eventChan := p.eventChan
	p.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	rawConvID := core.StripConvID(conversationID)
	nsConvID := core.BuildConvID(p.getInstanceId(), p.normalizeDMConversationID(rawConvID))

	// Handle different ID types for Slack conversations (user ID -> channel ID for DMs)
	actualChannelID := rawConvID
	if len(rawConvID) > 0 && rawConvID[0] == 'U' {
		// Open DM to get channel ID
		channel, _, _, err := client.OpenConversation(&slack.OpenConversationParameters{
			Users:    []string{rawConvID},
			ReturnIM: true,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to open DM conversation: %w", err)
		}
		if channel != nil && channel.ID != "" {
			actualChannelID = channel.ID
		}
	}

	_, timestamp, _, err := client.UpdateMessage(actualChannelID, messageID, slack.MsgOptionText(providerText, false))
	if err != nil {
		return nil, err
	}

	// Get existing message from database to preserve other fields
	var existingMsg models.Message
	if db.DB != nil {
		if err := db.DB.Where("protocol_msg_id = ?", messageID).First(&existingMsg).Error; err != nil {
			// Message not found in database, create a new one
			ts := parseSlackTimestamp(timestamp)
			existingMsg = models.Message{
				ProtocolMsgID:  messageID,
				ProtocolConvID: nsConvID,
				Body:           canonicalText,
				Timestamp:      ts,
				IsFromMe:       true,
			}
		} else {
			// Update existing message
			existingMsg.Body = canonicalText
			existingMsg.IsEdited = true
			if timestamp != "" {
				existingMsg.Timestamp = parseSlackTimestamp(timestamp)
			}
		}
	} else {
		// No database, create minimal message
		ts := parseSlackTimestamp(timestamp)
		existingMsg = models.Message{
			ProtocolMsgID:  messageID,
			ProtocolConvID: nsConvID,
			Body:           canonicalText,
			Timestamp:      ts,
			IsFromMe:       true,
			IsEdited:       true,
		}
	}

	// Update message in database
	if db.DB != nil {
		if err := db.DB.Save(&existingMsg).Error; err != nil {
			p.log("SlackProvider.EditMessage: Failed to update message in database: %v\n", err)
		} else {
			p.log("SlackProvider.EditMessage: Updated message %s in database\n", messageID)
		}
	}

	// Emit MessageEvent to notify frontend of the update
	if eventChan != nil {
		select {
		case eventChan <- core.MessageEvent{InstanceID: p.getInstanceId(), Message: existingMsg}:
			p.log("SlackProvider.EditMessage: MessageEvent emitted successfully for edited message %s\n", messageID)
		default:
			p.log("SlackProvider.EditMessage: WARNING - Failed to emit MessageEvent (channel full) for edited message %s\n", messageID)
		}
	}

	return &existingMsg, nil
}

// DeleteMessage deletes a message.
func (p *SlackProvider) DeleteMessage(conversationID string, messageID string) error {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	// A thread can be opened from a cached message while the selected contact
	// still uses an older conversation identifier (notably for DMs, which are
	// normalized from a D... channel ID to a U... user ID).  chat.delete must be
	// called with the channel that owns the message, so prefer the persisted
	// message metadata when it is available.
	if db.DB != nil {
		var storedMessage models.Message
		if err := db.DB.Where("protocol_msg_id = ?", messageID).First(&storedMessage).Error; err == nil && storedMessage.ProtocolConvID != "" {
			conversationID = storedMessage.ProtocolConvID
		}
	}

	// Strip namespace prefix before making Slack API calls
	rawConvID := core.StripConvID(conversationID)

	// Resolve user ID → actual DM channel ID (same pattern as SendMessage)
	actualChannelID := rawConvID
	if len(rawConvID) > 0 && rawConvID[0] == 'U' {
		channel, _, _, err := client.OpenConversation(&slack.OpenConversationParameters{
			Users:    []string{rawConvID},
			ReturnIM: true,
		})
		if err != nil {
			return fmt.Errorf("failed to open DM conversation: %w", err)
		}
		if channel != nil && channel.ID != "" {
			actualChannelID = channel.ID
		}
	}

	p.log("SlackProvider.DeleteMessage: deleting message %s in channel %s (original: %s)\n", messageID, actualChannelID, conversationID)
	fmt.Printf("SlackProvider.DeleteMessage: calling chat.delete for ts=%s channel=%s\n", messageID, actualChannelID)

	_, _, err := client.DeleteMessage(actualChannelID, messageID)
	if err != nil {
		p.log("SlackProvider.DeleteMessage: ERROR - %v\n", err)
		fmt.Printf("SlackProvider.DeleteMessage: ERROR - %v\n", err)
		return err
	}

	p.log("SlackProvider.DeleteMessage: message %s deleted successfully\n", messageID)
	fmt.Printf("SlackProvider.DeleteMessage: message %s deleted successfully\n", messageID)
	return nil
}

func (p *SlackProvider) convertMessage(msg slack.Message, conversationID string) models.Message {
	// Normalize conversation ID to ensure consistency, then namespace it for storage.
	// conversationID here is always a raw Slack channel/user ID (callers pass API-returned values).
	normalizedConversationID := core.BuildConvID(p.getInstanceId(), p.normalizeDMConversationID(conversationID))

	ts := parseSlackTimestamp(msg.Timestamp)

	// Get sender name and avatar
	senderName, senderAvatarURL := p.resolveUserInfo(msg.User)

	// Check if message is from me (compare with authenticated user)
	// p.selfUserID is populated at startup and read-safe here since callers hold p.mu.RLock.
	isFromMe := p.selfUserID != "" && msg.User == p.selfUserID

	// Convert Slack reactions to our Reaction model
	reactions := make([]models.Reaction, 0)
	if len(msg.Reactions) > 0 {
		for _, slackReaction := range msg.Reactions {
			// Each slack.Reaction has a Name (emoji) and Users (list of user IDs)
			// We create one Reaction per user
			for _, userID := range slackReaction.Users {
				reactions = append(reactions, models.Reaction{
					UserID: userID,
					Emoji:  slackReaction.Name,
				})
			}
		}
	}

	// Handle thread ID - if this message is a reply to a thread, set ThreadID
	var threadID *string
	if msg.ThreadTimestamp != "" {
		threadID = &msg.ThreadTimestamp
	}

	// Convert files and attachments to JSON
	attachmentsJSON := ""
	attachments := make([]models.Attachment, 0)
	seenURLs := make(map[string]bool)    // Track URLs to avoid duplicates
	seenFileIDs := make(map[string]bool) // Track file IDs to avoid duplicates

	// p.log("SlackProvider.convertMessage: Processing %d files, %d attachments for message %s\n", len(msg.Files), len(msg.Attachments), msg.Timestamp)

	// Process Files (slack.File objects)
	// Use file ID as primary key for deduplication, fallback to URL
	for _, file := range msg.Files {
		// Primary deduplication: use file ID if available, otherwise use URL
		var dedupKey string
		if file.ID != "" {
			dedupKey = file.ID
			if seenFileIDs[dedupKey] {
				// p.log("SlackProvider.convertMessage: SKIPPING duplicate file #%d (ID: %s, URL: %s) - already seen\n", i, file.ID, file.URLPrivate)
				continue
			}
			seenFileIDs[dedupKey] = true
			// p.log("SlackProvider.convertMessage: Processing file #%d (ID: %s, URL: %s, Name: %s, Size: %d, MimeType: %s)\n", i, file.ID, file.URLPrivate, file.Name, file.Size, file.Mimetype)
		} else {
			dedupKey = file.URLPrivate
			if seenURLs[dedupKey] {
				// p.log("SlackProvider.convertMessage: SKIPPING duplicate file #%d (URL: %s, Name: %s) - already seen\n", i, file.URLPrivate, file.Name)
				continue
			}
			seenURLs[dedupKey] = true
			// p.log("SlackProvider.convertMessage: Processing file #%d (URL: %s, Name: %s, Size: %d, MimeType: %s) - no ID\n", i, file.URLPrivate, file.Name, file.Size, file.Mimetype)
		}

		attachment := models.Attachment{
			Type:     file.Mimetype,
			URL:      file.URLPrivate,
			FileName: file.Name,
			FileSize: int64(file.Size),
			MimeType: file.Mimetype,
		}

		// Determine type from mimetype or file extension
		if file.Mimetype != "" {
			if len(file.Mimetype) >= 5 && file.Mimetype[:5] == "image" {
				attachment.Type = "image"
			} else if len(file.Mimetype) >= 5 && file.Mimetype[:5] == "video" {
				attachment.Type = "video"
			} else if len(file.Mimetype) >= 5 && file.Mimetype[:5] == "audio" {
				// Check if it's a voice message
				// Slack voice messages are typically:
				// - Short duration (usually < 5 minutes, so < ~5MB for compressed audio)
				// - Often have no filename or generic names
				// - Usually MP3 or OGG format
				isVoiceMessage := file.Size < 5*1024*1024 || // Less than 5MB
					file.Name == "" ||
					strings.Contains(strings.ToLower(file.Name), "voice") ||
					strings.Contains(strings.ToLower(file.Name), "recording") ||
					file.Mimetype == "audio/ogg" || file.Mimetype == "audio/opus"

				if isVoiceMessage {
					attachment.Type = "voice"
					// p.log("SlackProvider.convertMessage: Detected VOICE message (Size: %d, Name: %s, MimeType: %s)\n", file.Size, file.Name, file.Mimetype)
				} else {
					attachment.Type = "audio"
					// p.log("SlackProvider.convertMessage: Detected AUDIO file (Size: %d, Name: %s, MimeType: %s)\n", file.Size, file.Name, file.Mimetype)
				}
			} else {
				attachment.Type = "document"
			}
		}

		// Add thumbnail if available (use the largest available thumbnail)
		if file.Thumb1024 != "" {
			attachment.Thumbnail = file.Thumb1024
		} else if file.Thumb960 != "" {
			attachment.Thumbnail = file.Thumb960
		} else if file.Thumb720 != "" {
			attachment.Thumbnail = file.Thumb720
		} else if file.Thumb480 != "" {
			attachment.Thumbnail = file.Thumb480
		} else if file.Thumb360 != "" {
			attachment.Thumbnail = file.Thumb360
		} else if file.Thumb80 != "" {
			attachment.Thumbnail = file.Thumb80
		} else if file.Thumb64 != "" {
			attachment.Thumbnail = file.Thumb64
		}

		attachments = append(attachments, attachment)
		// p.log("SlackProvider.convertMessage: Added attachment (Type: %s, URL: %s, Name: %s)\n", attachment.Type, attachment.URL, attachment.FileName)
	}

	// p.log("SlackProvider.convertMessage: After processing Files: %d attachments\n", len(attachments))

	// Process Attachments (slack.Attachment objects - rich formatting)
	// Note: slack.Attachment is mainly for rich text formatting, not file attachments
	// Files are typically in msg.Files, but attachments can have image_url for images
	// IMPORTANT: Only process attachments if we haven't already processed the same file from msg.Files
	for _, slackAttachment := range msg.Attachments {
		// Check for image URL in attachment (only if not already seen)
		// Also check if this image URL corresponds to a file we've already processed
		if slackAttachment.ImageURL != "" {
			// Check if this URL matches any file we've already processed
			alreadyProcessed := false
			for _, existingAtt := range attachments {
				if existingAtt.URL == slackAttachment.ImageURL ||
					existingAtt.Thumbnail == slackAttachment.ImageURL {
					// p.log("SlackProvider.convertMessage: SKIPPING attachment #%d (ImageURL: %s) - already processed as file\n", i, slackAttachment.ImageURL)
					alreadyProcessed = true
					break
				}
			}

			if !alreadyProcessed && !seenURLs[slackAttachment.ImageURL] {
				seenURLs[slackAttachment.ImageURL] = true
				attachments = append(attachments, models.Attachment{
					Type:     "image",
					URL:      slackAttachment.ImageURL,
					FileName: slackAttachment.Title,
					MimeType: "image/png", // Default, Slack doesn't always provide this
				})
				// p.log("SlackProvider.convertMessage: Added attachment from msg.Attachments (ImageURL: %s, Title: %s)\n", slackAttachment.ImageURL, slackAttachment.Title)
			} else if seenURLs[slackAttachment.ImageURL] {
				// p.log("SlackProvider.convertMessage: SKIPPING attachment #%d (ImageURL: %s) - URL already seen\n", i, slackAttachment.ImageURL)
			}
		}
		// Note: Audio and video are typically in msg.Files, not in attachments
	}

	// p.log("SlackProvider.convertMessage: Final attachments count: %d\n", len(attachments))

	// Convert attachments to JSON string
	if len(attachments) > 0 {
		attachmentsBytes, err := json.Marshal(attachments)
		if err == nil {
			attachmentsJSON = string(attachmentsBytes)
		} else {
			p.log("SlackProvider.convertMessage: Failed to marshal attachments: %v\n", err)
		}
	}

	// Extract huddle join URL from the raw text before any preprocessing.
	// Slack encodes the URL as <https://...slack.com/huddle/...|label>.
	callUrl := ""
	callLinkAction := ""
	if m := huddleURLRegex.FindStringSubmatch(msg.Text); len(m) >= 2 {
		callUrl = m[1]
		callLinkAction = "join"
	}

	// Check if this is a huddle-related message
	// Slack huddles are typically indicated by system messages with specific text patterns
	// or by checking if the message subtype indicates a huddle
	callType := ""
	if msg.SubType == "sh_room_created" {
		isGroup := !strings.HasPrefix(normalizedConversationID, "D")
		if isGroup {
			callType = "incoming_group_call"
		} else {
			callType = "incoming_call"
		}
	}

	// Extract and optimize message body
	body := msg.Text

	// When the message has blocks, prefer block extraction over msg.Text:
	// - rich_text blocks: Slack serializes bullet lists as triple-backtick code blocks in msg.Text
	// - section/header/action blocks (bot messages): msg.Text is a short fallback, blocks hold full content
	if len(msg.Blocks.BlockSet) > 0 || len(body) < 10 {
		extracted := p.extractTextFromRichContent(msg)
		if len(extracted) > len(body) {
			body = extracted
		}
	}

	// Preprocess message body: resolve mentions, links, and formatting
	body = p.preprocessMessageBody(body)

	// Detect huddle start/end via text patterns
	// Common patterns: "started a huddle", "joined the huddle", "left the huddle", "ended the huddle"
	textLower := strings.ToLower(body)
	if strings.Contains(textLower, "huddle") {
		if strings.Contains(textLower, "started") || strings.Contains(textLower, "joined") {
			// Determine if it's a group or individual call
			// For Slack, huddles in channels are group calls, in DMs are individual
			isGroup := !strings.HasPrefix(normalizedConversationID, "D")
			if isGroup {
				callType = "incoming_group_call"
			} else {
				callType = "incoming_call"
			}
		} else if strings.Contains(textLower, "ended") || strings.Contains(textLower, "left") {
			// Huddle ended - we'll mark it as missed
			isGroup := !strings.HasPrefix(normalizedConversationID, "D")
			if isGroup {
				callType = "missed_group_voice"
			} else {
				callType = "missed_voice"
			}
		}
	}

	// Detect Slack blockquote formatting
	var quotedMsgID *string
	var quotedSenderName string
	var quotedBody *string

	if cleanText, qSender, qBody, isQuote := p.parseSlackBlockQuote(body); isQuote {
		body = cleanText
		quotedSenderName = qSender
		quotedBody = &qBody
		qID := msg.Timestamp + "-quote"
		quotedMsgID = &qID
	}

	return models.Message{
		ProtocolMsgID:    msg.Timestamp,
		ProtocolConvID:   normalizedConversationID, // Use normalized ID
		Body:             body,
		SenderID:         msg.User,
		SenderName:       senderName,
		SenderAvatarURL:  senderAvatarURL,
		Timestamp:        ts,
		IsFromMe:         isFromMe,
		ThreadID:         threadID,
		Reactions:        reactions,
		Attachments:      attachmentsJSON,
		CallType:         callType,
		CallUrl:          callUrl,
		CallLinkAction:   callLinkAction,
		QuotedMessageID:  quotedMsgID,
		QuotedSenderName: quotedSenderName,
		QuotedBody:       quotedBody,
	}
}

// preprocessMessageBody cleans and transforms Slack message text into standard Markdown.
func (p *SlackProvider) preprocessMessageBody(text string) string {
	if text == "" {
		return text
	}

	// 1. Transform Slack URL format <URL|text> or <URL> to Markdown [text](URL)
	urlRegex := regexp.MustCompile(`<([^|>]+)(?:\|([^>]+))?>`)
	text = urlRegex.ReplaceAllStringFunc(text, func(match string) string {
		submatch := urlRegex.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}
		url := submatch[1]
		linkText := url
		if len(submatch) > 2 && submatch[2] != "" {
			linkText = submatch[2]
		}

		// Handle user mentions separately (handled in next step)
		if strings.HasPrefix(url, "@U") || strings.HasPrefix(url, "@W") {
			return match
		}

		return "[" + linkText + "](" + url + ")"
	})

	// 2. Resolve User Mentions: <@U12345678> or <@U12345678|name>
	userMentionRegex := regexp.MustCompile(`<@([A-Z0-9]+)(?:\|[^>]+)?>`)
	text = userMentionRegex.ReplaceAllStringFunc(text, func(match string) string {
		submatch := userMentionRegex.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}
		userID := submatch[1]
		displayName, err := p.GetContactName(userID)
		if err == nil && displayName != "" {
			return "@" + displayName
		}
		return "@" + userID
	})

	// 3. Resolve Subteam Mentions: <!subteam^S12345678|@handle>
	subteamMentionRegex := regexp.MustCompile(`<!subteam\^[A-Z0-9]+\|@([^>]+)>`)
	text = subteamMentionRegex.ReplaceAllStringFunc(text, func(match string) string {
		submatch := subteamMentionRegex.FindStringSubmatch(match)
		if len(submatch) >= 2 {
			return "@" + submatch[1]
		}
		return match
	})

	// 4. Resolve Special Mentions: <!here>, <!channel>, <!everyone>
	text = strings.ReplaceAll(text, "<!here>", "@here")
	text = strings.ReplaceAll(text, "<!channel>", "@channel")
	text = strings.ReplaceAll(text, "<!everyone>", "@everyone")

	return text
}

func applyRichTextStyle(text string, style *slack.RichTextSectionTextStyle) string {
	if style == nil {
		return text
	}
	switch {
	case style.Code:
		return "`" + text + "`"
	case style.Bold && style.Italic:
		return "***" + text + "***"
	case style.Bold:
		return "**" + text + "**"
	case style.Italic:
		return "_" + text + "_"
	case style.Strike:
		return "~~" + text + "~~"
	}
	return text
}

// richTextSectionToText converts a RichTextSection's elements to a plain-text/markdown string.
func (p *SlackProvider) richTextSectionToText(section *slack.RichTextSection) string {
	var sb strings.Builder
	for _, elem := range section.Elements {
		switch e := elem.(type) {
		case *slack.RichTextSectionTextElement:
			sb.WriteString(applyRichTextStyle(e.Text, e.Style))
		case *slack.RichTextSectionLinkElement:
			linkText := e.Text
			if linkText == "" {
				linkText = e.URL
			}
			sb.WriteString("[" + linkText + "](" + e.URL + ")")
		case *slack.RichTextSectionUserElement:
			// Will be resolved by preprocessMessageBody
			sb.WriteString("<@" + e.UserID + ">")
		case *slack.RichTextSectionChannelElement:
			sb.WriteString("<#" + e.ChannelID + ">")
		case *slack.RichTextSectionEmojiElement:
			sb.WriteString(":" + e.Name + ":")
		case *slack.RichTextSectionBroadcastElement:
			sb.WriteString("@" + e.Range)
		}
	}
	return sb.String()
}

// richTextBlockToMarkdown converts a RichTextBlock to a markdown string, preserving
// bullet/ordered lists and preformatted sections.
func (p *SlackProvider) richTextBlockToMarkdown(block *slack.RichTextBlock) string {
	var parts []string
	for _, elem := range block.Elements {
		switch e := elem.(type) {
		case *slack.RichTextSection:
			parts = append(parts, p.richTextSectionToText(e))
		case *slack.RichTextList:
			for i, item := range e.Elements {
				if section, ok := item.(*slack.RichTextSection); ok {
					itemText := p.richTextSectionToText(section)
					indent := strings.Repeat("  ", e.Indent)
					var prefix string
					if e.Style == slack.RTEListOrdered {
						prefix = fmt.Sprintf("%s%d. ", indent, i+1+e.Offset)
					} else {
						prefix = indent + "- "
					}
					parts = append(parts, prefix+itemText)
				}
			}
		case *slack.RichTextPreformatted:
			content := p.richTextSectionToText(&slack.RichTextSection{Elements: e.Elements})
			parts = append(parts, "```\n"+content+"\n```")
		case *slack.RichTextQuote:
			content := p.richTextSectionToText(&slack.RichTextSection{Elements: e.Elements})
			for _, line := range strings.Split(content, "\n") {
				parts = append(parts, "> "+line)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// extractTextFromRichContent extracts a readable string from Slack Blocks and Attachments
func (p *SlackProvider) extractTextFromRichContent(msg slack.Message) string {
	var parts []string

	// Extract from blocks (the modern way)
	for _, block := range msg.Blocks.BlockSet {
		switch b := block.(type) {
		case *slack.RichTextBlock:
			parts = append(parts, p.richTextBlockToMarkdown(b))
		case *slack.SectionBlock:
			if b.Text != nil {
				parts = append(parts, b.Text.Text)
			}
			for _, field := range b.Fields {
				parts = append(parts, field.Text)
			}
		case *slack.ContextBlock:
			for _, elem := range b.ContextElements.Elements {
				if textElem, ok := elem.(*slack.TextBlockObject); ok {
					parts = append(parts, textElem.Text)
				}
			}
		case *slack.HeaderBlock:
			if b.Text != nil {
				parts = append(parts, b.Text.Text)
			}
		case *slack.ActionBlock:
			if b.Elements != nil {
				var labels []string
				for _, elem := range b.Elements.ElementSet {
					if btn, ok := elem.(*slack.ButtonBlockElement); ok && btn.Text != nil {
						labels = append(labels, btn.Text.Text)
					}
				}
				if len(labels) > 0 {
					parts = append(parts, "["+strings.Join(labels, " / ")+"]")
				}
			}
		}
	}

	// Extract from attachments (legacy but still common for bots/bridges)
	for _, att := range msg.Attachments {
		if att.Pretext != "" {
			parts = append(parts, att.Pretext)
		}
		if att.Text != "" {
			parts = append(parts, att.Text)
		}
		if att.Title != "" {
			parts = append(parts, att.Title)
		}
		if att.Fallback != "" && len(parts) == 0 {
			parts = append(parts, att.Fallback)
		}
		for _, field := range att.Fields {
			if field.Title != "" {
				parts = append(parts, field.Title+": "+field.Value)
			} else {
				parts = append(parts, field.Value)
			}
		}
	}

	result := strings.TrimSpace(strings.Join(parts, "\n"))
	return result
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
// saveUserNameToCache persists a user's display name to LinkedAccount and MetaContact
func (p *SlackProvider) saveUserNameToCache(userID, displayName, avatarURL string) {
	if db.DB == nil || userID == "" || displayName == "" || displayName == userID {
		return
	}

	// Use the existing updateLinkedAccountName function which handles MetaContact and LinkedAccount
	// Get instance ID
	p.mu.RLock()
	instanceID := ""
	if p.config != nil {
		if id, ok := p.config["_instance_id"].(string); ok {
			instanceID = id
		}
	}
	p.mu.RUnlock()

	if instanceID == "" {
		return
	}

	// Update LinkedAccount.Username (this is the source of truth for display names)
	if err := p.updateLinkedAccountName(instanceID, userID, displayName); err != nil {
		p.log("SlackProvider.saveUserNameToCache: Failed to update LinkedAccount for %s: %v\n", userID, err)
	}

	// Also update avatar URL if provided
	if avatarURL != "" {
		var account models.LinkedAccount
		if err := db.DB.Where("provider_instance_id = ? AND user_id = ?", instanceID, userID).First(&account).Error; err == nil {
			// Update LinkedAccount avatar
			if account.AvatarURL != avatarURL {
				account.AvatarURL = avatarURL
				db.DB.Save(&account)
			}
			// Also update MetaContact avatar if it's empty or different
			var metaContact models.MetaContact
			if err := db.DB.First(&metaContact, account.MetaContactID).Error; err == nil {
				if metaContact.AvatarURL == "" || metaContact.AvatarURL != avatarURL {
					metaContact.AvatarURL = avatarURL
					db.DB.Save(&metaContact)
				}
			}
		}
	}
}

// getUserNameFromCache retrieves a user's display name from LinkedAccount.Username
func (p *SlackProvider) getUserNameFromCache(userID string) (string, string) {
	if db.DB == nil || userID == "" {
		return "", ""
	}

	// Get instance ID
	p.mu.RLock()
	instanceID := ""
	if p.config != nil {
		if id, ok := p.config["_instance_id"].(string); ok {
			instanceID = id
		}
	}
	p.mu.RUnlock()

	if instanceID == "" {
		return "", ""
	}

	// Query LinkedAccount - Username is the source of truth
	var account models.LinkedAccount
	err := db.DB.Where("provider_instance_id = ? AND user_id = ?", instanceID, userID).First(&account).Error
	if err == nil {
		// Only return if we have a valid name (not empty and not the userID)
		if account.Username != "" && account.Username != userID {
			return account.Username, account.AvatarURL
		}
	}

	return "", ""
}

// fetchSenderInfoFromAPI fetches a user's display name and avatar directly from the
// Slack API, bypassing all local caches. It persists the result to the DB cache so
// subsequent loads are clean.
func (p *SlackProvider) fetchSenderInfoFromAPI(userID string) (string, string) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil || userID == "" {
		return "", ""
	}

	user, err := client.GetUserInfo(userID)
	if err != nil || user == nil {
		return "", ""
	}

	name := user.RealName
	if name == "" {
		name = user.Profile.DisplayName
	}
	if name == "" {
		name = user.Name
	}

	avatar := ""
	if user.Profile.Image512 != "" {
		avatar = user.Profile.Image512
	} else if user.Profile.Image48 != "" {
		avatar = user.Profile.Image48
	}

	if name != "" && name != userID {
		p.saveUserNameToCache(userID, name, avatar)
	}

	return name, avatar
}

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

	// Resolve self name once so we can detect corrupted messages where a
	// non-self message has been stored with the current user's name.
	selfName, _ := p.resolveUserInfo(p.selfUserID)

	for i := range messages {
		msg := &messages[i]

		if msg.SenderID == "" {
			continue
		}

		// Detect data corruption: message is not from us but carries our name.
		corruptedName := !msg.IsFromMe && selfName != "" && msg.SenderName == selfName

		// Skip enrichment only when the stored data looks fully correct.
		if !corruptedName && msg.SenderName != "" && msg.SenderAvatarURL != "" && msg.SenderName != msg.SenderID {
			continue
		}

		var name, avatar string
		if corruptedName {
			// Bypass DB cache: fetch directly from Slack API to get the real name.
			name, avatar = p.fetchSenderInfoFromAPI(msg.SenderID)
		} else {
			name, avatar = p.resolveUserInfo(msg.SenderID)
			// The DB cache itself may be corrupted: a non-self sender resolved to our own name.
			if !msg.IsFromMe && selfName != "" && name == selfName {
				name, avatar = p.fetchSenderInfoFromAPI(msg.SenderID)
			}
		}

		if name != "" && (corruptedName || msg.SenderName == "" || msg.SenderName == msg.SenderID) {
			msg.SenderName = name
		}
		if avatar != "" && msg.SenderAvatarURL == "" {
			msg.SenderAvatarURL = avatar
		}
	}
}

// SendTypingIndicator sends a typing indicator.
func (p *SlackProvider) SendTypingIndicator(_ string, _ bool) error {
	return nil
}

// AddReaction adds a reaction (emoji) to a message.
func (p *SlackProvider) AddReaction(conversationID string, messageID string, emoji string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	// Strip namespace prefix (e.g. "slack-1:C123" → "C123") and resolve DM user IDs to channel IDs
	rawConvID := p.normalizeDMConversationID(core.StripConvID(conversationID))
	actualChannelID := rawConvID
	if len(rawConvID) > 0 && rawConvID[0] == 'U' {
		channel, _, _, err := p.client.OpenConversation(&slack.OpenConversationParameters{
			Users:    []string{rawConvID},
			ReturnIM: true,
		})
		if err == nil && channel != nil && channel.ID != "" {
			actualChannelID = channel.ID
		}
	}

	item := slack.ItemRef{
		Channel:   actualChannelID,
		Timestamp: messageID,
	}
	emoji = strings.Trim(emoji, ":")
	if err := p.client.AddReaction(emoji, item); err != nil {
		// emoji-picker-react exposes a few GitHub-style aliases such as
		// "upside-down_face", while Slack names the same emoji
		// "upside_down_face". Preserve valid hyphenated Slack names, but retry
		// with underscores when Slack explicitly rejects the first name.
		underscoreEmoji := strings.ReplaceAll(emoji, "-", "_")
		if !strings.Contains(err.Error(), "invalid_name") || underscoreEmoji == emoji {
			return err
		}
		if retryErr := p.client.AddReaction(underscoreEmoji, item); retryErr != nil {
			return retryErr
		}
		emoji = underscoreEmoji
	}

	// Persist the reaction to DB so it survives query refetches.
	// (The polling loop may trigger a refetch before the RTM event arrives.)
	if db.DB != nil && p.selfUserID != "" {
		var msg models.Message
		if err := db.DB.Where("protocol_msg_id = ?", messageID).First(&msg).Error; err == nil {
			reaction := models.Reaction{
				MessageID: msg.ID,
				UserID:    p.selfUserID,
				Emoji:     emoji, // stored without colons, matching Slack API format
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			db.DB.Where("message_id = ? AND user_id = ? AND emoji = ?", msg.ID, p.selfUserID, emoji).
				FirstOrCreate(&reaction)
		}
	}

	// Emit an immediate reaction event so the frontend updates without waiting for polling.
	if p.selfUserID != "" {
		select {
		case p.eventChan <- core.ReactionEvent{InstanceID: p.getInstanceId(),
			ConversationID: conversationID, // Use original (normalized U...) ID to match frontend cache
			MessageID:      messageID,
			UserID:         p.selfUserID,
			Emoji:          emoji,
			Added:          true,
			Timestamp:      time.Now().Unix(),
		}:
		default:
		}
	}

	return nil
}

// RemoveReaction removes a reaction (emoji) from a message.
func (p *SlackProvider) RemoveReaction(conversationID string, messageID string, emoji string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	// Strip namespace prefix (e.g. "slack-1:C123" → "C123") and resolve DM user IDs to channel IDs
	rawConvID := p.normalizeDMConversationID(core.StripConvID(conversationID))
	actualChannelID := rawConvID
	if len(rawConvID) > 0 && rawConvID[0] == 'U' {
		channel, _, _, err := p.client.OpenConversation(&slack.OpenConversationParameters{
			Users:    []string{rawConvID},
			ReturnIM: true,
		})
		if err == nil && channel != nil && channel.ID != "" {
			actualChannelID = channel.ID
		}
	}

	item := slack.ItemRef{
		Channel:   actualChannelID,
		Timestamp: messageID,
	}
	emoji = strings.Trim(emoji, ":")
	if err := p.client.RemoveReaction(emoji, item); err != nil {
		underscoreEmoji := strings.ReplaceAll(emoji, "-", "_")
		if !strings.Contains(err.Error(), "invalid_name") || underscoreEmoji == emoji {
			return err
		}
		if retryErr := p.client.RemoveReaction(underscoreEmoji, item); retryErr != nil {
			return retryErr
		}
		emoji = underscoreEmoji
	}

	// Remove the reaction from DB so refetches reflect the correct state.
	if db.DB != nil && p.selfUserID != "" {
		var msg models.Message
		if err := db.DB.Where("protocol_msg_id = ?", messageID).First(&msg).Error; err == nil {
			db.DB.Where("message_id = ? AND user_id = ? AND emoji = ?", msg.ID, p.selfUserID, emoji).
				Delete(&models.Reaction{})
		}
	}

	// Emit an immediate reaction event so the frontend updates without waiting for polling.
	if p.selfUserID != "" {
		select {
		case p.eventChan <- core.ReactionEvent{InstanceID: p.getInstanceId(),
			ConversationID: conversationID, // Use original (normalized U...) ID to match frontend cache
			MessageID:      messageID,
			UserID:         p.selfUserID,
			Emoji:          emoji,
			Added:          false,
			Timestamp:      time.Now().Unix(),
		}:
		default:
		}
	}

	return nil
}

// GetCustomEmojiList returns a copy of the custom emoji cache (name -> URL).
// Unresolved aliases (URLs starting with "alias:") are excluded.
// If the cache is empty (e.g., loadEmojis hasn't finished yet), it triggers a synchronous load.
func (p *SlackProvider) GetCustomEmojiList() map[string]string {
	// Lazy-load: if cache is empty, trigger a synchronous load now
	p.emojiCacheMu.RLock()
	empty := len(p.emojiCache) == 0
	p.emojiCacheMu.RUnlock()
	if empty {
		p.loadEmojis()
	}

	p.emojiCacheMu.RLock()
	defer p.emojiCacheMu.RUnlock()

	result := make(map[string]string, len(p.emojiCache))
	for name, url := range p.emojiCache {
		// Skip unresolved aliases
		if strings.HasPrefix(url, "alias:") {
			continue
		}
		result[name] = url
	}
	return result
}
