// Package googlemessages adapts Google Messages for Loom using mautrix/libgm.
package googlemessages

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"Loom/pkg/providers/messageformat"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

const providerID = "googlemessages"

// Provider implements the Google Messages web protocol. libgm manages the
// encrypted phone relay session; Loom owns the per-instance session file.
type Provider struct {
	unsupportedProvider

	mu        sync.RWMutex
	config    core.ProviderConfig
	client    *libgm.Client
	auth      *libgm.AuthData
	pairing   *libgm.PairingSession
	eventChan chan core.ProviderEvent
	instance  string
	emoji     string
}

var _ core.Provider = (*Provider)(nil)

func NewProvider() *Provider {
	return &Provider{eventChan: make(chan core.ProviderEvent, 500), config: make(core.ProviderConfig)}
}

func (p *Provider) Init(config core.ProviderConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config = config
	p.instance, _ = config.GetString("_instance_id")
	if p.instance == "" {
		p.instance = providerID + "-1"
	}
	p.auth = libgm.NewAuthData()
	if err := p.loadAuthLocked(); err != nil {
		return err
	}
	p.newClientLocked()
	return nil
}

func (p *Provider) newClientLocked() {
	p.client = libgm.NewClient(p.auth, nil, zerolog.Nop())
	p.client.SetEventHandler(p.handleLibGMEvent)
}

func (p *Provider) GetConfig() core.ProviderConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	copy := make(core.ProviderConfig, len(p.config))
	for key, value := range p.config {
		copy[key] = value
	}
	return copy
}

func (p *Provider) SetConfig(config core.ProviderConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if config == nil {
		return fmt.Errorf("%s: configuration cannot be nil", providerID)
	}
	p.config = config
	if instance, ok := config.GetString("_instance_id"); ok && instance != "" {
		p.instance = instance
	}
	return nil
}

func (p *Provider) IsAuthenticated() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.auth != nil && p.auth.Browser != nil && len(p.auth.TachyonAuthToken) > 0
}

// Connect restores an existing Google-account pairing. New pairings must use
// StartGoogleAccountPairing: Google has removed the QR pairing flow.
func (p *Provider) Connect() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client == nil {
		return fmt.Errorf("%s: provider is not initialized", providerID)
	}
	if p.auth.Browser != nil && len(p.auth.TachyonAuthToken) > 0 {
		if err := p.client.Connect(); err != nil {
			return fmt.Errorf("%s: connect: %w", providerID, err)
		}
		return nil
	}
	return fmt.Errorf("%s: Google account pairing is required", providerID)
}

func (p *Provider) Disconnect() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil {
		p.client.Disconnect()
	}
	return nil
}

func (p *Provider) SyncHistory(since time.Time) error {
	p.mu.RLock()
	client := p.client
	instance := p.instance
	p.mu.RUnlock()
	if client == nil || !p.IsAuthenticated() {
		return fmt.Errorf("%s: not authenticated", providerID)
	}
	p.emit(core.SyncStatusEvent{InstanceID: instance, Status: core.SyncStatusFetchingContacts, Message: "Fetching Google Messages conversations", Progress: 0})
	response, err := client.ListConversations(1000, gmproto.ListConversationsRequest_INBOX)
	if err != nil {
		p.emit(core.SyncStatusEvent{InstanceID: instance, Status: core.SyncStatusError, Message: "Unable to fetch Google Messages conversations", Progress: -1})
		return fmt.Errorf("%s: list conversations: %w", providerID, err)
	}
	conversations := response.GetConversations()
	for index, remote := range conversations {
		if remote.GetConversationID() == "" {
			continue
		}
		if err := p.storeConversation(remote); err != nil {
			return err
		}
		progress := (index * 100) / max(1, len(conversations))
		p.emit(core.SyncStatusEvent{InstanceID: instance, Status: core.SyncStatusFetchingHistory, ConversationID: remote.GetConversationID(), Message: "Syncing Google Messages history", Progress: progress})
		messages, err := p.GetConversationHistory(remote.GetConversationID(), 0, nil, &since)
		if err != nil {
			return err
		}
		if err := p.storeMessages(messages); err != nil {
			return err
		}
		if len(messages) > 0 {
			p.emit(core.MessageBatchEvent{InstanceID: instance, ConversationID: remote.GetConversationID(), Messages: messages})
		}
	}
	p.emit(core.SyncStatusEvent{InstanceID: instance, Status: core.SyncStatusCompleted, Message: "Google Messages sync completed", Progress: 100})
	p.emit(core.ContactStatusEvent{InstanceID: instance, UserID: "refresh", Status: "new_conversations_discovered"})
	return nil
}

func (p *Provider) GetContacts() ([]models.LinkedAccount, error) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil || !p.IsAuthenticated() {
		return nil, fmt.Errorf("%s: not authenticated", providerID)
	}
	response, err := client.ListConversations(1000, gmproto.ListConversationsRequest_INBOX)
	if err != nil {
		return nil, fmt.Errorf("%s: list conversations: %w", providerID, err)
	}
	contacts := make([]models.LinkedAccount, 0, len(response.GetConversations()))
	for _, remote := range response.GetConversations() {
		contacts = append(contacts, p.linkedAccount(remote))
	}
	return contacts, nil
}

