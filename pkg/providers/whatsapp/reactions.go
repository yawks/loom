package whatsapp

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"fmt"
	"strings"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

// reactionMessage is shared by additions and removals: both must identify the
// original author for incoming group messages, not just the chat and message ID.
func (w *WhatsAppProvider) reactionMessage(jid types.JID, messageID, emoji string) (*waE2E.Message, error) {
	nsConvID := core.BuildConvID(w.getInstanceId(), jid.String())
	var message *models.Message
	w.mu.RLock()
	if msgs, ok := w.conversationMessages[nsConvID]; ok {
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
		if err := db.ForProvider(db.DB, w.getInstanceId()).MessagesForConversation(nsConvID).
			Where("protocol_msg_id = ?", messageID).First(&dbMsg).Error; err == nil {
			message = &dbMsg
		}
	}

	if message == nil {
		return nil, fmt.Errorf("message not found: %s", messageID)
	}

	sender := types.EmptyJID
	if !message.IsFromMe {
		var err error
		sender, err = types.ParseJID(message.SenderID)
		if err != nil || sender.User == "" || sender.Server == "" {
			return nil, fmt.Errorf("invalid sender for reaction target %s: %q", messageID, message.SenderID)
		}
	}
	return w.client.BuildReaction(jid, sender, messageID, emoji), nil
}

func (w *WhatsAppProvider) AddReaction(conversationID string, messageID string, emoji string) error {
	if w.client == nil {
		return fmt.Errorf("client not initialized")
	}

	rawConvID := core.StripConvID(conversationID)
	nsConvID := core.BuildConvID(w.getInstanceId(), rawConvID)
	jid, err := types.ParseJID(rawConvID)
	if err != nil {
		return fmt.Errorf("invalid conversation ID: %w", err)
	}
	reactionMsg, err := w.reactionMessage(jid, messageID, strings.Trim(emoji, ":"))
	if err != nil {
		return err
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
	case w.eventChan <- core.ReactionEvent{InstanceID: w.getInstanceId(),
		ConversationID: nsConvID,
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

	rawConvID := core.StripConvID(conversationID)
	nsConvID := core.BuildConvID(w.getInstanceId(), rawConvID)

	// Parse conversation ID (JID)
	jid, err := types.ParseJID(rawConvID)
	if err != nil {
		return fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Empty text removes the reaction, using the same target key as additions.
	reactionMsg, err := w.reactionMessage(jid, messageID, "")
	if err != nil {
		return err
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
	case w.eventChan <- core.ReactionEvent{InstanceID: w.getInstanceId(),
		ConversationID: nsConvID,
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
