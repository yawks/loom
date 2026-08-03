package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"fmt"
	"time"

	"github.com/slack-go/slack"
)

// slackPinChannel resolves Loom's stable DM identifier (a Slack user ID) to
// the channel ID required by Slack's pins API.
func (p *SlackProvider) slackPinChannel(conversationID string) (string, error) {
	rawID := core.StripConvID(conversationID)
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return "", fmt.Errorf("slack client not initialized")
	}
	if len(rawID) > 0 && rawID[0] == 'U' {
		channel, _, _, err := client.OpenConversation(&slack.OpenConversationParameters{Users: []string{rawID}, ReturnIM: true})
		if err != nil {
			return "", fmt.Errorf("slack: open DM for pin: %w", err)
		}
		if channel == nil || channel.ID == "" {
			return "", fmt.Errorf("slack: DM channel is unavailable")
		}
		return channel.ID, nil
	}
	return rawID, nil
}

func (p *SlackProvider) PinMessage(conversationID, messageID string) (*models.MessagePin, error) {
	channelID, err := p.slackPinChannel(conversationID)
	if err != nil {
		return nil, err
	}
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if err := client.AddPin(channelID, slack.NewRefToMessage(channelID, messageID)); err != nil {
		return nil, fmt.Errorf("slack: pin message: %w", err)
	}

	nsConvID := core.BuildConvID(p.getInstanceId(), p.normalizeDMConversationID(channelID))
	pin := &models.MessagePin{
		ProviderInstanceID: p.getInstanceId(), ProtocolConvID: nsConvID, ProtocolMsgID: messageID,
		Scope: models.MessagePinScopeShared, Resolution: models.MessagePinResolutionUnresolved,
	}
	now := time.Now()
	pin.PinnedAt = &now
	if db.DB != nil {
		var message models.Message
		if db.DB.Where("protocol_conv_id = ? AND protocol_msg_id = ?", nsConvID, messageID).First(&message).Error == nil {
			pin.Message = &message
			pin.MessageTimestamp = &message.Timestamp
			pin.SenderID = message.SenderID
			pin.MessageIsFromMe = message.IsFromMe
			pin.Resolution = models.MessagePinResolutionResolved
		}
	}
	return pin, nil
}

func (p *SlackProvider) UnpinMessage(conversationID, messageID string) error {
	channelID, err := p.slackPinChannel(conversationID)
	if err != nil {
		return err
	}
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if err := client.RemovePin(channelID, slack.NewRefToMessage(channelID, messageID)); err != nil {
		return fmt.Errorf("slack: unpin message: %w", err)
	}
	return nil
}

func (p *SlackProvider) ListMessagePins(conversationID string) ([]models.MessagePin, error) {
	channelID, err := p.slackPinChannel(conversationID)
	if err != nil {
		return nil, err
	}
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	items, _, err := client.ListPins(channelID)
	if err != nil {
		return nil, fmt.Errorf("slack: list message pins: %w", err)
	}

	nsConvID := core.BuildConvID(p.getInstanceId(), p.normalizeDMConversationID(channelID))
	pins := make([]models.MessagePin, 0, len(items))
	remoteIDs := make([]string, 0, len(items))
	for _, item := range items {
		if item.Type != slack.TYPE_MESSAGE || item.Message == nil {
			continue
		}
		messageID := item.Message.Timestamp
		if messageID == "" {
			messageID = item.Timestamp
		}
		if messageID == "" {
			continue
		}
		slackMessage := *item.Message
		if slackMessage.Timestamp == "" {
			slackMessage.Timestamp = messageID
		}
		message := p.convertMessage(slackMessage, channelID)
		pin := models.MessagePin{
			ProviderInstanceID: p.getInstanceId(), ProtocolConvID: nsConvID, ProtocolMsgID: messageID,
			SenderID: message.SenderID, MessageIsFromMe: message.IsFromMe,
			Scope: models.MessagePinScopeShared, Resolution: models.MessagePinResolutionResolved,
			MessageTimestamp: &message.Timestamp, Message: &message,
		}
		if db.DB != nil {
			var stored models.MessagePin
			if db.DB.Where("provider_instance_id = ? AND protocol_msg_id = ?", p.getInstanceId(), messageID).First(&stored).Error == nil {
				pin.PinnedAt = stored.PinnedAt
			}
			p.storeMessagesForConversation(nsConvID, []models.Message{message})
		}
		if pin.PinnedAt == nil {
			now := time.Now()
			pin.PinnedAt = &now
		}
		pins = append(pins, pin)
		remoteIDs = append(remoteIDs, messageID)
	}
	if db.DB != nil {
		stale := db.DB.Where("provider_instance_id = ? AND protocol_conv_id = ?", p.getInstanceId(), nsConvID)
		if len(remoteIDs) == 0 {
			_ = stale.Delete(&models.MessagePin{}).Error
		} else {
			_ = stale.Where("protocol_msg_id NOT IN ?", remoteIDs).Delete(&models.MessagePin{}).Error
		}
	}
	return pins, nil
}

func (p *SlackProvider) ResolvePinnedMessage(conversationID, messageID string) (*models.Message, error) {
	nsConvID := core.BuildConvID(p.getInstanceId(), p.normalizeDMConversationID(core.StripConvID(conversationID)))
	if db.DB != nil {
		var message models.Message
		if db.DB.Where("protocol_conv_id = ? AND protocol_msg_id = ?", nsConvID, messageID).
			Preload("Receipts").Preload("Reactions").First(&message).Error == nil {
			return &message, nil
		}
	}
	// pins.list includes the complete Slack message, so refreshing the remote pin
	// list is the most reliable targeted lookup (including pinned thread replies).
	pins, err := p.ListMessagePins(conversationID)
	if err != nil {
		return nil, err
	}
	for i := range pins {
		if pins[i].ProtocolMsgID == messageID && pins[i].Message != nil {
			return pins[i].Message, nil
		}
	}
	return nil, fmt.Errorf("slack: pinned message %s is no longer available", messageID)
}

// PinConversation pins a conversation (Stars it in Slack).
func (p *SlackProvider) PinConversation(conversationID string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	return p.client.AddStar(conversationID, slack.ItemRef{Channel: conversationID})
}

// UnpinConversation unpins a conversation.
func (p *SlackProvider) UnpinConversation(conversationID string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	return p.client.RemoveStar(conversationID, slack.ItemRef{Channel: conversationID})
}

// MuteConversation mutes a conversation.
func (p *SlackProvider) MuteConversation(conversationID string) error {
	return nil
}

// UnmuteConversation unmutes a conversation.
func (p *SlackProvider) UnmuteConversation(conversationID string) error {
	return nil
}

// GetConversationState returns the state of a conversation.
func (p *SlackProvider) GetConversationState(conversationID string) (*models.Conversation, error) {
	// We return just ID for now, caller will merge with DB state if needed
	return &models.Conversation{ProtocolConvID: conversationID}, nil
}