func (p *Provider) GetConversationHistory(conversationID string, limit int, beforeTimestamp *time.Time, sinceTimestamp *time.Time) ([]models.Message, error) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil || !p.IsAuthenticated() {
		return nil, fmt.Errorf("%s: not authenticated", providerID)
	}
	if limit < 0 {
		return nil, fmt.Errorf("%s: limit must not be negative", providerID)
	}
	const pageSize int64 = 100
	maxMessages := limit
	if maxMessages == 0 {
		maxMessages = 2000 // bounded best-effort backfill; SyncHistory is called repeatedly.
	}
	var cursor *gmproto.Cursor
	messages := make([]models.Message, 0, min(maxMessages, 200))
	dmSenderName := p.dmSenderName(conversationID)
	for len(messages) < maxMessages {
		response, err := client.FetchMessages(conversationID, pageSize, cursor)
		if err != nil {
			return nil, fmt.Errorf("%s: fetch messages for %s: %w", providerID, conversationID, err)
		}
		page := response.GetMessages()
		if len(page) == 0 {
			break
		}
		oldest := time.Time{}
		for _, remote := range page {
			message := p.toModelMessage(remote, conversationID, dmSenderName)
			if message.ProtocolMsgID == "" {
				continue
			}
			if beforeTimestamp != nil && !message.Timestamp.Before(*beforeTimestamp) {
				continue
			}
			if sinceTimestamp != nil && message.Timestamp.Before(*sinceTimestamp) {
				oldest = message.Timestamp
				continue
			}
			messages = append(messages, message)
			if oldest.IsZero() || message.Timestamp.Before(oldest) {
				oldest = message.Timestamp
			}
			if len(messages) >= maxMessages {
				break
			}
		}
		if len(messages) >= maxMessages || (sinceTimestamp != nil && !oldest.IsZero() && oldest.Before(*sinceTimestamp)) {
			break
		}
		next := response.GetCursor()
		if next == nil || next.GetLastItemID() == "" || (cursor != nil && next.GetLastItemID() == cursor.GetLastItemID()) {
			break
		}
		cursor = next
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i].Timestamp.Before(messages[j].Timestamp) })
	return messages, nil
}

func (p *Provider) linkedAccount(remote *gmproto.Conversation) models.LinkedAccount {
	p.mu.RLock()
	instance := p.instance
	p.mu.RUnlock()
	name := remote.GetName()
	if name == "" {
		name = remote.GetConversationID()
	}
	// Google Messages does not expose contact presence. "offline" is Loom's
	// neutral/no-presence value and intentionally suppresses the status badge.
	return models.LinkedAccount{Protocol: providerID, ProviderInstanceID: instance, UserID: remote.GetConversationID(), Username: name, AvatarURL: remote.GetGroupAvatarURL(), IsGroup: remote.GetIsGroupChat(), Status: "offline", ConversationID: remote.GetConversationID()}
}

func (p *Provider) storeConversation(remote *gmproto.Conversation) error {
	if db.DB == nil {
		return nil
	}
	account := p.linkedAccount(remote)
	var storedAccount models.LinkedAccount
	result := db.DB.Where("provider_instance_id = ? AND user_id = ?", account.ProviderInstanceID, account.UserID).First(&storedAccount)
	if result.Error != nil {
		meta := models.MetaContact{DisplayName: account.Username, AvatarURL: account.AvatarURL}
		if err := db.DB.Create(&meta).Error; err != nil {
			return fmt.Errorf("%s: create conversation contact: %w", providerID, err)
		}
		db.ContactStore.UpsertMetaContact(meta)
		account.MetaContactID = meta.ID
		if err := db.DB.Create(&account).Error; err != nil {
			return fmt.Errorf("%s: store conversation account: %w", providerID, err)
		}
		storedAccount = account
	} else {
		if storedAccount.MetaContactID == 0 {
			meta := models.MetaContact{DisplayName: account.Username, AvatarURL: account.AvatarURL}
			if err := db.DB.Create(&meta).Error; err != nil {
				return fmt.Errorf("%s: create missing conversation contact: %w", providerID, err)
			}
			db.ContactStore.UpsertMetaContact(meta)
			storedAccount.MetaContactID = meta.ID
		}
		storedAccount.Username, storedAccount.AvatarURL, storedAccount.IsGroup, storedAccount.Status = account.Username, account.AvatarURL, account.IsGroup, account.Status
		if err := db.DB.Save(&storedAccount).Error; err != nil {
			return err
		}
	}
	// Google Messages conversation titles are authoritative. The message view
	// resolves its title through MetaContact, so update it with the same value.
	if storedAccount.MetaContactID != 0 {
		var meta models.MetaContact
		if err := db.DB.First(&meta, storedAccount.MetaContactID).Error; err == nil {
			meta.DisplayName, meta.AvatarURL = storedAccount.Username, storedAccount.AvatarURL
			if err := db.DB.Save(&meta).Error; err != nil {
				return err
			}
			db.ContactStore.UpsertMetaContact(meta)
		}
	}
	// In a DM, the conversation title is Google's authoritative contact name.
	// SenderParticipant.FullName can be stale or refer to another cached contact,
	// so also repair messages that were persisted before this metadata arrived.
	if !storedAccount.IsGroup && storedAccount.Username != "" && storedAccount.Username != storedAccount.UserID {
		nsConvID := core.BuildConvID(p.instance, remote.GetConversationID())
		if err := db.DB.Model(&models.Message{}).
			Where("protocol_conv_id = ? AND is_from_me = ?", nsConvID, false).
			Update("sender_name", storedAccount.Username).Error; err != nil {
			return err
		}
	}
	db.ContactStore.UpsertLinkedAccount(storedAccount)
	nsConvID := core.BuildConvID(p.instance, remote.GetConversationID())
	var conversation models.Conversation
	result = db.DB.Where("protocol_conv_id = ?", nsConvID).First(&conversation)
	if result.Error != nil {
		conversation = models.Conversation{LinkedAccountID: storedAccount.ID, ProtocolConvID: nsConvID, IsGroup: remote.GetIsGroupChat(), GroupName: remote.GetName(), IsPinned: remote.GetPinned()}
		if err := db.DB.Create(&conversation).Error; err != nil {
			return err
		}
		db.ContactStore.UpsertConversation(storedAccount.ID, conversation.ProtocolConvID)
		return nil
	}
	conversation.LinkedAccountID, conversation.IsGroup, conversation.GroupName, conversation.IsPinned = storedAccount.ID, remote.GetIsGroupChat(), remote.GetName(), remote.GetPinned()
	if err := db.DB.Save(&conversation).Error; err != nil {
		return err
	}
	db.ContactStore.UpsertConversation(storedAccount.ID, conversation.ProtocolConvID)
	return nil
}

