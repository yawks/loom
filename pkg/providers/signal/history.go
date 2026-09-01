package signal

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.mau.fi/mautrix-signal/pkg/msgconv"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/backuppb"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/store"
	"gorm.io/gorm"
)

// syncTransferHistory consumes Signal's one-shot device-link transfer archive.
// Contact sync alone never contains conversations or old messages.
func (p *Provider) syncTransferHistory(since time.Time) error {
	p.transferMu.Lock()
	defer p.transferMu.Unlock()

	p.mu.RLock()
	client := p.client
	ctx := p.ctx
	p.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("signal is not paired")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	p.emit(core.SyncStatusEvent{InstanceID: p.instanceID(), Status: core.SyncStatusFetchingHistory, Message: "Downloading Signal conversation history", Progress: 5})
	if client.Store.EphemeralBackupKey != nil {
		meta, err := client.WaitForTransfer(ctx)
		if err != nil {
			return fmt.Errorf("wait for Signal history transfer: %w", err)
		}
		if err = client.FetchAndProcessTransfer(ctx, meta); err != nil {
			return fmt.Errorf("import Signal history transfer: %w", err)
		}
	}

	chats, err := client.Store.BackupStore.GetBackupChats(ctx)
	if err != nil {
		return fmt.Errorf("load Signal conversations: %w", err)
	}
	for index, chat := range chats {
		if err = p.importBackupChat(ctx, chat, since); err != nil {
			return err
		}
		progress := 10 + (index+1)*85/max(1, len(chats))
		p.emit(core.SyncStatusEvent{InstanceID: p.instanceID(), Status: core.SyncStatusFetchingHistory, Message: "Importing Signal conversations", Progress: progress})
	}
	p.emit(core.SyncStatusEvent{InstanceID: p.instanceID(), Status: core.SyncStatusCompleted, Message: "Signal synchronization complete", Progress: 100})
	return nil
}

func (p *Provider) importBackupChat(ctx context.Context, chat *store.BackupChat, since time.Time) error {
	client := p.client
	recipient, err := client.Store.BackupStore.GetBackupRecipient(ctx, chat.GetRecipientId())
	if err != nil || recipient == nil {
		return err
	}
	conversationID, conversationName, isGroup := backupConversationIdentity(recipient)
	if recipient.GetSelf() != nil {
		conversationID = client.Store.ACI.String()
	}
	if conversationID == "" {
		return nil
	}

	items, err := client.Store.BackupStore.GetBackupChatItems(ctx, chat.GetId(), time.Time{}, false, max(1, chat.TotalMessages))
	if err != nil {
		return fmt.Errorf("load Signal conversation %s: %w", conversationID, err)
	}
	slices.Reverse(items)
	// last_sync_at may already have been advanced by an older Loom build that
	// downloaded the archive but failed to materialize it. A missing canonical
	// conversation therefore means first import, regardless of that timestamp.
	firstCanonicalImport := true
	if db.DB != nil {
		var count int64
		db.DB.Model(&models.Conversation{}).
			Where("protocol_conv_id = ?", core.BuildConvID(p.instanceID(), conversationID)).
			Count(&count)
		firstCanonicalImport = count == 0
	}
	messages := make([]models.Message, 0, len(items))
	for _, item := range items {
		// Keep the newest item even when the requested window is newer, so a
		// dormant conversation is still discoverable in Loom.
		if !firstCanonicalImport && !since.IsZero() && time.UnixMilli(int64(item.DateSent)).Before(since) && item != items[len(items)-1] {
			continue
		}
		message, ok := p.backupItemToMessage(ctx, conversationID, item)
		if ok {
			messages = append(messages, message)
			p.remember(message)
		}
	}
	if len(messages) > 0 {
		conv := core.BuildConvID(p.instanceID(), conversationID)
		if err = p.persistCanonicalMessages(conversationID, conversationName, isGroup, messages); err != nil {
			return fmt.Errorf("persist Signal conversation %s: %w", conversationID, err)
		}
		p.emit(core.MessageBatchEvent{InstanceID: p.instanceID(), ConversationID: conv, Messages: messages, IsHistorical: true, ForceRead: true})
	}
	return nil
}

