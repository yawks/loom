package whatsapp

import (
	"Loom/pkg/models"
	"fmt"
)

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
	// TODO: Implement muting
	markUnused(conversationID)
	return fmt.Errorf("muting not yet implemented")
}

func (w *WhatsAppProvider) UnmuteConversation(conversationID string) error {
	// TODO: Implement unmuting
	markUnused(conversationID)
	return fmt.Errorf("unmuting not yet implemented")
}

func (w *WhatsAppProvider) GetConversationState(conversationID string) (*models.Conversation, error) {
	// TODO: Implement getting conversation state
	markUnused(conversationID)
	return nil, fmt.Errorf("getting conversation state not yet implemented")
}
