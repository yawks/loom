package whatsapp

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/types"
)

func (w *WhatsAppProvider) MarkMessageAsRead(conversationID string, messageID string) error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.client == nil {
		return fmt.Errorf("client not initialized")
	}

	// Parse conversation ID (JID)
	chatJID, err := types.ParseJID(conversationID)
	if err != nil {
		return fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Get message from database to find the sender
	var message models.Message
	if err := db.DB.Where("protocol_msg_id = ?", messageID).First(&message).Error; err != nil {
		fmt.Printf("WhatsApp: Warning - Could not find message %s in database: %v\n", messageID, err)
		// If message not found, use chatJID as participantJID (fallback)
		err = w.client.MarkRead(w.ctx, []types.MessageID{types.MessageID(messageID)}, time.Now(), chatJID, chatJID, types.ReceiptTypeRead)
		if err != nil {
			return fmt.Errorf("failed to send read receipt: %w", err)
		}
		fmt.Printf("WhatsApp: Sent read receipt for message %s in conversation %s (using chatJID as participant)\n", messageID, conversationID)
		return nil
	}

	// Determine participantJID based on message sender
	var participantJID types.JID
	if message.IsFromMe {
		// If message is from me, participantJID should be my own JID
		if w.client.Store != nil && w.client.Store.ID != nil {
			participantJID = *w.client.Store.ID
		} else {
			participantJID = chatJID
		}
	} else {
		// If message is from someone else, participantJID is the sender's JID
		senderJID, err := types.ParseJID(message.SenderID)
		if err != nil {
			fmt.Printf("WhatsApp: Warning - Could not parse sender ID %s: %v, using chatJID\n", message.SenderID, err)
			participantJID = chatJID
		} else {
			participantJID = senderJID
		}
	}

	// Send read receipt using MarkRead method
	// MarkRead signature: (ctx, messageIDs, timestamp, chatJID, participantJID, receiptType...)
	// participantJID is the JID of the person who sent the message
	err = w.client.MarkRead(w.ctx, []types.MessageID{types.MessageID(messageID)}, time.Now(), chatJID, participantJID, types.ReceiptTypeRead)
	if err != nil {
		return fmt.Errorf("failed to send read receipt: %w", err)
	}

	fmt.Printf("WhatsApp: Sent read receipt for message %s in conversation %s (participant: %s)\n", messageID, conversationID, participantJID.String())
	return nil
}

func (w *WhatsAppProvider) MarkMessageAsPlayed(conversationID string, messageID string) error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.client == nil {
		return fmt.Errorf("client not initialized")
	}

	// Parse conversation ID (JID)
	chatJID, err := types.ParseJID(conversationID)
	if err != nil {
		return fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Get message from database to find the sender
	var message models.Message
	if err := db.DB.Where("protocol_msg_id = ?", messageID).First(&message).Error; err != nil {
		fmt.Printf("WhatsApp: Warning - Could not find message %s in database: %v\n", messageID, err)
		// If message not found, use chatJID as participantJID (fallback)
		err = w.client.MarkRead(w.ctx, []types.MessageID{types.MessageID(messageID)}, time.Now(), chatJID, chatJID, types.ReceiptTypePlayed)
		if err != nil {
			return fmt.Errorf("failed to send played receipt: %w", err)
		}
		fmt.Printf("WhatsApp: Sent played receipt for message %s in conversation %s (using chatJID as participant)\n", messageID, conversationID)
		return nil
	}

	// Determine participantJID based on message sender
	var participantJID types.JID
	if message.IsFromMe {
		// If message is from me, participantJID should be my own JID
		if w.client.Store != nil && w.client.Store.ID != nil {
			participantJID = *w.client.Store.ID
		} else {
			participantJID = chatJID
		}
	} else {
		// If message is from someone else, participantJID is the sender's JID
		senderJID, err := types.ParseJID(message.SenderID)
		if err != nil {
			fmt.Printf("WhatsApp: Warning - Could not parse sender ID %s: %v, using chatJID\n", message.SenderID, err)
			participantJID = chatJID
		} else {
			participantJID = senderJID
		}
	}

	// Send played receipt using MarkRead method
	err = w.client.MarkRead(w.ctx, []types.MessageID{types.MessageID(messageID)}, time.Now(), chatJID, participantJID, types.ReceiptTypePlayed)
	if err != nil {
		return fmt.Errorf("failed to send played receipt: %w", err)
	}

	fmt.Printf("WhatsApp: Sent played receipt for message %s in conversation %s (participant: %s)\n", messageID, conversationID, participantJID.String())
	return nil
}

func (w *WhatsAppProvider) MarkConversationAsRead(conversationID string) error {
	// TODO: Implement marking conversation as read
	markUnused(conversationID)
	return fmt.Errorf("marking conversation as read not yet implemented")
}

func (w *WhatsAppProvider) SendRetryReceipt(conversationID string, messageID string) error {
	// TODO: Implement retry receipts
	markUnused(conversationID, messageID)
	return fmt.Errorf("retry receipts not yet implemented")
}