// persistCanonicalMessages materializes signalmeow's protocol store into Loom's
// canonical database. The UI intentionally reads only canonical contacts and
// conversations, so emitting an event is not sufficient (and events can be
// missed while the frontend is mounting).
func (p *Provider) persistCanonicalMessages(rawConversationID, name string, isGroup bool, messages []models.Message) error {
	if db.DB == nil || rawConversationID == "" {
		return nil
	}
	instanceID := p.instanceID()
	protocolConversationID := core.BuildConvID(instanceID, rawConversationID)
	if name == "" {
		name = rawConversationID
	}

	var meta models.MetaContact
	var account models.LinkedAccount
	var conversation models.Conversation
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("provider_instance_id = ? AND user_id = ?", instanceID, rawConversationID).First(&account)
		if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
			return result.Error
		}
		if result.Error == gorm.ErrRecordNotFound {
			meta = models.MetaContact{DisplayName: name}
			if err := tx.Create(&meta).Error; err != nil {
				return err
			}
			account = models.LinkedAccount{
				MetaContactID: meta.ID, Protocol: "signal", ProviderInstanceID: instanceID,
				UserID: rawConversationID, Username: name, IsGroup: isGroup, Status: "offline",
			}
			if err := tx.Create(&account).Error; err != nil {
				return err
			}
		} else {
			account.Protocol = "signal"
			account.IsGroup = isGroup
			if account.Username == "" || account.Username == account.UserID {
				account.Username = name
			}
			if err := tx.Save(&account).Error; err != nil {
				return err
			}
			if account.MetaContactID != 0 {
				_ = tx.First(&meta, account.MetaContactID).Error
			}
		}

		result = tx.Where("protocol_conv_id = ?", protocolConversationID).First(&conversation)
		if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
			return result.Error
		}
		if result.Error == gorm.ErrRecordNotFound {
			conversation = models.Conversation{
				LinkedAccountID: account.ID, ProtocolConvID: protocolConversationID,
				IsGroup: isGroup, GroupName: map[bool]string{true: name}[isGroup],
			}
			if err := tx.Create(&conversation).Error; err != nil {
				return err
			}
		}

		for index := range messages {
			messages[index].ConversationID = conversation.ID
			messages[index].ProtocolConvID = protocolConversationID
			var existing models.Message
			result = tx.Where("protocol_msg_id = ?", messages[index].ProtocolMsgID).First(&existing)
			if result.Error == gorm.ErrRecordNotFound {
				if err := tx.Create(&messages[index]).Error; err != nil {
					return err
				}
			} else if result.Error != nil {
				return result.Error
			} else if existing.ConversationID == 0 || existing.ProtocolConvID == "" {
				if err := tx.Model(&existing).Updates(map[string]any{
					"conversation_id": conversation.ID, "protocol_conv_id": protocolConversationID,
				}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if meta.ID != 0 {
		db.ContactStore.UpsertMetaContact(meta)
	}
	db.ContactStore.UpsertLinkedAccount(account)
	db.ContactStore.UpsertConversation(account.ID, protocolConversationID)
	return nil
}

func (p *Provider) backupItemToMessage(ctx context.Context, conversationID string, item *backuppb.ChatItem) (models.Message, bool) {
	client := p.client
	senderRecipient, err := client.Store.BackupStore.GetBackupRecipient(ctx, item.AuthorId)
	if err != nil || senderRecipient == nil {
		return models.Message{}, false
	}
	senderID, senderName, _ := backupConversationIdentity(senderRecipient)
	isFromMe := senderRecipient.GetSelf() != nil
	if isFromMe {
		senderID = client.Store.ACI.String()
		senderName = ""
	}
	dm, _ := msgconv.BackupToDataMessage(item, make(msgconv.AttachmentMap))
	if dm == nil {
		return models.Message{}, false
	}
	m := models.Message{
		ProtocolConvID: core.BuildConvID(p.instanceID(), conversationID),
		ProtocolMsgID:  fmt.Sprintf("%s|%d", senderID, item.DateSent),
		SenderID:       senderID,
		SenderName:     senderName,
		Body:           dm.GetBody(),
		Timestamp:      time.UnixMilli(int64(item.DateSent)),
		IsFromMe:       isFromMe,
	}
	if quote := item.GetStandardMessage().GetQuote(); quote != nil {
		id := fmt.Sprintf("%d", quote.GetTargetSentTimestamp())
		m.QuotedMessageID = &id
	}
	if len(dm.Attachments) > 0 {
		attachments := make([]map[string]any, 0, len(dm.Attachments))
		for _, pointer := range dm.Attachments {
			mimeType := pointer.GetContentType()
			kind := "file"
			if strings.HasPrefix(mimeType, "image/") {
				kind = "image"
			} else if strings.HasPrefix(mimeType, "video/") {
				kind = "video"
			} else if strings.HasPrefix(mimeType, "audio/") {
				kind = "audio"
			}
			attachments = append(attachments, map[string]any{"type": kind, "mimeType": mimeType, "fileName": pointer.GetFileName(), "fileSize": pointer.GetSize()})
		}
		encoded, _ := json.Marshal(attachments)
		m.Attachments = string(encoded)
	}
	return m, true
}
