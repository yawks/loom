package whatsapp

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
)

func (w *WhatsAppProvider) PinMessage(conversationID, messageID string) (*models.MessagePin, error) {
	if w.client == nil {
		return nil, fmt.Errorf("whatsapp: not connected")
	}
	var message models.Message
	if db.DB == nil || db.DB.Where("protocol_msg_id = ? AND protocol_conv_id = ?", messageID, conversationID).First(&message).Error != nil {
		return nil, fmt.Errorf("whatsapp: message %s is not available locally", messageID)
	}
	chat, err := types.ParseJID(core.StripConvID(conversationID))
	if err != nil {
		return nil, fmt.Errorf("whatsapp: invalid conversation: %w", err)
	}
	sender, err := types.ParseJID(message.SenderID)
	if err != nil && message.IsFromMe && w.client.Store != nil {
		if w.client.Store.ID != nil {
			sender = w.client.Store.ID.ToNonAD()
		}
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("whatsapp: invalid message sender: %w", err)
	}
	if err := w.client.SendAppState(context.Background(), appstate.BuildStar(chat, sender, types.MessageID(messageID), message.IsFromMe, true)); err != nil {
		return nil, fmt.Errorf("whatsapp: star message: %w", err)
	}
	now := time.Now()
	return &models.MessagePin{ProviderInstanceID: w.getInstanceId(), ProtocolConvID: conversationID, ProtocolMsgID: messageID, SenderID: message.SenderID, MessageIsFromMe: message.IsFromMe, Scope: models.MessagePinScopePersonal, Resolution: models.MessagePinResolutionResolved, PinnedAt: &now, MessageTimestamp: &message.Timestamp, Message: &message}, nil
}

func (w *WhatsAppProvider) UnpinMessage(conversationID, messageID string) error {
	if w.client == nil {
		return fmt.Errorf("whatsapp: not connected")
	}
	var message models.Message
	var storedPin models.MessagePin
	messageFound := db.DB != nil && db.DB.Where("protocol_msg_id = ? AND protocol_conv_id = ?", messageID, conversationID).First(&message).Error == nil
	if !messageFound && (db.DB == nil || db.DB.Where("provider_instance_id = ? AND protocol_msg_id = ?", w.getInstanceId(), messageID).First(&storedPin).Error != nil) {
		return fmt.Errorf("whatsapp: metadata for message %s is not available", messageID)
	}
	chat, err := types.ParseJID(core.StripConvID(conversationID))
	if err != nil {
		return err
	}
	senderID, isFromMe := message.SenderID, message.IsFromMe
	if !messageFound {
		senderID, isFromMe = storedPin.SenderID, storedPin.MessageIsFromMe
	}
	sender, err := types.ParseJID(senderID)
	if err != nil && isFromMe && w.client.Store != nil {
		if w.client.Store.ID != nil {
			sender = w.client.Store.ID.ToNonAD()
		}
		err = nil
	}
	if err != nil {
		return err
	}
	return w.client.SendAppState(context.Background(), appstate.BuildStar(chat, sender, types.MessageID(messageID), isFromMe, false))
}

func (w *WhatsAppProvider) ListMessagePins(conversationID string) ([]models.MessagePin, error) {
	var pins []models.MessagePin
	if db.DB == nil {
		return pins, nil
	}
	err := db.DB.Where("provider_instance_id = ? AND protocol_conv_id = ?", w.getInstanceId(), conversationID).Order("pinned_at DESC").Find(&pins).Error
	return pins, err
}

func (w *WhatsAppProvider) ResolvePinnedMessage(conversationID, messageID string) (*models.Message, error) {
	var message models.Message
	if db.DB != nil && db.DB.Where("protocol_msg_id = ? AND protocol_conv_id = ?", messageID, conversationID).Preload("Receipts").Preload("Reactions").First(&message).Error == nil {
		return &message, nil
	}
	if w.client == nil || db.DB == nil {
		return nil, fmt.Errorf("whatsapp: pinned message %s has not been synchronized", messageID)
	}
	var pin models.MessagePin
	if db.DB.Where("provider_instance_id = ? AND protocol_msg_id = ?", w.getInstanceId(), messageID).First(&pin).Error != nil {
		return nil, fmt.Errorf("whatsapp: pinned message metadata is unavailable")
	}
	chat, err := types.ParseJID(core.StripConvID(conversationID))
	if err != nil {
		return nil, err
	}
	sender, err := types.ParseJID(pin.SenderID)
	if err != nil && pin.MessageIsFromMe && w.client.Store != nil && w.client.Store.ID != nil {
		sender, err = w.client.Store.ID.ToNonAD(), nil
	}
	if err != nil {
		return nil, err
	}
	if _, err = w.client.SendPeerMessage(context.Background(), w.client.BuildUnavailableMessageRequest(chat, sender, messageID)); err != nil {
		return nil, fmt.Errorf("whatsapp: request old pinned message: %w", err)
	}
	for range 20 {
		time.Sleep(250 * time.Millisecond)
		if db.DB.Where("protocol_msg_id = ? AND protocol_conv_id = ?", messageID, conversationID).Preload("Receipts").Preload("Reactions").First(&message).Error == nil {
			return &message, nil
		}
	}
	return nil, fmt.Errorf("whatsapp: the old pinned message was requested from your phone; try again shortly")
}

func (w *WhatsAppProvider) PinConversation(conversationID string) error {
	// TODO: Implement pinning
	markUnused(conversationID)
	return fmt.Errorf("pinning not yet implemented")
}

func (w *WhatsAppProvider) UnpinConversation(conversationID string) error {
	// TODO: Implement unpinning
	markUnused(conversationID)
	return fmt.Errorf("unpinning not yet implemented")
}

func (w *WhatsAppProvider) MuteConversation(conversationID string) error {
	return w.setConversationMuted(conversationID, true)
}

func (w *WhatsAppProvider) UnmuteConversation(conversationID string) error {
	return w.setConversationMuted(conversationID, false)
}

func (w *WhatsAppProvider) setConversationMuted(conversationID string, muted bool) error {
	if w.client == nil {
		return fmt.Errorf("whatsapp: not connected")
	}
	chat, err := types.ParseJID(core.StripConvID(conversationID))
	if err != nil {
		return fmt.Errorf("whatsapp: invalid conversation: %w", err)
	}
	if err := w.client.SendAppState(context.Background(), appstate.BuildMute(chat, muted, 0)); err != nil {
		return fmt.Errorf("whatsapp: update conversation mute state: %w", err)
	}
	return nil
}

func (w *WhatsAppProvider) GetConversationState(conversationID string) (*models.Conversation, error) {
	if w.client == nil || w.client.Store == nil || w.client.Store.ChatSettings == nil {
		return nil, fmt.Errorf("whatsapp: not connected")
	}
	chat, err := types.ParseJID(core.StripConvID(conversationID))
	if err != nil {
		return nil, fmt.Errorf("whatsapp: invalid conversation: %w", err)
	}
	settings, err := w.client.Store.ChatSettings.GetChatSettings(context.Background(), chat)
	if err != nil {
		return nil, fmt.Errorf("whatsapp: get conversation settings: %w", err)
	}
	muted := !settings.MutedUntil.IsZero() && settings.MutedUntil.After(time.Now())
	return &models.Conversation{ProtocolConvID: conversationID, IsMuted: muted}, nil
}
