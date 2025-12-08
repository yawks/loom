package whatsapp

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"fmt"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func (w *WhatsAppProvider) AddReaction(conversationID string, messageID string, emoji string) error {
	if w.client == nil {
		return fmt.Errorf("client not initialized")
	}

	// Parse conversation ID (JID)
	jid, err := types.ParseJID(conversationID)
	if err != nil {
		return fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Find the message to get its key
	var message *models.Message
	w.mu.RLock()
	if msgs, ok := w.conversationMessages[conversationID]; ok {
		for _, msg := range msgs {
			if msg.ProtocolMsgID == messageID {
				message = &msg
				break
			}
		}
	}
	w.mu.RUnlock()

	// If not found in cache, try database
	if message == nil && db.DB != nil {
		var dbMsg models.Message
		if err := db.DB.Where("protocol_msg_id = ?", messageID).First(&dbMsg).Error; err == nil {
			message = &dbMsg
		}
	}

	if message == nil {
		return fmt.Errorf("message not found: %s", messageID)
	}

	// Parse the message ID to get the key
	msgKey := &waProto.MessageKey{
		RemoteJID: proto.String(conversationID),
		FromMe:    proto.Bool(message.IsFromMe),
		ID:        proto.String(messageID),
	}

	// Create reaction message
	reactionMsg := &waE2E.Message{
		ReactionMessage: &waE2E.ReactionMessage{
			Key:               msgKey,
			Text:              proto.String(emoji),
			GroupingKey:       proto.String(messageID),
			SenderTimestampMS: proto.Int64(time.Now().UnixMilli()),
		},
	}

	// Send reaction
	_, err = w.client.SendMessage(w.ctx, jid, reactionMsg)
	if err != nil {
		return fmt.Errorf("failed to send reaction: %w", err)
	}

	// Get current user ID
	currentUserID := ""
	if w.client.Store != nil && w.client.Store.ID != nil {
		currentUserID = w.client.Store.ID.String()
	}

	// Emit reaction event
	select {
	case w.eventChan <- core.ReactionEvent{
		ConversationID: conversationID,
		MessageID:      messageID,
		UserID:         currentUserID,
		Emoji:          emoji,
		Added:          true,
		Timestamp:      time.Now().Unix(),
	}:
		fmt.Printf("WhatsApp: ReactionEvent emitted successfully for message %s, emoji %s\n", messageID, emoji)
	default:
		fmt.Printf("WhatsApp: WARNING - Failed to emit ReactionEvent (channel full) for message %s\n", messageID)
	}

	return nil
}

func (w *WhatsAppProvider) RemoveReaction(conversationID string, messageID string, emoji string) error {
	if w.client == nil {
		return fmt.Errorf("client not initialized")
	}

	// Parse conversation ID (JID)
	jid, err := types.ParseJID(conversationID)
	if err != nil {
		return fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Find the message to get its key
	var message *models.Message
	w.mu.RLock()
	if msgs, ok := w.conversationMessages[conversationID]; ok {
		for _, msg := range msgs {
			if msg.ProtocolMsgID == messageID {
				message = &msg
				break
			}
		}
	}
	w.mu.RUnlock()

	// If not found in cache, try database
	if message == nil && db.DB != nil {
		var dbMsg models.Message
		if err := db.DB.Where("protocol_msg_id = ?", messageID).First(&dbMsg).Error; err == nil {
			message = &dbMsg
		}
	}

	if message == nil {
		return fmt.Errorf("message not found: %s", messageID)
	}

	// Parse the message ID to get the key
	msgKey := &waProto.MessageKey{
		RemoteJID: proto.String(conversationID),
		FromMe:    proto.Bool(message.IsFromMe),
		ID:        proto.String(messageID),
	}

	// Create reaction message with empty text to remove reaction
	reactionMsg := &waE2E.Message{
		ReactionMessage: &waE2E.ReactionMessage{
			Key:               msgKey,
			Text:              proto.String(""), // Empty text removes the reaction
			GroupingKey:       proto.String(messageID),
			SenderTimestampMS: proto.Int64(time.Now().UnixMilli()),
		},
	}

	// Send reaction removal
	_, err = w.client.SendMessage(w.ctx, jid, reactionMsg)
	if err != nil {
		return fmt.Errorf("failed to remove reaction: %w", err)
	}

	// Get current user ID
	currentUserID := ""
	if w.client.Store != nil && w.client.Store.ID != nil {
		currentUserID = w.client.Store.ID.String()
	}

	// Emit reaction event
	select {
	case w.eventChan <- core.ReactionEvent{
		ConversationID: conversationID,
		MessageID:      messageID,
		UserID:         currentUserID,
		Emoji:          emoji,
		Added:          false,
		Timestamp:      time.Now().Unix(),
	}:
		fmt.Printf("WhatsApp: ReactionEvent emitted successfully for removed reaction on message %s, emoji %s\n", messageID, emoji)
	default:
		fmt.Printf("WhatsApp: WARNING - Failed to emit ReactionEvent (channel full) for message %s\n", messageID)
	}

	return nil
}
