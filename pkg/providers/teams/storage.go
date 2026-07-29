package teams

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/mautrix-teams/pkg/msteams"
	"gorm.io/gorm"
)

func (p *Provider) storeConversation(account models.LinkedAccount) error {
	if db.DB == nil {
		return nil
	}
	var stored models.LinkedAccount
	result := db.DB.Where(
		"provider_instance_id = ? AND user_id = ?",
		account.ProviderInstanceID, account.UserID,
	).First(&stored)
	if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
		return fmt.Errorf("%s: find conversation account: %w", providerID, result.Error)
	}
	if result.Error == gorm.ErrRecordNotFound {
		meta := models.MetaContact{DisplayName: account.Username, AvatarURL: account.AvatarURL}
		if err := db.DB.Create(&meta).Error; err != nil {
			return fmt.Errorf("%s: create conversation contact: %w", providerID, err)
		}
		db.ContactStore.UpsertMetaContact(meta)
		account.MetaContactID = meta.ID
		if err := db.DB.Create(&account).Error; err != nil {
			return fmt.Errorf("%s: store conversation account: %w", providerID, err)
		}
		stored = account
	} else {
		stored.Username = account.Username
		stored.AvatarURL = account.AvatarURL
		stored.IsGroup = account.IsGroup
		stored.Status = account.Status
		if err := db.DB.Save(&stored).Error; err != nil {
			return fmt.Errorf("%s: update conversation account: %w", providerID, err)
		}
		if stored.MetaContactID != 0 {
			var meta models.MetaContact
			if err := db.DB.First(&meta, stored.MetaContactID).Error; err == nil {
				meta.DisplayName = account.Username
				meta.AvatarURL = account.AvatarURL
				if err := db.DB.Save(&meta).Error; err != nil {
					return fmt.Errorf("%s: update conversation contact: %w", providerID, err)
				}
				db.ContactStore.UpsertMetaContact(meta)
			}
		}
	}
	db.ContactStore.UpsertLinkedAccount(stored)

	nsConvID := core.BuildConvID(p.instance, account.ConversationID)
	var conversation models.Conversation
	result = db.DB.Where("protocol_conv_id = ?", nsConvID).First(&conversation)
	if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
		return fmt.Errorf("%s: find conversation: %w", providerID, result.Error)
	}
	if result.Error == gorm.ErrRecordNotFound {
		conversation = models.Conversation{
			LinkedAccountID: stored.ID, ProtocolConvID: nsConvID,
			IsGroup: account.IsGroup,
		}
		if account.IsGroup {
			conversation.GroupName = account.Username
		}
		if err := db.DB.Create(&conversation).Error; err != nil {
			return fmt.Errorf("%s: create conversation: %w", providerID, err)
		}
	} else if conversation.IsGroup {
		conversation.GroupName = account.Username
		if err := db.DB.Save(&conversation).Error; err != nil {
			return fmt.Errorf("%s: update conversation name: %w", providerID, err)
		}
	}
	db.ContactStore.UpsertConversation(stored.ID, conversation.ProtocolConvID)
	return nil
}

