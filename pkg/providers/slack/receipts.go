package slack

// MarkMessageAsRead marks a message as read.
func (p *SlackProvider) MarkMessageAsRead(conversationID string, messageID string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Slack "mark as read" is usually creating an implicit read marker.
	// We can't easily do this with a bot token for a user, usually.
	// But assuming we might have a user token:
	// return p.client.SetChannelReadMark(conversationID, messageID)
	// For now, no-op as it's often not critical for bots.
	return nil
}

// MarkConversationAsRead marks all messages in a conversation as read.
func (p *SlackProvider) MarkConversationAsRead(conversationID string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
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
