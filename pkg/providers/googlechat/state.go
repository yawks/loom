package googlechat

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"Loom/pkg/core"
	"Loom/pkg/models"
)

func (p *GoogleChatProvider) PinMessage(convID, messageID string) (*models.MessagePin, error) {
	rawConvID := core.StripConvID(convID)
	messageID = strings.TrimPrefix(messageID, "/")
	var remote ChatMessagePin
	if err := p.apiPost("/"+rawConvID+"/messagePins", ChatMessagePin{Message: messageID}, &remote); err != nil {
		return nil, fmt.Errorf("googlechat: pin message: %w", err)
	}
	message, err := p.ResolvePinnedMessage(convID, messageID)
	if err != nil {
		now := time.Now()
		return &models.MessagePin{ProviderInstanceID: p.getInstanceID(), ProtocolConvID: convID, ProtocolMsgID: messageID, Scope: models.MessagePinScopeShared, Resolution: models.MessagePinResolutionUnresolved, PinnedAt: &now, ProviderPinID: remote.Name}, nil
	}
	now := time.Now()
	return &models.MessagePin{ProviderInstanceID: p.getInstanceID(), ProtocolConvID: convID, ProtocolMsgID: messageID, Scope: models.MessagePinScopeShared, Resolution: models.MessagePinResolutionResolved, PinnedAt: &now, MessageTimestamp: &message.Timestamp, ProviderPinID: remote.Name, Message: message}, nil
}

func (p *GoogleChatProvider) UnpinMessage(convID, messageID string) error {
	rawConvID := core.StripConvID(convID)
	messageResourceID := messageID[strings.LastIndex(messageID, "/")+1:]
	return p.apiDelete("/" + rawConvID + "/messagePins/" + url.PathEscape(messageResourceID))
}

func (p *GoogleChatProvider) ListMessagePins(convID string) ([]models.MessagePin, error) {
	rawConvID := core.StripConvID(convID)
	var resp MessagePinListResponse
	if err := p.apiGet("/"+rawConvID+"/messagePins", url.Values{"pageSize": {"100"}}, &resp); err != nil {
		return nil, fmt.Errorf("googlechat: list message pins: %w", err)
	}
	pins := make([]models.MessagePin, 0, len(resp.MessagePins))
	for _, remote := range resp.MessagePins {
		pins = append(pins, models.MessagePin{ProviderInstanceID: p.getInstanceID(), ProtocolConvID: convID, ProtocolMsgID: remote.Message, ProviderPinID: remote.Name, Scope: models.MessagePinScopeShared, Resolution: models.MessagePinResolutionUnresolved})
	}
	return pins, nil
}

func (p *GoogleChatProvider) ResolvePinnedMessage(convID, messageID string) (*models.Message, error) {
	var remote ChatMessage
	if err := p.apiGet("/"+strings.TrimPrefix(messageID, "/"), nil, &remote); err != nil {
		return nil, fmt.Errorf("googlechat: get pinned message: %w", err)
	}
	message := p.convertMessage(remote, core.StripConvID(convID), p.getSelfID())
	p.storeMessagesForConversation(core.StripConvID(convID), []models.Message{message})
	return &message, nil
}

func (p *GoogleChatProvider) PinConversation(convID string) error {
	return fmt.Errorf("googlechat: PinConversation not supported")
}

func (p *GoogleChatProvider) UnpinConversation(convID string) error {
	return fmt.Errorf("googlechat: UnpinConversation not supported")
}

func (p *GoogleChatProvider) MuteConversation(convID string) error {
	return fmt.Errorf("googlechat: MuteConversation not supported")
}

func (p *GoogleChatProvider) UnmuteConversation(convID string) error {
	return fmt.Errorf("googlechat: UnmuteConversation not supported")
}

func (p *GoogleChatProvider) GetConversationState(convID string) (*models.Conversation, error) {
	return &models.Conversation{ProtocolConvID: convID}, nil
}
