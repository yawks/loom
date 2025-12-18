package slack

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/slack-go/slack"
)

// MarkMessageAsRead marks a message as read by setting the read cursor in Slack.
// messageID is the ProtocolMsgID which is a Slack timestamp string (e.g., "1502126650.000003").
func (p *SlackProvider) MarkMessageAsRead(conversationID string, messageID string) error {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	// Handle different ID types for Slack conversations (user ID -> channel ID for DMs)
	actualChannelID := conversationID
	if len(conversationID) > 0 && conversationID[0] == 'U' {
		// Open DM to get channel ID
		channel, _, _, err := client.OpenConversation(&slack.OpenConversationParameters{
			Users: []string{conversationID},
		})
		if err != nil {
			return fmt.Errorf("failed to open DM conversation: %w", err)
		}
		if channel != nil && channel.ID != "" {
			actualChannelID = channel.ID
		}
	} else if len(conversationID) > 0 && conversationID[0] == 'D' {
		// For DM channel IDs, ensure the conversation is open
		_, _, _, err := client.OpenConversation(&slack.OpenConversationParameters{
			ChannelID: conversationID,
		})
		if err != nil {
			// Log but don't fail - the conversation might already be open
			p.log("SlackProvider.MarkMessageAsRead: Warning - failed to open DM conversation %s: %v (may already be open)\n", conversationID, err)
		}
	}

	// messageID is a Slack timestamp string (e.g., "1502126650.000003")
	// MarkConversation expects the timestamp as a string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.MarkConversationContext(ctx, actualChannelID, messageID)
	if err != nil {
		return fmt.Errorf("failed to mark conversation as read: %w", err)
	}

	p.log("SlackProvider.MarkMessageAsRead: Marked message %s as read in conversation %s\n", messageID, actualChannelID)
	return nil
}

// MarkConversationAsRead marks all messages in a conversation as read.
// This is called when opening a conversation to mark all messages as read.
func (p *SlackProvider) MarkConversationAsRead(conversationID string) error {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	// Handle different ID types for Slack conversations
	actualChannelID := conversationID
	if len(conversationID) > 0 && conversationID[0] == 'U' {
		// Open DM to get channel ID
		channel, _, _, err := client.OpenConversation(&slack.OpenConversationParameters{
			Users: []string{conversationID},
		})
		if err != nil {
			return fmt.Errorf("failed to open DM conversation: %w", err)
		}
		if channel != nil && channel.ID != "" {
			actualChannelID = channel.ID
		}
	} else if len(conversationID) > 0 && conversationID[0] == 'D' {
		// For DM channel IDs, ensure the conversation is open
		_, _, _, err := client.OpenConversation(&slack.OpenConversationParameters{
			ChannelID: conversationID,
		})
		if err != nil {
			// Log but don't fail - the conversation might already be open
			p.log("SlackProvider.MarkConversationAsRead: Warning - failed to open DM conversation %s: %v (may already be open)\n", conversationID, err)
		}
	}

	// Get the latest message timestamp in the conversation to mark everything as read
	var timestamp string
	if db.DB != nil {
		var latestMessage models.Message
		if err := db.DB.Where("protocol_conv_id = ?", actualChannelID).
			Order("timestamp DESC").
			Limit(1).
			First(&latestMessage).Error; err == nil && latestMessage.ProtocolMsgID != "" {
			// Use the timestamp of the latest message
			timestamp = latestMessage.ProtocolMsgID
		}
	}

	// Fallback to current timestamp if no messages found
	if timestamp == "" {
		now := time.Now()
		timestamp = strconv.FormatFloat(float64(now.Unix())+float64(now.Nanosecond())/1e9, 'f', -1, 64)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.MarkConversationContext(ctx, actualChannelID, timestamp)
	if err != nil {
		return fmt.Errorf("failed to mark conversation as read: %w", err)
	}

	p.log("SlackProvider.MarkConversationAsRead: Marked all messages as read in conversation %s (up to timestamp %s)\n", actualChannelID, timestamp)
	return nil
}

// MarkMessageAsPlayed marks a voice message as played.
func (p *SlackProvider) MarkMessageAsPlayed(conversationID string, messageID string) error {
	return nil
}

// SendRetryReceipt sends a retry receipt.
func (p *SlackProvider) SendRetryReceipt(conversationID string, messageID string) error {
	return nil
}