func (p *Provider) storeMessages(messages []models.Message) error {
	if db.DB == nil {
		return nil
	}
	for _, message := range messages {
		var existing models.Message
		result := db.DB.Where("protocol_msg_id = ?", message.ProtocolMsgID).First(&existing)
		if result.Error != nil {
			reactions := message.Reactions
			receipts := message.Receipts
			message.Reactions = nil
			message.Receipts = nil
			if err := db.DB.Create(&message).Error; err != nil {
				return err
			}
			for index := range reactions {
				reactions[index].MessageID = message.ID
			}
			if len(reactions) > 0 {
				if err := db.DB.Create(&reactions).Error; err != nil {
					return err
				}
			}
			if err := storeGoogleMessagesReceipts(message.ID, receipts); err != nil {
				return err
			}
		} else {
			existing.Body, existing.Timestamp, existing.SenderID, existing.SenderName, existing.IsFromMe, existing.QuotedMessageID = message.Body, message.Timestamp, message.SenderID, message.SenderName, message.IsFromMe, message.QuotedMessageID
			if message.Attachments != "" {
				existing.Attachments = message.Attachments
			}
			if err := db.DB.Save(&existing).Error; err != nil {
				return err
			}
			if err := db.DB.Where("message_id = ?", existing.ID).Delete(&models.Reaction{}).Error; err != nil {
				return err
			}
			for index := range message.Reactions {
				message.Reactions[index].MessageID = existing.ID
			}
			if len(message.Reactions) > 0 {
				if err := db.DB.Create(&message.Reactions).Error; err != nil {
					return err
				}
			}
			if err := storeGoogleMessagesReceipts(existing.ID, message.Receipts); err != nil {
				return err
			}
		}
	}
	return nil
}

