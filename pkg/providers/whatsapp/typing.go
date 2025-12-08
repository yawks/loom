package whatsapp

import (
	"fmt"
	"go.mau.fi/whatsmeow/types"
)

func (w *WhatsAppProvider) SendTypingIndicator(conversationID string, isTyping bool) error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.client == nil {
		return fmt.Errorf("client not initialized")
	}

	// Parse conversation ID (JID)
	jid, err := types.ParseJID(conversationID)
	if err != nil {
		return fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Send typing indicator
	if isTyping {
		err = w.client.SendChatPresence(w.ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	} else {
		err = w.client.SendChatPresence(w.ctx, jid, types.ChatPresencePaused, types.ChatPresenceMediaText)
	}
	if err != nil {
		return fmt.Errorf("failed to send typing indicator: %w", err)
	}

	return nil
}
