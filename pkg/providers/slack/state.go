package slack

import (
	"Loom/pkg/models"
	"fmt"

	"github.com/slack-go/slack"
)

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
