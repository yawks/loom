package googlechat

import (
	"fmt"

	"Loom/pkg/models"
)

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
