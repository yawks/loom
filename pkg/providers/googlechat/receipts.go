package googlechat

import "fmt"

// MarkMessageAsRead delegates to MarkConversationAsRead.
func (p *GoogleChatProvider) MarkMessageAsRead(convID, msgID string) error {
	return p.MarkConversationAsRead(convID)
}

// MarkConversationAsRead is not supported by the Google Chat REST API.
func (p *GoogleChatProvider) MarkConversationAsRead(convID string) error {
	return nil
}

// MarkMessageAsPlayed is not supported.
func (p *GoogleChatProvider) MarkMessageAsPlayed(convID, msgID string) error {
	return fmt.Errorf("googlechat: MarkMessageAsPlayed not supported")
}
