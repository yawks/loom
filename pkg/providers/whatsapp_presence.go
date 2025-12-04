package providers

import (
	"Loom/pkg/models"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/types"
)

// subscribeToContactPresence subscribes to presence updates for DM contacts.

// subscribeToContactPresence subscribes to presence updates for DM contacts.
// This allows receiving online/offline status updates from contacts.
func (w *WhatsAppProvider) subscribeToContactPresence() {
	if w.client == nil {
		return
	}

	// Wait a bit for the client to be fully ready
	time.Sleep(2 * time.Second)

	fmt.Println("WhatsApp: Starting presence subscription for DM contacts...")

	// Get all conversations
	w.mu.RLock()
	conversations := make([]models.LinkedAccount, 0, len(w.conversations))
	for _, conv := range w.conversations {
		conversations = append(conversations, conv)
	}
	w.mu.RUnlock()

	// Subscribe to presence for DM contacts (not groups)
	subscribed := 0
	for _, conv := range conversations {
		// Skip group chats (they end with @g.us)
		if len(conv.UserID) > 5 && conv.UserID[len(conv.UserID)-5:] == "@g.us" {
			continue
		}

		// Parse JID
		jid, err := types.ParseJID(conv.UserID)
		if err != nil {
			fmt.Printf("WhatsApp: Failed to parse JID for presence subscription: %s, error: %v\n", conv.UserID, err)
			continue
		}

		// Subscribe to presence
		err = w.client.SubscribePresence(w.ctx, jid)
		if err != nil {
			fmt.Printf("WhatsApp: Failed to subscribe to presence for %s: %v\n", conv.UserID, err)
		} else {
			subscribed++
		}

		// Small delay to avoid overwhelming the server
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Printf("WhatsApp: Subscribed to presence updates for %d DM contacts\n", subscribed)
}