func (p *Provider) storeMessages(messages []models.Message) error {
	if db.DB == nil {
		return nil
	}
	return db.DB.Transaction(func(tx *gorm.DB) error {
		for index := range messages {
			message := &messages[index]
			if message.ProtocolMsgID == "" || message.ProtocolConvID == "" {
				continue
			}
			var conversation models.Conversation
			if err := tx.Where("protocol_conv_id = ?", message.ProtocolConvID).First(&conversation).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					continue
				}
				return err
			}
			message.ConversationID = conversation.ID

			var stored models.Message
			err := tx.Unscoped().Where(
				"protocol_msg_id = ? AND protocol_conv_id = ?",
				message.ProtocolMsgID, message.ProtocolConvID,
			).First(&stored).Error
			switch err {
			case nil:
				stored.Body = message.Body
				stored.SenderID = message.SenderID
				stored.SenderName = message.SenderName
				stored.SenderAvatarURL = message.SenderAvatarURL
				stored.Attachments = message.Attachments
				stored.ThreadID = message.ThreadID
				stored.QuotedMessageID = message.QuotedMessageID
				stored.QuotedSenderID = message.QuotedSenderID
				stored.QuotedSenderName = message.QuotedSenderName
				stored.QuotedBody = message.QuotedBody
				stored.CallType = message.CallType
				stored.CallDurationSecs = message.CallDurationSecs
				stored.CallParticipants = message.CallParticipants
				stored.CallOutcome = message.CallOutcome
				stored.CallIsVideo = message.CallIsVideo
				stored.CallUrl = message.CallUrl
				stored.IsEdited = stored.IsEdited || message.IsEdited
				stored.IsDeleted = stored.IsDeleted || message.IsDeleted
				if message.EditedTimestamp != nil {
					stored.EditedTimestamp = message.EditedTimestamp
				}
				if message.DeletedTimestamp != nil {
					stored.DeletedTimestamp = message.DeletedTimestamp
				}
				if err := tx.Save(&stored).Error; err != nil {
					return err
				}
				message.ID = stored.ID
			case gorm.ErrRecordNotFound:
				// protocol_msg_id has a global unique index. A soft-deleted row
				// or an exceptional Teams ID reused in another conversation
				// must not make the whole synchronization fail.
				var conflicting models.Message
				if err := tx.Unscoped().
					Where("protocol_msg_id = ?", message.ProtocolMsgID).
					First(&conflicting).Error; err == nil {
					continue
				} else if err != gorm.ErrRecordNotFound {
					return err
				}
				if message.Timestamp.IsZero() {
					message.Timestamp = time.Now()
				}
				if err := tx.Create(message).Error; err != nil {
					return err
				}
			default:
				return err
			}
		}
		return nil
	})
}

func (p *Provider) enrichReplyMetadata(messages []models.Message) {
	byID := make(map[string]*models.Message, len(messages))
	for index := range messages {
		byID[messages[index].ProtocolMsgID] = &messages[index]
	}
	for index := range messages {
		message := &messages[index]
		if message.QuotedMessageID == nil || *message.QuotedMessageID == "" {
			continue
		}
		parent := byID[*message.QuotedMessageID]
		var stored models.Message
		if parent == nil && db.DB != nil {
			if err := db.DB.Where(
				"protocol_msg_id = ? AND protocol_conv_id = ?",
				*message.QuotedMessageID, message.ProtocolConvID,
			).First(&stored).Error; err == nil {
				parent = &stored
			}
		}
		if parent == nil {
			continue
		}
		body := parent.Body
		if looksLikeTeamsHTML(body) {
			body = teamsHTMLToMarkdown(
				msteams.StripAMSAttachments(msteams.StripReplyBlockquote(body)),
			)
		}
		message.QuotedBody = &body
		senderID := parent.SenderID
		message.QuotedSenderID = &senderID
		message.QuotedSenderName = parent.SenderName
	}
}

func (p *Provider) repairStoredHTMLFormatting() error {
	if db.DB == nil {
		return nil
	}
	var stored []models.Message
	if err := db.DB.Where(
		"protocol_conv_id LIKE ? AND body LIKE '%<%'",
		p.instance+"::%",
	).Find(&stored).Error; err != nil {
		return fmt.Errorf("%s: find stored HTML messages: %w", providerID, err)
	}
	converted := make([]bool, len(stored))
	for index := range stored {
		message := &stored[index]
		if !looksLikeTeamsHTML(message.Body) {
			continue
		}
		converted[index] = true
		rawBody := message.Body
		parentID := msteams.ExtractReplyParent(rawBody)
		cleanBody := msteams.StripAMSAttachments(msteams.StripReplyBlockquote(rawBody))
		message.Body = teamsHTMLToMarkdown(cleanBody)
		message.ThreadID = nil
		if parentID != "" {
			message.QuotedMessageID = &parentID
		}
	}
	p.enrichReplyMetadata(stored)
	for index := range stored {
		if !converted[index] {
			continue
		}
		message := &stored[index]
		if err := db.DB.Model(&models.Message{}).Where("id = ?", message.ID).Updates(map[string]any{
			"body":               message.Body,
			"thread_id":          message.ThreadID,
			"quoted_message_id":  message.QuotedMessageID,
			"quoted_sender_id":   message.QuotedSenderID,
			"quoted_sender_name": message.QuotedSenderName,
			"quoted_body":        message.QuotedBody,
		}).Error; err != nil {
			return fmt.Errorf("%s: repair stored HTML message: %w", providerID, err)
		}
	}
	return nil
}