func storeGoogleMessagesReceipts(messageID uint, receipts []models.MessageReceipt) error {
	for _, receipt := range receipts {
		var existing models.MessageReceipt
		result := db.DB.Where(
			"message_id = ? AND user_id = ? AND receipt_type = ?",
			messageID, receipt.UserID, receipt.ReceiptType,
		).First(&existing)
		if result.Error == nil {
			if receipt.Timestamp.After(existing.Timestamp) {
				existing.Timestamp = receipt.Timestamp
				if err := db.DB.Save(&existing).Error; err != nil {
					return err
				}
			}
			continue
		}
		receipt.ID = 0
		receipt.MessageID = messageID
		if err := db.DB.Create(&receipt).Error; err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) toModelMessage(remote *gmproto.Message, fallbackConversationID, dmSenderName string) models.Message {
	rawConvID := remote.GetConversationID()
	if rawConvID == "" {
		rawConvID = core.StripConvID(fallbackConversationID)
	}
	conversationID := core.BuildConvID(p.instance, rawConvID)
	parts := make([]string, 0, len(remote.GetMessageInfo()))
	for _, info := range remote.GetMessageInfo() {
		if content := info.GetMessageContent().GetContent(); content != "" {
			parts = append(parts, content)
		}
	}
	sender := remote.GetSenderParticipant()
	senderID, senderName, fromMe := remote.GetParticipantID(), "", false
	if sender != nil {
		senderID, senderName, fromMe = sender.GetID().GetParticipantID(), sender.GetFullName(), sender.GetIsMe()
	}
	if senderID == "" {
		senderID = remote.GetParticipantID()
	}
	if !fromMe && dmSenderName != "" {
		senderName = dmSenderName
	}
	timestamp := time.UnixMicro(remote.GetTimestamp())
	if remote.GetTimestamp() == 0 {
		timestamp = time.Now()
	}
	message := models.Message{ProtocolMsgID: remote.GetMessageID(), ProtocolConvID: conversationID, SenderID: senderID, SenderName: senderName, Body: strings.Join(parts, "\n"), Timestamp: timestamp, IsFromMe: fromMe, Attachments: p.mediaAttachmentsJSON(remote)}
	if fromMe {
		message.Receipts = googleMessagesReceipts(remote.GetMessageStatus().GetStatus(), conversationID, timestamp)
	}
	for _, entry := range remote.GetReactions() {
		emoji := entry.GetData().GetUnicode()
		if emoji == "" {
			continue
		}
		for _, userID := range entry.GetParticipantIDs() {
			message.Reactions = append(message.Reactions, models.Reaction{UserID: userID, Emoji: emoji})
		}
	}
	if reply := remote.GetReplyMessage(); reply != nil && reply.GetMessageID() != "" {
		message.QuotedMessageID = ptr(reply.GetMessageID())
	}
	return message
}

// dmSenderName returns the authoritative conversation title only for DMs.
// Google Messages participant names are still used for groups.
func (p *Provider) dmSenderName(conversationID string) string {
	if db.DB == nil {
		return ""
	}
	var account models.LinkedAccount
	if err := db.DB.
		Where("provider_instance_id = ? AND user_id = ?", p.instance, core.StripConvID(conversationID)).
		First(&account).Error; err != nil {
		return ""
	}
	if account.IsGroup || account.Username == "" || account.Username == account.UserID {
		return ""
	}
	return account.Username
}

func googleMessagesReceipts(status gmproto.MessageStatusType, conversationID string, timestamp time.Time) []models.MessageReceipt {
	var receiptType string
	switch status {
	case gmproto.MessageStatusType_OUTGOING_DELIVERED:
		receiptType = string(core.ReceiptTypeDelivery)
	case gmproto.MessageStatusType_OUTGOING_DISPLAYED:
		receiptType = string(core.ReceiptTypeRead)
	default:
		return nil
	}
	// Google exposes an aggregate outgoing status rather than one status per RCS
	// participant. The conversation ID represents that remote side.
	return []models.MessageReceipt{{
		UserID:      conversationID,
		ReceiptType: receiptType,
		Timestamp:   timestamp,
	}}
}

// mediaAttachmentsJSON downloads encrypted Google Messages media into Loom's
// private cache. It is deliberately used by both history conversion and live
// events, so a received image follows the same durable path in either case.
func (p *Provider) mediaAttachmentsJSON(remote *gmproto.Message) string {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return ""
	}
	attachments := make([]models.Attachment, 0)
	for _, info := range remote.GetMessageInfo() {
		media := info.GetMediaContent()
		if media == nil || media.GetMediaID() == "" || len(media.GetDecryptionKey()) == 0 {
			continue
		}
		attachment, err := p.downloadMediaAttachment(client, remote.GetMessageID(), media)
		if err == nil {
			attachments = append(attachments, attachment)
		}
	}
	if len(attachments) == 0 {
		return ""
	}
	data, err := json.Marshal(attachments)
	if err != nil {
		return ""
	}
	return string(data)
}

func (p *Provider) downloadMediaAttachment(client *libgm.Client, messageID string, media *gmproto.MediaContent) (models.Attachment, error) {
	mimeType := media.GetMimeType()
	mediaType := libgm.FormatToMediaType[media.GetFormat()]
	if mimeType == "" {
		mimeType = mediaType.Format
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	fileName := filepath.Base(media.GetMediaName())
	if fileName == "." || fileName == "" {
		extension := mediaType.Extension
		if extension == "" {
			extension = "bin"
		}
		fileName = "attachment." + extension
	}
	cacheDir, err := p.mediaCacheDir()
	if err != nil {
		return models.Attachment{}, err
	}
	sum := sha256.Sum256([]byte(messageID + "\x00" + media.GetMediaID()))
	cachePath := filepath.Join(cacheDir, fmt.Sprintf("%x", sum[:]))
	if extension := filepath.Ext(fileName); extension != "" {
		cachePath += extension
	}
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		data, err := client.DownloadMedia(media.GetMediaID(), media.GetDecryptionKey())
		if err != nil {
			return models.Attachment{}, err
		}
		if err := os.WriteFile(cachePath, data, 0600); err != nil {
			return models.Attachment{}, err
		}
	} else if err != nil {
		return models.Attachment{}, err
	}
	attachment := models.Attachment{Type: attachmentTypeFromMIME(mimeType), URL: cachePath, FileName: fileName, FileSize: media.GetSize(), MimeType: mimeType}
	if media.GetThumbnailMediaID() != "" && len(media.GetThumbnailDecryptionKey()) > 0 {
		extension := filepath.Ext(cachePath)
		thumbnailPath := strings.TrimSuffix(cachePath, extension) + ".thumb" + extension
		if _, err := os.Stat(thumbnailPath); os.IsNotExist(err) {
			if data, err := client.DownloadMedia(media.GetThumbnailMediaID(), media.GetThumbnailDecryptionKey()); err == nil {
				_ = os.WriteFile(thumbnailPath, data, 0600)
			}
		}
		if _, err := os.Stat(thumbnailPath); err == nil {
			attachment.Thumbnail = thumbnailPath
		}
	}
	return attachment, nil
}

