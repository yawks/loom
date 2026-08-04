package teams

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"fmt"
	"time"

	"go.mau.fi/mautrix-teams/pkg/msteams"
)

func (p *Provider) PinMessage(conversationID, messageID string) (*models.MessagePin, error) {
	client, instance, err := p.connectedClient()
	if err != nil {
		return nil, err
	}
	rawConvID := core.StripConvID(conversationID)
	if err := client.PinMessage(context.Background(), rawConvID, messageID); err != nil {
		return nil, fmt.Errorf("%s: pin message: %w", providerID, err)
	}
	message, err := p.resolvePinMessage(client, conversationID, messageID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &models.MessagePin{
		ProviderInstanceID: instance, ProtocolConvID: message.ProtocolConvID, ProtocolMsgID: messageID,
		SenderID: message.SenderID, MessageIsFromMe: message.IsFromMe,
		Scope: models.MessagePinScopeShared, Resolution: models.MessagePinResolutionResolved,
		PinnedAt: &now, MessageTimestamp: &message.Timestamp, Message: message,
	}, nil
}

func (p *Provider) UnpinMessage(conversationID, messageID string) error {
	client, _, err := p.connectedClient()
	if err != nil {
		return err
	}
	if err := client.UnpinMessage(context.Background(), core.StripConvID(conversationID), messageID); err != nil {
		return fmt.Errorf("%s: unpin message: %w", providerID, err)
	}
	return nil
}

func (p *Provider) ListMessagePins(conversationID string) ([]models.MessagePin, error) {
	client, instance, err := p.connectedClient()
	if err != nil {
		return nil, err
	}
	rawConvID := core.StripConvID(conversationID)
	nsConvID := core.BuildConvID(instance, rawConvID)
	items, err := client.GetPinnedMessages(context.Background(), rawConvID)
	if err != nil {
		return nil, fmt.Errorf("%s: list message pins: %w", providerID, err)
	}
	pins := make([]models.MessagePin, 0, len(items))
	remoteIDs := make([]string, 0, len(items))
	for _, item := range items {
		message, resolveErr := p.resolvePinMessage(client, nsConvID, item.ItemID)
		pin := models.MessagePin{
			ProviderInstanceID: instance, ProtocolConvID: nsConvID, ProtocolMsgID: item.ItemID,
			Scope: models.MessagePinScopeShared, Resolution: models.MessagePinResolutionUnresolved,
		}
		if resolveErr == nil {
			pin.SenderID = message.SenderID
			pin.MessageIsFromMe = message.IsFromMe
			pin.MessageTimestamp = &message.Timestamp
			pin.Message = message
			pin.Resolution = models.MessagePinResolutionResolved
		}
		if db.DB != nil {
			var stored models.MessagePin
			if db.DB.Where("provider_instance_id = ? AND protocol_msg_id = ?", instance, item.ItemID).First(&stored).Error == nil {
				pin.PinnedAt = stored.PinnedAt
			}
		}
		if pin.PinnedAt == nil {
			now := time.Now()
			pin.PinnedAt = &now
		}
		pins = append(pins, pin)
		remoteIDs = append(remoteIDs, item.ItemID)
	}
	if db.DB != nil {
		stale := db.DB.Where("provider_instance_id = ? AND protocol_conv_id = ?", instance, nsConvID)
		if len(remoteIDs) == 0 {
			_ = stale.Delete(&models.MessagePin{}).Error
		} else {
			_ = stale.Where("protocol_msg_id NOT IN ?", remoteIDs).Delete(&models.MessagePin{}).Error
		}
	}
	return pins, nil
}

func (p *Provider) ResolvePinnedMessage(conversationID, messageID string) (*models.Message, error) {
	client, _, err := p.connectedClient()
	if err != nil {
		return nil, err
	}
	return p.resolvePinMessage(client, conversationID, messageID)
}

func (p *Provider) resolvePinMessage(client *msteams.Client, conversationID, messageID string) (*models.Message, error) {
	rawConvID := core.StripConvID(conversationID)
	nsConvID := core.BuildConvID(p.instance, rawConvID)
	if db.DB != nil {
		var message models.Message
		if db.DB.Where("protocol_conv_id = ? AND protocol_msg_id = ?", nsConvID, messageID).
			Preload("Receipts").Preload("Reactions").First(&message).Error == nil {
			return &message, nil
		}
	}
	remote, err := client.FetchMessage(context.Background(), rawConvID, messageID)
	if err != nil {
		return nil, fmt.Errorf("%s: resolve pinned message: %w", providerID, err)
	}
	message := p.toModelMessage(client, *remote, rawConvID)
	_ = p.storeMessages([]models.Message{message})
	return &message, nil
}