func (p *Provider) repairStoredSenderNames(client *msteams.Client, conversationID string, members []msteams.Member) error {
	if db.DB == nil {
		return nil
	}
	for _, member := range members {
		if member.MRI == "" {
			continue
		}
		name := client.CachedDisplayName(member.MRI)
		if strings.EqualFold(member.MRI, client.UserMRI()) {
			name = p.displayName()
		}
		if name == "" || name == member.MRI {
			continue
		}
		if err := db.DB.Model(&models.Message{}).
			Where(
				"protocol_conv_id = ? AND sender_id = ? AND (sender_name = '' OR sender_name = sender_id)",
				conversationID, member.MRI,
			).
			Update("sender_name", name).Error; err != nil {
			return fmt.Errorf("%s: repair sender name: %w", providerID, err)
		}
	}
	return nil
}

func (p *Provider) removeVirtualConversations() error {
	if db.DB == nil {
		return nil
	}
	virtualIDs := []string{"48:calllogs", "48:mentions", "48:notifications", "48:notes"}
	var deletedAccountIDs []uint
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		var accounts []models.LinkedAccount
		if err := tx.Where(
			"provider_instance_id = ? AND user_id IN ?",
			p.instance, virtualIDs,
		).Find(&accounts).Error; err != nil {
			return err
		}
		for _, account := range accounts {
			var conversations []models.Conversation
			if err := tx.Unscoped().Where("linked_account_id = ?", account.ID).Find(&conversations).Error; err != nil {
				return err
			}
			for _, conversation := range conversations {
				var messageIDs []uint
				if err := tx.Unscoped().Model(&models.Message{}).
					Where("conversation_id = ?", conversation.ID).
					Pluck("id", &messageIDs).Error; err != nil {
					return err
				}
				if len(messageIDs) > 0 {
					if err := tx.Where("message_id IN ?", messageIDs).Delete(&models.Reaction{}).Error; err != nil {
						return err
					}
					if err := tx.Where("message_id IN ?", messageIDs).Delete(&models.MessageReceipt{}).Error; err != nil {
						return err
					}
				}
				if err := tx.Unscoped().Where("conversation_id = ?", conversation.ID).Delete(&models.Message{}).Error; err != nil {
					return err
				}
				if err := tx.Where("conversation_id = ?", conversation.ID).Delete(&models.GroupParticipant{}).Error; err != nil {
					return err
				}
				if err := tx.Unscoped().Delete(&conversation).Error; err != nil {
					return err
				}
			}
			if err := tx.Delete(&account).Error; err != nil {
				return err
			}
			deletedAccountIDs = append(deletedAccountIDs, account.ID)
			if account.MetaContactID != 0 {
				var count int64
				if err := tx.Model(&models.LinkedAccount{}).
					Where("meta_contact_id = ?", account.MetaContactID).
					Count(&count).Error; err != nil {
					return err
				}
				if count == 0 {
					if err := tx.Delete(&models.MetaContact{}, account.MetaContactID).Error; err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, accountID := range deletedAccountIDs {
		db.ContactStore.DeleteLinkedAccount(accountID)
		db.ContactStore.SetConversation(accountID, "")
	}
	return nil
}