func (p *Provider) mediaCacheDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("%s: find media cache directory: %w", providerID, err)
	}
	cacheDir := filepath.Join(configDir, "Loom", p.instance, "googlemessages-attachments")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return "", err
	}
	return cacheDir, nil
}

func attachmentTypeFromMIME(mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	default:
		return "document"
	}
}

func ptr(value string) *string { return &value }

func (p *Provider) StreamEvents() (<-chan core.ProviderEvent, error) { return p.eventChan, nil }

func (p *Provider) MarkMessageAsRead(conversationID, messageID string) error {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil || !p.IsAuthenticated() {
		return fmt.Errorf("%s: not authenticated", providerID)
	}
	if conversationID == "" || messageID == "" {
		return fmt.Errorf("%s: conversation ID and message ID are required", providerID)
	}
	if err := client.MarkRead(conversationID, messageID); err != nil {
		return fmt.Errorf("%s: mark message as read: %w", providerID, err)
	}
	return nil
}

func (p *Provider) MarkConversationAsRead(conversationID string) error {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil || !p.IsAuthenticated() {
		return fmt.Errorf("%s: not authenticated", providerID)
	}
	response, err := client.FetchMessages(conversationID, 1, nil)
	if err != nil {
		return fmt.Errorf("%s: fetch latest message for read marker: %w", providerID, err)
	}
	if len(response.GetMessages()) == 0 || response.GetMessages()[0].GetMessageID() == "" {
		return nil
	}
	return p.MarkMessageAsRead(conversationID, response.GetMessages()[0].GetMessageID())
}

func (p *Provider) GetCapabilities() core.Capabilities {
	return core.Capabilities{
		SupportsReactions:     true,
		SupportsDeleteMessage: true,
		NativeEmojiReactions:  true,
	}
}

// SendMessage sends text and/or one media attachment. Google Messages has no
// thread API, therefore threadID is intentionally ignored.
func (p *Provider) SendMessage(conversationID, text string, file *core.Attachment, threadID *string) (*models.Message, error) {
	return p.sendMessage(conversationID, text, file, "")
}

func (p *Provider) SendReply(conversationID, text, quotedMessageID string) (*models.Message, error) {
	if quotedMessageID == "" {
		return nil, fmt.Errorf("%s: quoted message ID is required", providerID)
	}
	return p.sendMessage(conversationID, text, nil, quotedMessageID)
}

func (p *Provider) SendFile(conversationID string, file *core.Attachment, threadID *string) (*models.Message, error) {
	if file == nil {
		return nil, fmt.Errorf("%s: attachment is required", providerID)
	}
	return p.sendMessage(conversationID, "", file, "")
}

func (p *Provider) sendMessage(conversationID, text string, file *core.Attachment, quotedMessageID string) (*models.Message, error) {
	canonicalText := text
	providerText := messageformat.PlainText(text)
	if strings.TrimSpace(text) == "" && file == nil {
		return nil, fmt.Errorf("%s: message text or attachment is required", providerID)
	}
	rawConvID := core.StripConvID(conversationID)
	nsConvID := core.BuildConvID(p.instance, rawConvID)
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil || !p.IsAuthenticated() {
		return nil, fmt.Errorf("%s: not authenticated", providerID)
	}
	conversation, err := client.GetConversation(rawConvID)
	if err != nil {
		return nil, fmt.Errorf("%s: get conversation for sending: %w", providerID, err)
	}
	tmpID := uuid.NewString()
	infos := make([]*gmproto.MessageInfo, 0, 2)
	if providerText != "" {
		infos = append(infos, &gmproto.MessageInfo{Data: &gmproto.MessageInfo_MessageContent{MessageContent: &gmproto.MessageContent{Content: providerText}}})
	}
	if file != nil {
		mimeType := file.MimeType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		media, err := client.UploadMedia(file.Data, file.FileName, mimeType)
		if err != nil {
			return nil, fmt.Errorf("%s: upload attachment: %w", providerID, err)
		}
		infos = append(infos, &gmproto.MessageInfo{Data: &gmproto.MessageInfo_MediaContent{MediaContent: media}})
	}
	request := &gmproto.SendMessageRequest{
		ConversationID: rawConvID,
		MessagePayload: &gmproto.MessagePayload{
			TmpID: tmpID, TmpID2: tmpID, ConversationID: rawConvID,
			ParticipantID: conversation.GetDefaultOutgoingID(), MessageInfo: infos,
		},
		SIMPayload: conversation.GetSimCard().GetSIMData().GetSIMPayload(),
		TmpID:      tmpID,
	}
	if quotedMessageID != "" {
		request.Reply = &gmproto.ReplyPayload{MessageID: quotedMessageID}
	}
	response, err := client.SendMessage(request)
	if err != nil {
		return nil, fmt.Errorf("%s: send message: %w", providerID, err)
	}
	if response.GetStatus() != gmproto.SendMessageResponse_SUCCESS {
		return nil, fmt.Errorf("%s: send message rejected with status %s", providerID, response.GetStatus())
	}
	// Google confirms delivery asynchronously and does not return the final ID.
	// Return a local echo; the event stream later provides the canonical message.
	message := &models.Message{ProtocolMsgID: "temp-" + tmpID, ProtocolConvID: nsConvID, SenderID: conversation.GetDefaultOutgoingID(), Body: canonicalText, Timestamp: time.Now(), IsFromMe: true}
	if quotedMessageID != "" {
		message.QuotedMessageID = ptr(quotedMessageID)
	}
	return message, nil
}

func (p *Provider) AddReaction(conversationID, messageID, emoji string) error {
	return p.sendReaction(conversationID, messageID, emoji, gmproto.SendReactionRequest_ADD)
}

func (p *Provider) RemoveReaction(conversationID, messageID, emoji string) error {
	return p.sendReaction(conversationID, messageID, emoji, gmproto.SendReactionRequest_REMOVE)
}

func (p *Provider) sendReaction(conversationID, messageID, emoji string, action gmproto.SendReactionRequest_Action) error {
	if messageID == "" || emoji == "" {
		return fmt.Errorf("%s: message ID and emoji are required", providerID)
	}
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil || !p.IsAuthenticated() {
		return fmt.Errorf("%s: not authenticated", providerID)
	}
	conversation, err := client.GetConversation(conversationID)
	if err != nil {
		return fmt.Errorf("%s: get conversation for reaction: %w", providerID, err)
	}
	response, err := client.SendReaction(&gmproto.SendReactionRequest{MessageID: messageID, ReactionData: gmproto.MakeReactionData(emoji), Action: action, SIMPayload: conversation.GetSimCard().GetSIMData().GetSIMPayload()})
	if err != nil {
		return fmt.Errorf("%s: send reaction: %w", providerID, err)
	}
	if !response.GetSuccess() {
		return fmt.Errorf("%s: reaction was rejected", providerID)
	}
	return nil
}

func (p *Provider) DeleteMessage(conversationID, messageID string) error {
	if messageID == "" {
		return fmt.Errorf("%s: message ID is required", providerID)
	}
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil || !p.IsAuthenticated() {
		return fmt.Errorf("%s: not authenticated", providerID)
	}
	response, err := client.DeleteMessage(messageID)
	if err != nil {
		return fmt.Errorf("%s: delete message: %w", providerID, err)
	}
	if !response.GetSuccess() {
		return fmt.Errorf("%s: message deletion was rejected", providerID)
	}
	return nil
}

// StartGoogleAccountPairing starts the current Google Messages web pairing
// flow. cookieJSON is intentionally never copied into ProviderConfig.
func (p *Provider) StartGoogleAccountPairing(cookieJSON string) (string, error) {
	var cookies map[string]string
	if err := json.Unmarshal([]byte(cookieJSON), &cookies); err != nil {
		return "", fmt.Errorf("%s: cookies must be a JSON object: %w", providerID, err)
	}
	for _, name := range []string{"SID", "HSID", "SSID", "OSID", "APISID", "SAPISID"} {
		if cookies[name] == "" {
			return "", fmt.Errorf("%s: missing required cookie %s", providerID, name)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client == nil {
		return "", fmt.Errorf("%s: provider is not initialized", providerID)
	}
	p.auth.SetCookies(cookies)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.client.FetchConfig(ctx); err != nil {
		p.auth.SetCookies(nil)
		return "", fmt.Errorf("%s: verify Google session: %w", providerID, err)
	}
	emoji, pairing, err := p.client.StartGaiaPairing(ctx)
	if err != nil {
		p.auth.SetCookies(nil)
		return "", fmt.Errorf("%s: start Google account pairing: %w", providerID, err)
	}
	p.pairing = pairing
	p.emoji = emoji
	return emoji, nil
}

// CompleteGoogleAccountPairing waits for the user to confirm the displayed
// emoji in Google Messages, then persists the private session.
func (p *Provider) CompleteGoogleAccountPairing() error {
	p.mu.Lock()
	if p.pairing == nil || p.client == nil {
		p.mu.Unlock()
		return fmt.Errorf("%s: no Google account pairing is in progress", providerID)
	}
	pairing := p.pairing
	client := p.client
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := client.FinishGaiaPairing(ctx, pairing); err != nil {
		return fmt.Errorf("%s: confirm emoji on phone: %w", providerID, err)
	}

	// FinishGaiaPairing completes the cryptographic state on a temporary client
	// whose pre-pairing long-poll is still active. As in mautrix-gmessages'
	// reference connector, tear it down and reconnect using a fresh client built
	// from the completed AuthData. Reusing it can leave requests on the anonymous
	// polling session and leads to 401 errors on ListConversations.
	client.Disconnect()
	p.mu.Lock()
	p.pairing = nil
	p.emoji = ""
	err := p.saveAuthLocked()
	if err == nil {
		p.newClientLocked()
	}
	reconnectedClient := p.client
	p.mu.Unlock()
	if err != nil {
		return fmt.Errorf("%s: save Google session: %w", providerID, err)
	}
	if err := reconnectedClient.Connect(); err != nil {
		return fmt.Errorf("%s: connect completed Google session: %w", providerID, err)
	}
	return nil
}

func (p *Provider) Cleanup() error {
	_ = p.Disconnect()
	return os.Remove(p.authPath())
}

func (p *Provider) handleLibGMEvent(event any) {
	switch event := event.(type) {
	case *libgm.WrappedMessage:
		if event.IsOld || event.Message == nil || event.GetMessageID() == "" {
			return
		}
		message := p.toModelMessage(event.Message, event.GetConversationID(), p.dmSenderName(event.GetConversationID()))
		previous := p.reactionsForMessage(message.ProtocolMsgID)
		if err := p.storeMessages([]models.Message{message}); err != nil {
			p.emit(core.SyncStatusEvent{InstanceID: p.instance, Status: core.SyncStatusError, Message: "Google Messages message could not be stored", Progress: -1})
			return
		}
		p.emitReactionChanges(message, previous)
		p.emit(core.MessageEvent{InstanceID: p.instance, Message: message})
		for _, receipt := range message.Receipts {
			p.emit(core.ReceiptEvent{
				InstanceID:     p.instance,
				ConversationID: message.ProtocolConvID,
				MessageID:      message.ProtocolMsgID,
				ReceiptType:    core.ReceiptType(receipt.ReceiptType),
				UserID:         receipt.UserID,
				Timestamp:      receipt.Timestamp.Unix(),
			})
		}
	case *gmproto.Conversation:
		if event.GetConversationID() == "" {
			return
		}
		if err := p.storeConversation(event); err != nil {
			p.emit(core.SyncStatusEvent{InstanceID: p.instance, Status: core.SyncStatusError, Message: "Google Messages conversation could not be stored", Progress: -1})
			return
		}
		p.emit(core.ContactStatusEvent{InstanceID: p.instance, UserID: "refresh", Status: "new_conversations_discovered"})
	case *events.PairSuccessful:
		p.mu.Lock()
		p.emoji = ""
		err := p.saveAuthLocked()
		p.mu.Unlock()
		if err != nil {
			p.emit(core.SyncStatusEvent{InstanceID: p.instance, Status: core.SyncStatusError, Message: "Google Messages session could not be saved", Progress: -1})
		}
	case *events.GaiaLoggedOut:
		// Google revoked the pairing session server-side. Clear local auth so
		// IsAuthenticated() returns false and the UI prompts for re-pairing.
		p.mu.Lock()
		p.auth = libgm.NewAuthData()
		_ = os.Remove(p.authPath())
		p.newClientLocked()
		p.mu.Unlock()
		p.emit(core.SyncStatusEvent{InstanceID: p.instance, Status: core.SyncStatusNeedsReauth, Message: "Google Messages session expired — please re-pair your account", Progress: -1})
	case *events.ListenFatalError:
		// All ListenFatalError cases (auth token refresh rejected, 401/403) are
		// permanent auth failures. Clear auth so the provider does not pretend
		// to be authenticated on the next startup.
		p.mu.Lock()
		p.auth = libgm.NewAuthData()
		_ = os.Remove(p.authPath())
		p.newClientLocked()
		p.mu.Unlock()
		p.emit(core.SyncStatusEvent{InstanceID: p.instance, Status: core.SyncStatusNeedsReauth, Message: "Google Messages connection failed — please re-pair your account", Progress: -1})
	}
}

func (p *Provider) reactionsForMessage(protocolMessageID string) map[string]models.Reaction {
	if db.DB == nil {
		return nil
	}
	var message models.Message
	if err := db.DB.Where("protocol_msg_id = ?", protocolMessageID).First(&message).Error; err != nil {
		return nil
	}
	var reactions []models.Reaction
	if err := db.DB.Where("message_id = ?", message.ID).Find(&reactions).Error; err != nil {
		return nil
	}
	result := make(map[string]models.Reaction, len(reactions))
	for _, reaction := range reactions {
		result[reaction.UserID+"\x00"+reaction.Emoji] = reaction
	}
	return result
}

func (p *Provider) emitReactionChanges(message models.Message, previous map[string]models.Reaction) {
	current := make(map[string]models.Reaction, len(message.Reactions))
	for _, reaction := range message.Reactions {
		key := reaction.UserID + "\x00" + reaction.Emoji
		current[key] = reaction
		if _, exists := previous[key]; !exists {
			p.emit(core.ReactionEvent{InstanceID: p.instance, ConversationID: message.ProtocolConvID, MessageID: message.ProtocolMsgID, UserID: reaction.UserID, Emoji: reaction.Emoji, Added: true, Timestamp: message.Timestamp.Unix()})
		}
	}
	for key, reaction := range previous {
		if _, exists := current[key]; !exists {
			p.emit(core.ReactionEvent{InstanceID: p.instance, ConversationID: message.ProtocolConvID, MessageID: message.ProtocolMsgID, UserID: reaction.UserID, Emoji: reaction.Emoji, Added: false, Timestamp: time.Now().Unix()})
		}
	}
}

func (p *Provider) emit(event core.ProviderEvent) {
	select {
	case p.eventChan <- event:
	default:
		// The next synchronization reconciles an event lost to backpressure.
	}
}

func (p *Provider) authPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Loom", p.instance, "googlemessages-session.json")
}

func (p *Provider) loadAuthLocked() error {
	path := p.authPath()
	if path == "" {
		return fmt.Errorf("%s: find configuration directory", providerID)
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: read session: %w", providerID, err)
	}
	if err := json.Unmarshal(data, p.auth); err != nil {
		return fmt.Errorf("%s: decode session: %w", providerID, err)
	}
	return nil
}

func (p *Provider) saveAuthLocked() error {
	path := p.authPath()
	if path == "" {
		return fmt.Errorf("%s: find configuration directory", providerID)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(p.auth)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// unsupportedProvider keeps the provider contract explicit while a remote
// operation has no Google Messages protocol implementation yet.
type unsupportedProvider struct{}

func unsupported(op string) error                       { return fmt.Errorf("%s: %s not supported", providerID, op) }
func (unsupportedProvider) SyncHistory(time.Time) error { return unsupported("SyncHistory") }
func (unsupportedProvider) GetContacts() ([]models.LinkedAccount, error) {
	return nil, unsupported("GetContacts")
}
func (unsupportedProvider) GetConversationHistory(string, int, *time.Time, *time.Time) ([]models.Message, error) {
	return nil, unsupported("GetConversationHistory")
}
func (unsupportedProvider) SendMessage(string, string, *core.Attachment, *string) (*models.Message, error) {
	return nil, unsupported("SendMessage")
}
func (unsupportedProvider) SendReply(string, string, string) (*models.Message, error) {
	return nil, unsupported("SendReply")
}
func (unsupportedProvider) SendThreadReply(string, string, string, string) (*models.Message, error) {
	return nil, unsupported("SendThreadReply")
}
func (unsupportedProvider) SendFile(string, *core.Attachment, *string) (*models.Message, error) {
	return nil, unsupported("SendFile")
}
func (unsupportedProvider) EditMessage(string, string, string) (*models.Message, error) {
	return nil, unsupported("EditMessage")
}
func (unsupportedProvider) DeleteMessage(string, string) error { return unsupported("DeleteMessage") }
func (unsupportedProvider) GetThreads(string) ([]models.Message, error) {
	return nil, unsupported("GetThreads")
}
func (unsupportedProvider) AddReaction(string, string, string) error {
	return unsupported("AddReaction")
}
func (unsupportedProvider) RemoveReaction(string, string, string) error {
	return unsupported("RemoveReaction")
}
func (unsupportedProvider) SendTypingIndicator(string, bool) error {
	return unsupported("SendTypingIndicator")
}
func (unsupportedProvider) CreateGroup(string, []string) (*models.Conversation, error) {
	return nil, unsupported("CreateGroup")
}
func (unsupportedProvider) UpdateGroupName(string, string) error {
	return unsupported("UpdateGroupName")
}
func (unsupportedProvider) AddGroupParticipants(string, []string) error {
	return unsupported("AddGroupParticipants")
}
func (unsupportedProvider) RemoveGroupParticipants(string, []string) error {
	return unsupported("RemoveGroupParticipants")
}
func (unsupportedProvider) LeaveGroup(string) error { return unsupported("LeaveGroup") }
func (unsupportedProvider) PromoteGroupAdmins(string, []string) error {
	return unsupported("PromoteGroupAdmins")
}
func (unsupportedProvider) DemoteGroupAdmins(string, []string) error {
	return unsupported("DemoteGroupAdmins")
}
func (unsupportedProvider) GetGroupParticipants(string) ([]models.GroupParticipant, error) {
	return nil, unsupported("GetGroupParticipants")
}
func (unsupportedProvider) CreateGroupInviteLink(string) (string, error) {
	return "", unsupported("CreateGroupInviteLink")
}
func (unsupportedProvider) RevokeGroupInviteLink(string) error {
	return unsupported("RevokeGroupInviteLink")
}
func (unsupportedProvider) JoinGroupByInviteLink(string) (*models.Conversation, error) {
	return nil, unsupported("JoinGroupByInviteLink")
}
func (unsupportedProvider) JoinGroupByInviteMessage(string) (*models.Conversation, error) {
	return nil, unsupported("JoinGroupByInviteMessage")
}
func (unsupportedProvider) MarkMessageAsRead(string, string) error {
	return unsupported("MarkMessageAsRead")
}
func (unsupportedProvider) MarkConversationAsRead(string) error {
	return unsupported("MarkConversationAsRead")
}
func (unsupportedProvider) MarkMessageAsPlayed(string, string) error {
	return unsupported("MarkMessageAsPlayed")
}
func (unsupportedProvider) PinConversation(string) error    { return unsupported("PinConversation") }
func (unsupportedProvider) UnpinConversation(string) error  { return unsupported("UnpinConversation") }
func (unsupportedProvider) MuteConversation(string) error   { return unsupported("MuteConversation") }
func (unsupportedProvider) UnmuteConversation(string) error { return unsupported("UnmuteConversation") }
func (unsupportedProvider) GetConversationState(string) (*models.Conversation, error) {
	return nil, unsupported("GetConversationState")
}
func (unsupportedProvider) SendRetryReceipt(string, string) error {
	return unsupported("SendRetryReceipt")
}
func (unsupportedProvider) SendStatusMessage(string, *core.Attachment) (*models.Message, error) {
	return nil, unsupported("SendStatusMessage")
}
func (unsupportedProvider) GetContactName(string) (string, error) {
	return "", unsupported("GetContactName")
}
func (unsupportedProvider) GetCustomEmojis() (map[string]string, error) {
	return nil, unsupported("GetCustomEmojis")
}
func (unsupportedProvider) GetAuthQRCode() (string, error) { return "", unsupported("GetAuthQRCode") }
func (unsupportedProvider) RefreshContact(string) error    { return unsupported("RefreshContact") }
