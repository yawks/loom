package whatsapp

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func (w *WhatsAppProvider) downloadAndCacheAttachment(evt *events.Message, mediaType string) *models.Attachment {
	if w.client == nil {
		return nil
	}

	var media *waE2E.Message
	msg := evt.Message
	if msg == nil {
		return nil
	}

	// Determine media type and get the media message
	var fileName string
	var mimeType string
	var fileSize int64
	var thumbnailData []byte

	switch mediaType {
	case "image":
		if img := msg.GetImageMessage(); img != nil {
			media = &waE2E.Message{ImageMessage: img}
			mimeType = img.GetMimetype()
			fileSize = int64(img.GetFileLength())
			// Generate filename from mime type
			if strings.HasPrefix(mimeType, "image/") {
				ext := strings.TrimPrefix(mimeType, "image/")
				if ext == "jpeg" {
					ext = "jpg"
				}
				fileName = fmt.Sprintf("image.%s", ext)
			} else {
				fileName = "image.jpg"
			}
			if img.GetJPEGThumbnail() != nil {
				thumbnailData = img.GetJPEGThumbnail()
			}
		} else {
			return nil
		}
	case "video":
		if vid := msg.GetVideoMessage(); vid != nil {
			media = &waE2E.Message{VideoMessage: vid}
			mimeType = vid.GetMimetype()
			fileSize = int64(vid.GetFileLength())
			// Generate filename from mime type
			if strings.HasPrefix(mimeType, "video/") {
				ext := strings.TrimPrefix(mimeType, "video/")
				fileName = fmt.Sprintf("video.%s", ext)
			} else {
				fileName = "video.mp4"
			}
			if vid.GetJPEGThumbnail() != nil {
				thumbnailData = vid.GetJPEGThumbnail()
			}
		} else {
			return nil
		}
	case "audio":
		if aud := msg.GetAudioMessage(); aud != nil {
			media = &waE2E.Message{AudioMessage: aud}
			mimeType = aud.GetMimetype()
			fileSize = int64(aud.GetFileLength())
			// Generate filename from mime type
			if strings.HasPrefix(mimeType, "audio/") {
				ext := strings.TrimPrefix(mimeType, "audio/")
				fileName = fmt.Sprintf("audio.%s", ext)
			} else {
				fileName = "audio.mp3"
			}
		} else {
			return nil
		}
	case "document":
		if doc := msg.GetDocumentMessage(); doc != nil {
			media = &waE2E.Message{DocumentMessage: doc}
			fileName = doc.GetFileName()
			mimeType = doc.GetMimetype()
			fileSize = int64(doc.GetFileLength())
		} else {
			return nil
		}
	case "sticker":
		if stk := msg.GetStickerMessage(); stk != nil {
			media = &waE2E.Message{StickerMessage: stk}
			fileName = "sticker.webp"
			mimeType = "image/webp"
			fileSize = int64(stk.GetFileLength())
		} else {
			return nil
		}
	default:
		return nil
	}

	if media == nil {
		return nil
	}

	// Get cache directory
	configDir, err := os.UserConfigDir()
	if err != nil {
		fmt.Printf("WhatsApp: Failed to get config directory for attachment cache: %v\n", err)
		return nil
	}
	cacheDir := filepath.Join(configDir, "Loom", "whatsapp", "attachments")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		fmt.Printf("WhatsApp: Failed to create attachment cache directory: %v\n", err)
		return nil
	}

	// Generate a unique filename based on message ID and media type
	hash := sha256.Sum256([]byte(evt.Info.ID + mediaType))
	ext := filepath.Ext(fileName)
	if ext == "" {
		// Determine extension from mime type
		switch {
		case strings.HasPrefix(mimeType, "image/"):
			ext = ".jpg"
		case strings.HasPrefix(mimeType, "video/"):
			ext = ".mp4"
		case strings.HasPrefix(mimeType, "audio/ogg"):
			ext = ".ogg"
		case strings.HasPrefix(mimeType, "audio/"):
			ext = ".mp3"
		case mimeType == "application/pdf":
			ext = ".pdf"
		case strings.HasPrefix(mimeType, "application/vnd.ms-excel") || strings.HasPrefix(mimeType, "application/vnd.openxmlformats-officedocument.spreadsheetml"):
			ext = ".xls"
		default:
			ext = ".bin"
		}
	}
	filename := hex.EncodeToString(hash[:]) + ext
	cachePath := filepath.Join(cacheDir, filename)

	// Check if file is already cached
	if _, err := os.Stat(cachePath); err == nil {
		// File exists, return the path
		att := &models.Attachment{
			Type:     mediaType,
			URL:      cachePath,
			FileName: fileName,
			FileSize: fileSize,
			MimeType: mimeType,
		}
		// Save thumbnail if available
		if len(thumbnailData) > 0 {
			thumbHash := sha256.Sum256([]byte(evt.Info.ID + mediaType + "thumb"))
			thumbFilename := hex.EncodeToString(thumbHash[:]) + ".jpg"
			thumbPath := filepath.Join(cacheDir, thumbFilename)
			if err := os.WriteFile(thumbPath, thumbnailData, 0644); err == nil {
				att.Thumbnail = thumbPath
			}
		}
		return att
	}

	// Download the media
	var downloadable whatsmeow.DownloadableMessage
	switch mediaType {
	case "image":
		downloadable = media.GetImageMessage()
	case "video":
		downloadable = media.GetVideoMessage()
	case "audio":
		downloadable = media.GetAudioMessage()
	case "document":
		downloadable = media.GetDocumentMessage()
	case "sticker":
		downloadable = media.GetStickerMessage()
	default:
		return nil
	}

	fmt.Printf("WhatsApp: Starting download of %s attachment for message %s\n", mediaType, evt.Info.ID)
	data, err := w.client.Download(w.ctx, downloadable)
	if err != nil {
		fmt.Printf("WhatsApp: Failed to download %s attachment for message %s: %v\n", mediaType, evt.Info.ID, err)
		// Log more details about the error
		if err != nil {
			fmt.Printf("WhatsApp: Download error details: %T, %s\n", err, err.Error())
		}
		return nil
	}
	fmt.Printf("WhatsApp: Successfully downloaded %s attachment for message %s, size: %d bytes\n", mediaType, evt.Info.ID, len(data))

	// Create the file
	file, err := os.Create(cachePath)
	if err != nil {
		fmt.Printf("WhatsApp: Failed to create attachment file: %v\n", err)
		return nil
	}
	defer file.Close()

	// Write the data
	_, err = file.Write(data)
	if err != nil {
		fmt.Printf("WhatsApp: Failed to save attachment file: %v\n", err)
		os.Remove(cachePath) // Clean up on error
		return nil
	}

	att := &models.Attachment{
		Type:     mediaType,
		URL:      cachePath,
		FileName: fileName,
		FileSize: fileSize,
		MimeType: mimeType,
	}

	// Save thumbnail if available
	if len(thumbnailData) > 0 {
		thumbHash := sha256.Sum256([]byte(evt.Info.ID + mediaType + "thumb"))
		thumbFilename := hex.EncodeToString(thumbHash[:]) + ".jpg"
		thumbPath := filepath.Join(cacheDir, thumbFilename)
		if err := os.WriteFile(thumbPath, thumbnailData, 0644); err == nil {
			att.Thumbnail = thumbPath
		}
	}

	return att
}

func (w *WhatsAppProvider) extractAttachments(evt *events.Message) []models.Attachment {
	var attachments []models.Attachment
	msg := evt.Message
	if msg == nil {
		fmt.Printf("WhatsApp: extractAttachments: Message is nil\n")
		return attachments
	}

	// Check for different media types
	if imgMsg := msg.GetImageMessage(); imgMsg != nil {
		fmt.Printf("WhatsApp: extractAttachments: Found image message\n")
		if att := w.downloadAndCacheAttachment(evt, "image"); att != nil {
			fmt.Printf("WhatsApp: extractAttachments: Successfully downloaded image attachment: %s\n", att.URL)
			attachments = append(attachments, *att)
		} else {
			fmt.Printf("WhatsApp: extractAttachments: Failed to download image attachment\n")
		}
	}
	if vidMsg := msg.GetVideoMessage(); vidMsg != nil {
		fmt.Printf("WhatsApp: extractAttachments: Found video message\n")
		if att := w.downloadAndCacheAttachment(evt, "video"); att != nil {
			fmt.Printf("WhatsApp: extractAttachments: Successfully downloaded video attachment: %s\n", att.URL)
			attachments = append(attachments, *att)
		} else {
			fmt.Printf("WhatsApp: extractAttachments: Failed to download video attachment\n")
		}
	}
	if audMsg := msg.GetAudioMessage(); audMsg != nil {
		fmt.Printf("WhatsApp: extractAttachments: Found audio message\n")
		// Default to "audio" for download
		if att := w.downloadAndCacheAttachment(evt, "audio"); att != nil {
			// Check if it's a voice message (PTT)
			if audMsg.GetPTT() {
				att.Type = "voice"
			}
			// Set duration
			if seconds := audMsg.GetSeconds(); seconds > 0 {
				att.Duration = seconds
			}
			fmt.Printf("WhatsApp: extractAttachments: Successfully downloaded audio attachment: %s (Type: %s, Duration: %d)\n", att.URL, att.Type, att.Duration)
			attachments = append(attachments, *att)
		} else {
			fmt.Printf("WhatsApp: extractAttachments: Failed to download audio attachment\n")
		}
	}
	if docMsg := msg.GetDocumentMessage(); docMsg != nil {
		fmt.Printf("WhatsApp: extractAttachments: Found document message: %s\n", docMsg.GetFileName())
		if att := w.downloadAndCacheAttachment(evt, "document"); att != nil {
			fmt.Printf("WhatsApp: extractAttachments: Successfully downloaded document attachment: %s\n", att.URL)
			attachments = append(attachments, *att)
		} else {
			fmt.Printf("WhatsApp: extractAttachments: Failed to download document attachment\n")
		}
	}
	if stkMsg := msg.GetStickerMessage(); stkMsg != nil {
		fmt.Printf("WhatsApp: extractAttachments: Found sticker message\n")
		if att := w.downloadAndCacheAttachment(evt, "sticker"); att != nil {
			fmt.Printf("WhatsApp: extractAttachments: Successfully downloaded sticker attachment: %s\n", att.URL)
			attachments = append(attachments, *att)
		} else {
			fmt.Printf("WhatsApp: extractAttachments: Failed to download sticker attachment\n")
		}
	}

	if len(attachments) == 0 {
		fmt.Printf("WhatsApp: extractAttachments: No attachments found in message %s\n", evt.Info.ID)
	}

	return attachments
}

func (w *WhatsAppProvider) tryHandleProtocolMessage(evt *events.Message, emitEvent bool) bool {
	if evt == nil || evt.Message == nil {
		return false
	}
	protocolMsg := evt.Message.GetProtocolMessage()
	if protocolMsg == nil {
		return false
	}

	switch protocolMsg.GetType() {
	case waProto.ProtocolMessage_REVOKE:
		w.handleRevokedProtocolMessage(evt, protocolMsg, emitEvent)
		return true
	case waProto.ProtocolMessage_MESSAGE_EDIT:
		w.handleEditedProtocolMessage(evt, protocolMsg, emitEvent)
		return true
	default:
		return false
	}
}

func (w *WhatsAppProvider) handleRevokedProtocolMessage(evt *events.Message, protocolMsg *waProto.ProtocolMessage, emitEvent bool) {
	if evt == nil || protocolMsg == nil {
		return
	}

	key := protocolMsg.GetKey()
	if key == nil {
		fmt.Println("WhatsApp: Received revoke protocol message without key, skipping")
		return
	}

	convID := key.GetRemoteJID()
	if convID == "" && evt.Info.Chat.String() != "" {
		convID = evt.Info.Chat.String()
	}

	msgID := key.GetID()
	if msgID == "" {
		fmt.Println("WhatsApp: Received revoke protocol message without message ID, skipping")
		return
	}

	deletedBy := ""
	if !evt.Info.Sender.IsEmpty() {
		deletedBy = evt.Info.Sender.String()
	}

	deletedAt := evt.Info.Timestamp
	updated := w.markMessageAsDeleted(convID, msgID, deletedBy, "revoked", deletedAt)
	if updated == nil {
		fmt.Printf("WhatsApp: Revoke event received for %s but message not found locally yet\n", msgID)
		return
	}

	if emitEvent {
		select {
		case w.eventChan <- core.MessageEvent{Message: *updated}:
			fmt.Printf("WhatsApp: MessageEvent emitted for revoked message %s\n", msgID)
		default:
			fmt.Printf("WhatsApp: WARNING - Failed to emit MessageEvent for revoked message %s (channel full)\n", msgID)
		}
	}
}

func (w *WhatsAppProvider) handleEditedProtocolMessage(evt *events.Message, protocolMsg *waProto.ProtocolMessage, emitEvent bool) {
	if evt == nil || protocolMsg == nil {
		return
	}

	key := protocolMsg.GetKey()
	if key == nil {
		fmt.Println("WhatsApp: Received edit protocol message without key, skipping")
		return
	}

	convID := key.GetRemoteJID()
	if convID == "" && evt.Info.Chat.String() != "" {
		convID = evt.Info.Chat.String()
	}

	msgID := key.GetID()
	if msgID == "" {
		fmt.Println("WhatsApp: Received edit protocol message without message ID, skipping")
		return
	}

	editedMsg := protocolMsg.GetEditedMessage()
	if editedMsg == nil {
		fmt.Println("WhatsApp: Received edit protocol message without edited message, skipping")
		return
	}

	newText := ""
	if editedMsg.Conversation != nil {
		newText = *editedMsg.Conversation
	}

	// Update the message in cache and database
	var updated *models.Message
	editedAt := time.Now()
	w.mu.Lock()
	if msgs, ok := w.conversationMessages[convID]; ok {
		for idx := range msgs {
			if msgs[idx].ProtocolMsgID == msgID {
				msgs[idx].Body = newText
				msgs[idx].IsEdited = true
				msgs[idx].EditedTimestamp = &editedAt
				copyMsg := msgs[idx]
				updated = &copyMsg
				w.conversationMessages[convID][idx] = msgs[idx]
				fmt.Printf("WhatsApp: Found and updated message %s in cache\n", msgID)
				break
			}
		}
	}
	w.mu.Unlock()

	// If not found in cache, try database
	if updated == nil && db.DB != nil {
		var dbMsg models.Message
		if err := db.DB.Where("protocol_msg_id = ?", msgID).First(&dbMsg).Error; err == nil {
			fmt.Printf("WhatsApp: Found message %s in database, updating. Old body: '%s', New body: '%s'\n", msgID, dbMsg.Body, newText)

			// Update in database first
			updates := map[string]interface{}{
				"body":             newText,
				"is_edited":        true,
				"edited_timestamp": editedAt,
			}
			if err := db.DB.Model(&models.Message{}).
				Where("protocol_msg_id = ?", msgID).
				Updates(updates).Error; err != nil {
				fmt.Printf("WhatsApp: Failed to update edited message in database: %v\n", err)
			} else {
				fmt.Printf("WhatsApp: Successfully updated edited message %s in database\n", msgID)
				// Reload from database to get the updated version
				if err := db.DB.Where("protocol_msg_id = ?", msgID).First(&dbMsg).Error; err == nil {
					dbMsg.Body = newText
					dbMsg.IsEdited = true
					dbMsg.EditedTimestamp = &editedAt
					fmt.Printf("WhatsApp: Reloaded message %s from database, body: '%s'\n", msgID, dbMsg.Body)
				}
			}

			// Add to cache if conversation exists
			if convID != "" {
				w.mu.Lock()
				if msgs, ok := w.conversationMessages[convID]; ok {
					// Check if it's not already there
					found := false
					for idx := range msgs {
						if msgs[idx].ProtocolMsgID == msgID {
							msgs[idx] = dbMsg
							w.conversationMessages[convID][idx] = msgs[idx]
							found = true
							fmt.Printf("WhatsApp: Updated message %s in cache, body: '%s'\n", msgID, msgs[idx].Body)
							break
						}
					}
					if !found {
						w.conversationMessages[convID] = append(msgs, dbMsg)
						fmt.Printf("WhatsApp: Added message %s to cache\n", msgID)
					}
				} else {
					// Create new conversation entry
					w.conversationMessages[convID] = []models.Message{dbMsg}
					fmt.Printf("WhatsApp: Created new conversation entry for %s with message %s\n", convID, msgID)
				}
				w.mu.Unlock()
			}

			updated = &dbMsg
		} else {
			fmt.Printf("WhatsApp: Message %s not found in database: %v\n", msgID, err)
		}
	}

	// Update in database if found in cache (and not already updated above)
	if updated != nil && db.DB != nil {
		// Check if we already updated it above (when found in DB)
		// We only need to update here if it was found in cache
		var needsUpdate bool
		w.mu.RLock()
		if msgs, ok := w.conversationMessages[convID]; ok {
			for _, msg := range msgs {
				if msg.ProtocolMsgID == msgID {
					needsUpdate = true
					break
				}
			}
		}
		w.mu.RUnlock()

		if needsUpdate {
			updates := map[string]interface{}{
				"body":             newText,
				"is_edited":        true,
				"edited_timestamp": editedAt,
			}
			if err := db.DB.Model(&models.Message{}).
				Where("protocol_msg_id = ?", msgID).
				Updates(updates).Error; err != nil {
				fmt.Printf("WhatsApp: Failed to update edited message in database: %v\n", err)
			} else {
				fmt.Printf("WhatsApp: Successfully updated edited message %s in database (from cache)\n", msgID)
			}
		}
	}

	if updated != nil && emitEvent {
		fmt.Printf("WhatsApp: Emitting MessageEvent for edited message %s with body: '%s', isEdited: %v\n", msgID, updated.Body, updated.IsEdited)
		select {
		case w.eventChan <- core.MessageEvent{Message: *updated}:
			fmt.Printf("WhatsApp: MessageEvent emitted for edited message %s\n", msgID)
		default:
			fmt.Printf("WhatsApp: WARNING - Failed to emit MessageEvent for edited message %s\n", msgID)
		}
	} else if updated == nil {
		fmt.Printf("WhatsApp: Edit event received for %s but message not found locally yet\n", msgID)
	}
}

func (w *WhatsAppProvider) markMessageAsDeleted(convID, msgID, deletedBy, reason string, deletedAt time.Time) *models.Message {
	if msgID == "" {
		return nil
	}

	var updated *models.Message
	var convIDCopy = convID

	w.mu.Lock()
	if convIDCopy != "" {
		updated = w.updateCachedMessageDeletion(convIDCopy, msgID, deletedBy, reason, deletedAt)
	}
	if updated == nil {
		updated = w.updateCachedMessageDeletionAcrossConversations(msgID, deletedBy, reason, deletedAt, &convIDCopy)
	}
	w.mu.Unlock()

	if db.DB != nil {
		updates := map[string]interface{}{
			"is_deleted":     true,
			"deleted_by":     deletedBy,
			"deleted_reason": reason,
		}
		if !deletedAt.IsZero() {
			updates["deleted_timestamp"] = deletedAt
		} else {
			updates["deleted_timestamp"] = nil
		}

		if err := db.DB.Model(&models.Message{}).
			Where("protocol_msg_id = ?", msgID).
			Updates(updates).Error; err != nil {
			fmt.Printf("WhatsApp: Failed to persist deletion state for message %s: %v\n", msgID, err)
		} else if updated == nil {
			var dbMsg models.Message
			if err := db.DB.Where("protocol_msg_id = ?", msgID).First(&dbMsg).Error; err == nil {
				convIDCopy = dbMsg.ProtocolConvID
				updatedCopy := dbMsg
				updated = &updatedCopy

				if convIDCopy != "" {
					w.mu.Lock()
					if msgs, ok := w.conversationMessages[convIDCopy]; ok {
						for idx := range msgs {
							if msgs[idx].ProtocolMsgID == msgID {
								msgs[idx] = dbMsg
								w.conversationMessages[convIDCopy][idx] = msgs[idx]
								break
							}
						}
					}
					w.mu.Unlock()
				}
			}
		}
	}

	return updated
}

func (w *WhatsAppProvider) updateCachedMessageDeletion(convID, msgID, deletedBy, reason string, deletedAt time.Time) *models.Message {
	if w.conversationMessages == nil {
		return nil
	}
	msgs, ok := w.conversationMessages[convID]
	if !ok {
		return nil
	}

	for idx := range msgs {
		if msgs[idx].ProtocolMsgID != msgID {
			continue
		}

		msg := msgs[idx]
		msg.IsDeleted = true
		msg.DeletedBy = deletedBy
		msg.DeletedReason = reason
		if !deletedAt.IsZero() {
			ts := deletedAt
			msg.DeletedTimestamp = &ts
		} else {
			msg.DeletedTimestamp = nil
		}
		w.conversationMessages[convID][idx] = msg
		copyMsg := msg
		return &copyMsg
	}

	return nil
}

func (w *WhatsAppProvider) updateCachedMessageDeletionAcrossConversations(msgID, deletedBy, reason string, deletedAt time.Time, convIDOut *string) *models.Message {
	if w.conversationMessages == nil {
		return nil
	}
	for convID, msgs := range w.conversationMessages {
		for idx := range msgs {
			if msgs[idx].ProtocolMsgID != msgID {
				continue
			}

			msg := msgs[idx]
			msg.IsDeleted = true
			msg.DeletedBy = deletedBy
			msg.DeletedReason = reason
			if !deletedAt.IsZero() {
				ts := deletedAt
				msg.DeletedTimestamp = &ts
			} else {
				msg.DeletedTimestamp = nil
			}
			w.conversationMessages[convID][idx] = msg
			copyMsg := msg
			if convIDOut != nil {
				*convIDOut = convID
			}
			return &copyMsg
		}
	}

	return nil
}

func (w *WhatsAppProvider) convertWhatsAppMessage(evt *events.Message) *models.Message {
	msg := evt.Message
	if msg == nil {
		return nil
	}

	// Skip reaction messages - they are handled separately in eventHandler
	if msg.GetReactionMessage() != nil {
		return nil
	}

	// Get conversation ID
	convID := evt.Info.Chat.String()
	chatJID := evt.Info.Chat

	// Check if this is a call message
	var callType string
	if callMsg := msg.GetCall(); callMsg != nil {
		// Extract call type from the call message
		// Determine if it's a group or individual call
		isGroup := chatJID.Server == types.GroupServer

		// Check if it's a video call (CallMessage has a VideoCall field)
		// For now, we'll determine the type based on the call message structure
		// Most call messages are missed calls, so we'll default to that
		if isGroup {
			// For group calls, we need to check if it's video or voice
			// Since we can't easily determine this from the Call message alone,
			// we'll use a generic type and let the frontend handle display
			callType = "missed_group_voice" // Default, can be refined later
		} else {
			callType = "missed_voice" // Default for individual calls
		}
		fmt.Printf("WhatsApp: Detected call message type: %s for message %s (group: %v)\n", callType, evt.Info.ID, isGroup)
	}

	// Track groups from messages
	if chatJID.Server == types.GroupServer {
		// Try to get group info from client to get the name
		// Do this without holding the lock to avoid deadlocks
		if w.client != nil {
			groupInfo, err := w.client.GetGroupInfo(w.ctx, chatJID)
			if err == nil && groupInfo != nil {
				w.mu.Lock()
				w.knownGroups[convID] = groupInfo.Name
				w.mu.Unlock()
				// Cache group participants asynchronously to avoid blocking
				// This avoids deadlocks since cacheGroupParticipants also takes locks
				go w.cacheGroupParticipants(chatJID)
			} else {
				w.mu.RLock()
				_, exists := w.knownGroups[convID]
				w.mu.RUnlock()
				if !exists {
					w.mu.Lock()
					w.knownGroups[convID] = convID
					w.mu.Unlock()
				}
			}
		}
	}

	// Get message ID
	msgID := evt.Info.ID

	// Get sender ID and resolve to canonical phone number (handles LID -> phone number conversion)
	senderID := evt.Info.Sender.String()
	if chatJID.Server == types.GroupServer {
		// In groups, resolve LID to phone number for consistency
		resolvedID, err := w.resolveContactIDForGroup(senderID, chatJID)
		if err == nil {
			senderID = resolvedID
			fmt.Printf("WhatsApp: Resolved sender ID %s to %s in group %s\n", evt.Info.Sender.String(), senderID, chatJID.String())
		} else {
			fmt.Printf("WhatsApp: Could not resolve sender ID %s in group %s: %v\n", senderID, chatJID.String(), err)
		}
	} else {
		// For 1-on-1 chats, also try to resolve (in case we receive a LID)
		resolvedID, err := w.resolveContactID(senderID)
		if err == nil {
			senderID = resolvedID
		}
	}

	// Check if message is from me
	isFromMe := evt.Info.IsFromMe
	var senderName string

	// Always try to get the actual name, even for messages from me
	// to avoid creating duplicate contacts with "You" as the name
	if chatJID.Server == types.GroupServer {
		// Try push name first (most reliable for groups)
		if evt.Info.PushName != "" {
			senderName = evt.Info.PushName
		} else {
			// Use group-aware lookup to handle LID participants
			senderName = w.lookupSenderNameInGroup(evt.Info.Sender, chatJID)
		}
	} else {
		// For individual chats, use normal lookup
		senderName = w.lookupSenderName(evt.Info.Sender)
	}

	// If senderName is empty or is a formatted phone number, prefer PushName if available
	// PushName is more reliable for real-time messages as it comes directly from WhatsApp
	if (senderName == "" || isPhoneNumber(senderName)) && evt.Info.PushName != "" && !isPhoneNumber(evt.Info.PushName) {
		senderName = evt.Info.PushName
		fmt.Printf("WhatsApp: Using PushName '%s' instead of formatted phone number for sender %s\n", senderName, senderID)
	}

	// If still empty, use push name as fallback (even if it's a phone number)
	if senderName == "" && evt.Info.PushName != "" {
		senderName = evt.Info.PushName
	}

	// If still empty, use sender ID as last resort
	if senderName == "" {
		senderName = senderID
	}

	// Get message text
	body := ""
	if msg.GetConversation() != "" {
		body = msg.GetConversation()
	} else if msg.GetExtendedTextMessage() != nil {
		body = msg.GetExtendedTextMessage().GetText()
	}

	// Extract quoted message information from ContextInfo
	// ContextInfo can be present in ExtendedTextMessage, ImageMessage, VideoMessage, etc.
	var quotedMessageID *string
	var quotedSenderID *string
	var quotedBody *string

	var contextInfo *waE2E.ContextInfo

	// Get ContextInfo from various message types
	if msg.GetExtendedTextMessage() != nil && msg.GetExtendedTextMessage().GetContextInfo() != nil {
		contextInfo = msg.GetExtendedTextMessage().GetContextInfo()
	} else if msg.GetImageMessage() != nil && msg.GetImageMessage().GetContextInfo() != nil {
		contextInfo = msg.GetImageMessage().GetContextInfo()
	} else if msg.GetVideoMessage() != nil && msg.GetVideoMessage().GetContextInfo() != nil {
		contextInfo = msg.GetVideoMessage().GetContextInfo()
	} else if msg.GetAudioMessage() != nil && msg.GetAudioMessage().GetContextInfo() != nil {
		contextInfo = msg.GetAudioMessage().GetContextInfo()
	} else if msg.GetDocumentMessage() != nil && msg.GetDocumentMessage().GetContextInfo() != nil {
		contextInfo = msg.GetDocumentMessage().GetContextInfo()
	} else if msg.GetStickerMessage() != nil && msg.GetStickerMessage().GetContextInfo() != nil {
		contextInfo = msg.GetStickerMessage().GetContextInfo()
	}

	if contextInfo != nil {
		// Get quoted message ID (StanzaID)
		if contextInfo.GetStanzaID() != "" {
			stanzaID := contextInfo.GetStanzaID()
			quotedMessageID = &stanzaID
		}

		// Get quoted sender ID (Participant)
		if contextInfo.GetParticipant() != "" {
			participant := contextInfo.GetParticipant()
			quotedSenderID = &participant
		}

		// Get quoted message text
		if contextInfo.GetQuotedMessage() != nil {
			quotedMsg := contextInfo.GetQuotedMessage()
			var quotedText string

			// Try to get text from various message types
			if quotedMsg.GetConversation() != "" {
				quotedText = quotedMsg.GetConversation()
			} else if quotedMsg.GetExtendedTextMessage() != nil {
				quotedText = quotedMsg.GetExtendedTextMessage().GetText()
			} else if quotedMsg.GetImageMessage() != nil {
				// Image message - use caption or indicate it's an image
				if quotedMsg.GetImageMessage().GetCaption() != "" {
					quotedText = quotedMsg.GetImageMessage().GetCaption()
				} else {
					quotedText = "📷 Photo"
				}
			} else if quotedMsg.GetVideoMessage() != nil {
				// Video message - use caption or indicate it's a video
				if quotedMsg.GetVideoMessage().GetCaption() != "" {
					quotedText = quotedMsg.GetVideoMessage().GetCaption()
				} else {
					quotedText = "🎥 Video"
				}
			} else if quotedMsg.GetAudioMessage() != nil {
				quotedText = "🎵 Audio"
			} else if quotedMsg.GetDocumentMessage() != nil {
				// Document message - show filename
				if quotedMsg.GetDocumentMessage().GetFileName() != "" {
					quotedText = "📎 " + quotedMsg.GetDocumentMessage().GetFileName()
				} else {
					quotedText = "📎 Document"
				}
			} else if quotedMsg.GetStickerMessage() != nil {
				quotedText = "Sticker"
			}

			if quotedText != "" {
				quotedBody = &quotedText
			}
		}
	}

	// Get timestamp
	timestamp := evt.Info.Timestamp

	// Extract attachments (download and cache them)
	attachments := w.extractAttachments(evt)
	var attachmentsJSON string
	if len(attachments) > 0 {
		attJSON, err := json.Marshal(attachments)
		if err == nil {
			attachmentsJSON = string(attJSON)
			fmt.Printf("WhatsApp: Extracted %d attachments for message %s: %s\n", len(attachments), msgID, attachmentsJSON)
		} else {
			fmt.Printf("WhatsApp: Failed to marshal attachments: %v\n", err)
		}
	} else {
		// Check if message has media but no attachments were extracted
		if msg.GetImageMessage() != nil || msg.GetVideoMessage() != nil || msg.GetAudioMessage() != nil || msg.GetDocumentMessage() != nil || msg.GetStickerMessage() != nil {
			fmt.Printf("WhatsApp: Message %s has media but no attachments were extracted\n", msgID)
		}
	}

	// Get sender avatar URL (only for messages not from me)
	var senderAvatarURL string
	if !isFromMe {
		// First, try to get avatar from cached conversations
		w.mu.RLock()
		if cached, exists := w.conversations[senderID]; exists && cached.AvatarURL != "" {
			senderAvatarURL = cached.AvatarURL
			w.mu.RUnlock()
		} else {
			w.mu.RUnlock()
			// Check if avatar is already being loaded to avoid duplicate requests
			w.avatarLoadingMu.Lock()
			isLoading := w.avatarLoading[senderID]
			if !isLoading {
				w.avatarLoading[senderID] = true
			}
			w.avatarLoadingMu.Unlock()

			// Only load if not already loading
			if !isLoading {
				// If not in cache, try to load it (this will cache it for future use)
				// We do this synchronously for messages as they arrive one by one
				// and we want the avatar to be available immediately
				senderAvatarURL = w.getProfilePictureURL(evt.Info.Sender)
				// If we got an avatar, update the cache
				if senderAvatarURL != "" {
					w.mu.Lock()
					if cached, exists := w.conversations[senderID]; exists {
						cached.AvatarURL = senderAvatarURL
						w.conversations[senderID] = cached
					} else {
						// Create a new entry in cache for this sender
						w.conversations[senderID] = models.LinkedAccount{
							Protocol:  "whatsapp",
							UserID:    senderID,
							Username:  senderName,
							AvatarURL: senderAvatarURL,
							Status:    "offline",
							CreatedAt: timestamp,
							UpdatedAt: timestamp,
						}
					}
					w.mu.Unlock()

					// Persist avatar to database
					if db.DB != nil {
						var existing models.LinkedAccount
						err := db.DB.Where("protocol = ? AND user_id = ?", "whatsapp", senderID).First(&existing).Error
						if err == nil {
							// Update existing
							existing.AvatarURL = senderAvatarURL
							existing.UpdatedAt = timestamp
							if err := db.DB.Save(&existing).Error; err != nil {
								fmt.Printf("WhatsApp: Failed to save sender avatar to database: %v\n", err)
							}
						} else {
							// Create new
							newAccount := models.LinkedAccount{
								Protocol:  "whatsapp",
								UserID:    senderID,
								Username:  senderName,
								AvatarURL: senderAvatarURL,
								Status:    "offline",
								CreatedAt: timestamp,
								UpdatedAt: timestamp,
							}
							if err := db.DB.Create(&newAccount).Error; err != nil {
								fmt.Printf("WhatsApp: Failed to create sender LinkedAccount in database: %v\n", err)
							}
						}
					}
				}

				// Mark as no longer loading
				w.avatarLoadingMu.Lock()
				delete(w.avatarLoading, senderID)
				w.avatarLoadingMu.Unlock()
			}
		}
	}

	// Update LinkedAccount name in database for 1-on-1 chats when we discover a name
	// This ensures the name appears in the sidebar
	if !isFromMe && chatJID.Server != types.GroupServer && senderName != "" && senderName != senderID {
		w.updateLinkedAccountName(senderID, senderName)
	}

	return &models.Message{
		ProtocolConvID:  convID,
		ProtocolMsgID:   msgID,
		SenderID:        senderID,
		SenderName:      senderName,
		SenderAvatarURL: senderAvatarURL,
		Body:            body,
		Timestamp:       timestamp,
		IsFromMe:        isFromMe,
		Attachments:     attachmentsJSON,
		QuotedMessageID: quotedMessageID,
		QuotedSenderID:  quotedSenderID,
		QuotedBody:      quotedBody,
		CallType:        callType,
	}
}

func (w *WhatsAppProvider) storeMessagesForConversation(convID string, messages []models.Message) int {
	if convID == "" || len(messages) == 0 {
		return 0
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.conversationMessages == nil {
		w.conversationMessages = make(map[string][]models.Message)
	}

	existing := append([]models.Message{}, w.conversationMessages[convID]...)
	combined := append(existing, messages...)

	sort.SliceStable(combined, func(i, j int) bool {
		return combined[i].Timestamp.Before(combined[j].Timestamp)
	})

	seens := make(map[string]struct{}, len(combined))
	dedup := make([]models.Message, 0, len(combined))
	for _, msg := range combined {
		key := msg.ProtocolMsgID
		if key != "" {
			if _, exists := seens[key]; exists {
				continue
			}
			seens[key] = struct{}{}
		}
		dedup = append(dedup, msg)
	}

	if len(dedup) > maxMessagesPerConversation {
		dedup = dedup[len(dedup)-maxMessagesPerConversation:]
	}

	w.conversationMessages[convID] = dedup

	// Persist messages to database
	if db.DB != nil {
		// Store messages in database (upsert by ProtocolMsgID)
		// Note: We store messages with ProtocolConvID, even if Conversation doesn't exist yet
		// This allows us to load messages on startup and filter conversations properly
		for _, msg := range messages {
			if msg.ProtocolMsgID == "" {
				continue
			}
			isCallMsg := msg.CallType != ""
			if isCallMsg {
				fmt.Printf("WhatsApp: [CALL MSG] Storing call message to DB: ProtocolMsgID=%s, ProtocolConvID=%s, CallType=%s\n",
					msg.ProtocolMsgID, convID, msg.CallType)
			}
			var existingMsg models.Message
			err := db.DB.Where("protocol_msg_id = ?", msg.ProtocolMsgID).First(&existingMsg).Error
			if err != nil {
				// Message doesn't exist, create it
				// Set ProtocolConvID so we can query by conversation later
				msg.ProtocolConvID = convID
				if err := db.DB.Create(&msg).Error; err != nil {
					if isCallMsg {
						fmt.Printf("WhatsApp: [CALL MSG] ERROR - Failed to persist call message %s: %v\n", msg.ProtocolMsgID, err)
					} else {
						fmt.Printf("WhatsApp: Failed to persist message %s: %v\n", msg.ProtocolMsgID, err)
					}
				} else {
					if isCallMsg {
						fmt.Printf("WhatsApp: [CALL MSG] SUCCESS - Persisted call message %s to DB with ProtocolConvID=%s\n", msg.ProtocolMsgID, convID)
					}
				}
			} else {
				// Message exists, update it if needed
				msg.ID = existingMsg.ID
				oldConvID := existingMsg.ProtocolConvID
				msg.ProtocolConvID = convID
				if err := db.DB.Save(&msg).Error; err != nil {
					if isCallMsg {
						fmt.Printf("WhatsApp: [CALL MSG] ERROR - Failed to update call message %s: %v\n", msg.ProtocolMsgID, err)
					} else {
						fmt.Printf("WhatsApp: Failed to update message %s: %v\n", msg.ProtocolMsgID, err)
					}
				} else {
					if isCallMsg && oldConvID != convID {
						fmt.Printf("WhatsApp: [CALL MSG] Updated call message %s ProtocolConvID from %s to %s\n", msg.ProtocolMsgID, oldConvID, convID)
					}
				}
			}
		}
	}

	return len(dedup)
}

func (w *WhatsAppProvider) appendMessageToConversation(msg *models.Message) {
	if msg == nil {
		return
	}
	if msg.CallType != "" {
		fmt.Printf("WhatsApp: [CALL MSG] appendMessageToConversation called for call message: ProtocolMsgID=%s, ProtocolConvID=%s, CallType=%s\n",
			msg.ProtocolMsgID, msg.ProtocolConvID, msg.CallType)
	}
	w.storeMessagesForConversation(msg.ProtocolConvID, []models.Message{*msg})
}

func (w *WhatsAppProvider) hasConversationHistory(convID string) bool {
	// First check in-memory cache
	w.mu.RLock()
	if len(w.conversationMessages[convID]) > 0 {
		w.mu.RUnlock()
		return true
	}
	w.mu.RUnlock()

	// If not in cache, check database
	if db.DB != nil {
		var count int64
		err := db.DB.Model(&models.Message{}).
			Where("protocol_conv_id = ?", convID).
			Count(&count).Error
		if err == nil && count > 0 {
			return true
		}

		// Also check if convID is a LID and try to resolve it
		// Messages might be stored with resolved JID while contact has LID
		if jid, parseErr := types.ParseJID(convID); parseErr == nil && jid.Server == "lid" {
			if resolvedID, resolveErr := w.resolveContactID(convID); resolveErr == nil && resolvedID != convID {
				var resolvedCount int64
				if err := db.DB.Model(&models.Message{}).
					Where("protocol_conv_id = ?", resolvedID).
					Count(&resolvedCount).Error; err == nil && resolvedCount > 0 {
					return true
				}
			}
		}
	}

	return false
}

func (w *WhatsAppProvider) cacheMessagesFromHistory(history *waHistorySync.HistorySync) {
	fmt.Printf("WhatsApp: [CACHE_MESSAGES] cacheMessagesFromHistory called\n")
	if history == nil || w.client == nil {
		fmt.Printf("WhatsApp: [CACHE_MESSAGES] Skipping - history=%v, client=%v\n", history != nil, w.client != nil)
		return
	}

	conversations := history.GetConversations()
	fmt.Printf("WhatsApp: [CACHE_MESSAGES] Processing %d conversations for messages\n", len(conversations))
	for _, conv := range conversations {
		if conv == nil {
			continue
		}
		convID := conv.GetID()
		if convID == "" {
			continue
		}
		chatJID, err := types.ParseJID(convID)
		if err != nil {
			continue
		}
		historyMsgs := conv.GetMessages()
		fmt.Printf("WhatsApp: [CACHE_MESSAGES] Conversation %s has %d messages in history\n", convID, len(historyMsgs))
		if len(historyMsgs) == 0 {
			continue
		}

		converted := make([]models.Message, 0, len(historyMsgs))
		for _, hMsg := range historyMsgs {
			if hMsg == nil || hMsg.GetMessage() == nil {
				continue
			}

			// Get WebMessageInfo to access reactions
			webMsgInfo := hMsg.GetMessage()

			evt, err := w.client.ParseWebMessage(chatJID, webMsgInfo)
			if err != nil {
				fmt.Printf("WhatsApp: Failed to parse history message for %s: %v\n", convID, err)
				continue
			}
			if w.tryHandleProtocolMessage(evt, false) {
				continue
			}
			if msg := w.convertWhatsAppMessage(evt); msg != nil {
				// Extract reactions from WebMessageInfo
				reactions := webMsgInfo.GetReactions()
				if len(reactions) > 0 {
					fmt.Printf("WhatsApp: Found %d reactions in history for message %s\n", len(reactions), msg.ProtocolMsgID)
					msg.Reactions = w.convertHistoryReactions(reactions, msg.ProtocolMsgID)
				}

				// Extract message status from WebMessageInfo
				// Only process status for messages sent by current user
				if msg.IsFromMe {
					status := webMsgInfo.GetStatus()
					receipts := w.convertMessageStatus(status, msg.ProtocolMsgID, convID, msg.Timestamp)
					if len(receipts) > 0 {
						fmt.Printf("WhatsApp: Extracted %d receipts from history for message %s (status: %v)\n", len(receipts), msg.ProtocolMsgID, status)
						msg.Receipts = receipts
					}
				}

				// Check if message has attachments but they weren't extracted (download might have failed)
				if msg.Attachments == "" {
					// Try to extract attachments asynchronously for history messages
					// This is done in a goroutine to avoid blocking the history sync
					go func(evtCopy *events.Message, msgID string) {
						fmt.Printf("WhatsApp: Attempting to extract attachments for history message %s\n", msgID)
						attachments := w.extractAttachments(evtCopy)
						if len(attachments) > 0 {
							attJSON, err := json.Marshal(attachments)
							if err == nil {
								// Update message in database with attachments
								if db.DB != nil {
									var dbMsg models.Message
									if err := db.DB.Where("protocol_msg_id = ?", msgID).First(&dbMsg).Error; err == nil {
										dbMsg.Attachments = string(attJSON)
										if err := db.DB.Save(&dbMsg).Error; err == nil {
											fmt.Printf("WhatsApp: Successfully saved attachments for history message %s\n", msgID)
										} else {
											fmt.Printf("WhatsApp: Failed to save attachments for history message %s: %v\n", msgID, err)
										}
									}
								}
							}
						} else {
							// Check if message has media but no attachments were extracted
							msg := evtCopy.Message
							if msg != nil && (msg.GetImageMessage() != nil || msg.GetVideoMessage() != nil || msg.GetAudioMessage() != nil || msg.GetDocumentMessage() != nil || msg.GetStickerMessage() != nil) {
								fmt.Printf("WhatsApp: History message %s has media but attachments extraction failed or returned empty\n", msgID)
							}
						}
					}(evt, msg.ProtocolMsgID)
				} else {
					fmt.Printf("WhatsApp: History message %s already has attachments: %s\n", msg.ProtocolMsgID, msg.Attachments)
				}

				converted = append(converted, *msg)
			}
		}

		if len(converted) > 0 {
			// Infer receipts for group messages based on participant activity
			// This must be done BEFORE storing to ensure receipts are persisted
			if len(convID) > 5 && convID[len(convID)-5:] == "@g.us" {
				w.inferGroupReceipts(converted, convID)
			}

			total := w.storeMessagesForConversation(convID, converted)
			fmt.Printf("WhatsApp: Cached %d messages from history for %s (total stored: %d)\n", len(converted), convID, total)
		}
	}
}

func (w *WhatsAppProvider) GetConversationHistory(conversationID string, limit int, beforeTimestamp *time.Time) ([]models.Message, error) {
	if conversationID == "" {
		return []models.Message{}, fmt.Errorf("conversation ID is required")
	}

	fmt.Printf("WhatsApp: [HISTORY] GetConversationHistory called for conversation %s\n", conversationID)

	// Parse conversation ID to determine if it's a group
	chatJID, err := types.ParseJID(conversationID)
	if err != nil {
		return []models.Message{}, fmt.Errorf("invalid conversation ID: %w", err)
	}
	isGroup := chatJID.Server == types.GroupServer

	// Default limit to 20 if not specified
	if limit <= 0 {
		limit = 20
	}

	// If not in cache or beforeTimestamp is specified, load from database
	if beforeTimestamp != nil || db.DB != nil {
		var dbMessages []models.Message
		query := db.DB.Where("protocol_conv_id = ?", conversationID)

		// If beforeTimestamp is specified, only get messages before that timestamp
		if beforeTimestamp != nil {
			query = query.Where("timestamp < ?", *beforeTimestamp)
		}

		// Order by timestamp descending to get newest first, then reverse
		// Preload receipts and reactions to include delivery and read receipts, and reactions
		query = query.Preload("Receipts").Preload("Reactions").Order("timestamp DESC").Limit(limit)

		fmt.Printf("WhatsApp: [HISTORY] Querying messages for conversation %s (limit=%d, beforeTimestamp=%v)\n", conversationID, limit, beforeTimestamp != nil)

		// First, check if there are any call messages in the database for this conversation
		var callMsgCount int64
		if err := db.DB.Model(&models.Message{}).Where("protocol_conv_id = ? AND call_type != ''", conversationID).Count(&callMsgCount).Error; err == nil {
			fmt.Printf("WhatsApp: [HISTORY] Found %d call messages in database for conversation %s\n", callMsgCount, conversationID)
		}

		// Also check if there are call messages with LID or other variations
		// Try to resolve the conversation ID to see if there might be LID versions
		resolvedConvID, err := w.resolveContactID(conversationID)
		if err == nil && resolvedConvID != conversationID {
			var lidCallMsgCount int64
			if err := db.DB.Model(&models.Message{}).Where("protocol_conv_id = ? AND call_type != ''", resolvedConvID).Count(&lidCallMsgCount).Error; err == nil {
				fmt.Printf("WhatsApp: [HISTORY] Found %d call messages in database for resolved conversation ID %s\n", lidCallMsgCount, resolvedConvID)
			}
		}

		// Also search for any call messages that might have this phone number in sender_id
		var senderCallMsgCount int64
		if err := db.DB.Model(&models.Message{}).Where("sender_id = ? AND call_type != ''", conversationID).Count(&senderCallMsgCount).Error; err == nil {
			if senderCallMsgCount > 0 {
				fmt.Printf("WhatsApp: [HISTORY] Found %d call messages with sender_id = %s\n", senderCallMsgCount, conversationID)
			}
		}

		if err := query.Find(&dbMessages).Error; err != nil {
			fmt.Printf("WhatsApp: [HISTORY] ERROR querying messages: %v\n", err)
			return []models.Message{}, err
		}
		fmt.Printf("WhatsApp: [HISTORY] Query returned %d messages for conversation %s\n", len(dbMessages), conversationID)
		if len(dbMessages) > 0 {
			// Count call messages
			callMsgCountInResult := 0
			for _, msg := range dbMessages {
				if msg.CallType != "" {
					callMsgCountInResult++
					fmt.Printf("WhatsApp: [HISTORY] Call message found: ProtocolMsgID=%s, CallType=%s, Timestamp=%s\n",
						msg.ProtocolMsgID, msg.CallType, msg.Timestamp.Format("2006-01-02 15:04:05"))
				}
			}
			fmt.Printf("WhatsApp: [HISTORY] Found %d total messages (%d call messages) for conversation %s\n", len(dbMessages), callMsgCountInResult, conversationID)

			// If we found call messages in DB but not in result, there might be a filtering issue
			if callMsgCount > 0 && callMsgCountInResult == 0 {
				fmt.Printf("WhatsApp: [HISTORY] WARNING - Found %d call messages in DB but 0 in query result. Checking why...\n", callMsgCount)
				var allCallMsgs []models.Message
				if err := db.DB.Where("protocol_conv_id = ? AND call_type != ''", conversationID).
					Order("timestamp DESC").Limit(10).Find(&allCallMsgs).Error; err == nil {
					for _, msg := range allCallMsgs {
						fmt.Printf("WhatsApp: [HISTORY] Call message in DB: ProtocolMsgID=%s, CallType=%s, Timestamp=%s, ProtocolConvID=%s\n",
							msg.ProtocolMsgID, msg.CallType, msg.Timestamp.Format("2006-01-02 15:04:05"), msg.ProtocolConvID)
					}
				}
			}
			// Reverse to get oldest first
			for i, j := 0, len(dbMessages)-1; i < j; i, j = i+1, j-1 {
				dbMessages[i], dbMessages[j] = dbMessages[j], dbMessages[i]
			}

			// Enrich messages with sender names and avatars
			w.enrichMessagesWithSenderInfo(dbMessages, chatJID, isGroup)

			// If beforeTimestamp is nil (initial load), update cache
			if beforeTimestamp == nil {
				w.mu.Lock()
				if w.conversationMessages == nil {
					w.conversationMessages = make(map[string][]models.Message)
				}
				// Merge with existing cache, avoiding duplicates
				existing := w.conversationMessages[conversationID]
				existingMap := make(map[string]bool)
				for _, msg := range existing {
					existingMap[msg.ProtocolMsgID] = true
				}
				for _, msg := range dbMessages {
					if !existingMap[msg.ProtocolMsgID] {
						existing = append(existing, msg)
					}
				}
				// Sort by timestamp
				sort.SliceStable(existing, func(i, j int) bool {
					return existing[i].Timestamp.Before(existing[j].Timestamp)
				})
				w.conversationMessages[conversationID] = existing
				w.mu.Unlock()
			}

			return dbMessages, nil
		}
	}

	// Fallback to cache if available
	w.mu.RLock()
	messages, ok := w.conversationMessages[conversationID]
	w.mu.RUnlock()

	if ok && len(messages) > 0 {
		// Filter by beforeTimestamp if specified
		var filtered []models.Message
		if beforeTimestamp != nil {
			for _, msg := range messages {
				if msg.Timestamp.Before(*beforeTimestamp) {
					filtered = append(filtered, msg)
				}
			}
		} else {
			filtered = messages
		}

		// Take last 'limit' messages
		start := 0
		if limit > 0 && len(filtered) > limit {
			start = len(filtered) - limit
		}

		result := make([]models.Message, len(filtered)-start)
		copy(result, filtered[start:])

		// Ensure messages are enriched
		w.enrichMessagesWithSenderInfo(result, chatJID, isGroup)
		return result, nil
	}

	return []models.Message{}, nil
}

// updateLinkedAccountName updates the Username field of a LinkedAccount in the database
// This ensures that names discovered from messages appear in the sidebar
func (w *WhatsAppProvider) updateLinkedAccountName(userID, name string) {
	if db.DB == nil || userID == "" || name == "" || name == userID {
		return
	}

	// Get instance ID for this provider
	w.mu.RLock()
	instanceID := ""
	if w.config != nil {
		if id, ok := w.config["_instance_id"].(string); ok {
			instanceID = id
		}
	}
	w.mu.RUnlock()

	if instanceID == "" {
		return
	}

	// Update LinkedAccount in database
	var account models.LinkedAccount
	err := db.DB.Where("provider_instance_id = ? AND user_id = ?", instanceID, userID).First(&account).Error
	if err == nil {
		// Account exists, update if name is better (not empty, not just the ID)
		oldName := account.Username
		if account.Username == "" || account.Username == userID || (account.Username != name && name != userID) {
			account.Username = name
			account.UpdatedAt = time.Now()
			if err := db.DB.Save(&account).Error; err == nil {
				fmt.Printf("WhatsApp: Updated LinkedAccount name for %s: '%s' -> '%s'\n", userID, oldName, name)
			} else {
				fmt.Printf("WhatsApp: Failed to update LinkedAccount name for %s: %v\n", userID, err)
			}
		}
	} else {
		// Account doesn't exist yet, but we have a name - create it
		// This can happen if a message arrives before the contact is in GetContacts
		newAccount := models.LinkedAccount{
			Protocol:           "whatsapp",
			ProviderInstanceID: instanceID,
			UserID:             userID,
			Username:           name,
			Status:             "offline",
			CreatedAt:          time.Now(),
			UpdatedAt:          time.Now(),
		}
		if err := db.DB.Create(&newAccount).Error; err == nil {
			fmt.Printf("WhatsApp: Created LinkedAccount for %s with name '%s'\n", userID, name)
		}
	}

	// Also update cache
	w.mu.Lock()
	if cached, exists := w.conversations[userID]; exists {
		if cached.Username == "" || cached.Username == userID {
			cached.Username = name
			cached.UpdatedAt = time.Now()
			w.conversations[userID] = cached
		}
	} else {
		// Create entry in cache
		w.conversations[userID] = models.LinkedAccount{
			Protocol:  "whatsapp",
			UserID:    userID,
			Username:  name,
			Status:    "offline",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
	}
	w.mu.Unlock()
}

func (w *WhatsAppProvider) enrichMessagesWithSenderInfo(messages []models.Message, chatJID types.JID, isGroup bool) {
	if len(messages) == 0 {
		return
	}

	// Convert LID sender IDs to phone numbers for consistency with GetGroupParticipants
	if isGroup {
		for i := range messages {
			msg := &messages[i]
			// Resolve sender ID to canonical phone number
			resolvedID, err := w.resolveContactIDForGroup(msg.SenderID, chatJID)
			if err == nil && resolvedID != msg.SenderID {
				msg.SenderID = resolvedID
				fmt.Printf("WhatsApp: Resolved sender ID %s to %s in enriched message\n", msg.SenderID, resolvedID)
			}
		}
	} else {
		// For 1-on-1 chats, also resolve LIDs
		for i := range messages {
			msg := &messages[i]
			resolvedID, err := w.resolveContactID(msg.SenderID)
			if err == nil && resolvedID != msg.SenderID {
				msg.SenderID = resolvedID
			}
		}
	}

	for i := range messages {
		msg := &messages[i]

		// Skip if already enriched
		if msg.SenderName != "" && msg.SenderAvatarURL != "" {
			continue
		}

		// Parse sender JID
		senderJID, err := types.ParseJID(msg.SenderID)
		if err != nil {
			continue
		}

		// Get sender name
		if msg.SenderName == "" {
			if msg.IsFromMe {
				// Always try to get the actual name, even for messages from me
				if isGroup {
					msg.SenderName = w.lookupSenderNameInGroup(senderJID, chatJID)
				} else {
					msg.SenderName = w.lookupSenderName(senderJID)
				}
				// Fallback to sender ID if still empty
				if msg.SenderName == "" {
					msg.SenderName = msg.SenderID
				}
			} else {
				if isGroup {
					msg.SenderName = w.lookupSenderNameInGroup(senderJID, chatJID)
				} else {
					msg.SenderName = w.lookupSenderName(senderJID)
				}
				// Fallback to sender ID if still empty
				if msg.SenderName == "" {
					msg.SenderName = msg.SenderID
				}
			}
		} else if msg.SenderName == "You" {
			// Clean up old "You" values from DB and replace with actual name
			if isGroup {
				msg.SenderName = w.lookupSenderNameInGroup(senderJID, chatJID)
			} else {
				msg.SenderName = w.lookupSenderName(senderJID)
			}
			if msg.SenderName == "" {
				msg.SenderName = msg.SenderID
			}
			fmt.Printf("WhatsApp: Cleaned up 'You' in message %s, new name: %s\n", msg.ProtocolMsgID, msg.SenderName)
		}

		// Update LinkedAccount in database with discovered name (for 1-on-1 chats)
		// This ensures the name appears in the sidebar
		if !isGroup && !msg.IsFromMe && msg.SenderName != "" && msg.SenderName != msg.SenderID {
			w.updateLinkedAccountName(msg.SenderID, msg.SenderName)
		}

		// Get sender avatar URL (only for messages not from me)
		if !msg.IsFromMe && msg.SenderAvatarURL == "" {
			// First, try to get avatar from cached conversations
			w.mu.RLock()
			if cached, exists := w.conversations[msg.SenderID]; exists && cached.AvatarURL != "" {
				msg.SenderAvatarURL = cached.AvatarURL
				w.mu.RUnlock()
			} else {
				w.mu.RUnlock()

				// Check if avatar is already being loaded to avoid duplicate requests
				w.avatarLoadingMu.Lock()
				isLoading := w.avatarLoading[msg.SenderID]
				if !isLoading {
					w.avatarLoading[msg.SenderID] = true
				}
				w.avatarLoadingMu.Unlock()

				// Only try to load if not already loading
				if !isLoading {
					// Try to get avatar URL (this will cache it if found)
					msg.SenderAvatarURL = w.getProfilePictureURL(senderJID)

					// Mark as no longer loading
					w.avatarLoadingMu.Lock()
					delete(w.avatarLoading, msg.SenderID)
					w.avatarLoadingMu.Unlock()

					// If we got an avatar, update the cache
					if msg.SenderAvatarURL != "" {
						w.mu.Lock()
						if cached, exists := w.conversations[msg.SenderID]; exists {
							cached.AvatarURL = msg.SenderAvatarURL
							// Also update name if we have one
							if msg.SenderName != "" && msg.SenderName != msg.SenderID {
								cached.Username = msg.SenderName
							}
							w.conversations[msg.SenderID] = cached
						} else {
							// Create a new entry in cache for this sender
							w.conversations[msg.SenderID] = models.LinkedAccount{
								Protocol:  "whatsapp",
								UserID:    msg.SenderID,
								Username:  msg.SenderName,
								AvatarURL: msg.SenderAvatarURL,
								Status:    "offline",
								CreatedAt: msg.Timestamp,
								UpdatedAt: msg.Timestamp,
							}
						}
						w.mu.Unlock()

						// Update database for 1-on-1 chats (name and avatar)
						if !isGroup && !msg.IsFromMe && msg.SenderName != "" && msg.SenderName != msg.SenderID {
							w.updateLinkedAccountName(msg.SenderID, msg.SenderName)
						}
					} else {
						// Avatar not available - mark in cache to avoid repeated attempts
						// Use a special marker to indicate we tried and failed
						w.mu.Lock()
						if cached, exists := w.conversations[msg.SenderID]; exists {
							// Keep existing entry, just mark that we tried
							w.conversations[msg.SenderID] = cached
						} else {
							// Create entry without avatar to mark that we tried
							w.conversations[msg.SenderID] = models.LinkedAccount{
								Protocol:  "whatsapp",
								UserID:    msg.SenderID,
								Username:  msg.SenderName,
								AvatarURL: "", // Empty means we tried and it's not available
								Status:    "offline",
								CreatedAt: msg.Timestamp,
								UpdatedAt: msg.Timestamp,
							}
						}
						w.mu.Unlock()
					}
				}
			}
		}

		// Enrich quoted message sender name
		if msg.QuotedSenderID != nil && *msg.QuotedSenderID != "" {
			quotedSenderJID, err := types.ParseJID(*msg.QuotedSenderID)
			if err == nil {
				var quotedSenderName string
				if isGroup {
					quotedSenderName = w.lookupSenderNameInGroup(quotedSenderJID, chatJID)
				} else {
					quotedSenderName = w.lookupSenderName(quotedSenderJID)
				}
				// Set the quoted sender name (not persisted, for display only)
				if quotedSenderName != "" {
					msg.QuotedSenderName = quotedSenderName
				} else {
					// Fallback to sender ID
					msg.QuotedSenderName = *msg.QuotedSenderID
				}
			}
		}
	}
}

func (w *WhatsAppProvider) loadMessagesFromDatabase() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadMessagesFromDatabaseLocked()
}

func (w *WhatsAppProvider) loadMessagesFromDatabaseLocked() {
	if db.DB == nil {
		return
	}

	// Load messages grouped by conversation
	// Preload receipts and reactions to include delivery and read receipts, and reactions
	var messages []models.Message
	if err := db.DB.Preload("Receipts").Preload("Reactions").Order("protocol_conv_id, timestamp ASC").Find(&messages).Error; err != nil {
		fmt.Printf("WhatsApp: Failed to load messages from database: %v\n", err)
		return
	}

	if len(messages) == 0 {
		// Still try to load conversations even if no messages
		w.loadConversationsFromDatabaseLocked()
		return
	}

	if w.conversationMessages == nil {
		w.conversationMessages = make(map[string][]models.Message)
	}

	// Group messages by conversation ID
	for _, msg := range messages {
		if msg.ProtocolConvID != "" {
			w.conversationMessages[msg.ProtocolConvID] = append(w.conversationMessages[msg.ProtocolConvID], msg)
		}
	}

	// Sort messages within each conversation
	// Note: We don't enrich messages here because enrichMessagesWithSenderInfo
	// needs to lock the mutex, which is already locked. Enrichment will happen
	// lazily when GetConversationHistory is called.
	for convID, convMessages := range w.conversationMessages {
		// Sort messages
		sort.SliceStable(convMessages, func(i, j int) bool {
			return convMessages[i].Timestamp.Before(convMessages[j].Timestamp)
		})
		// Update the cache with sorted messages
		w.conversationMessages[convID] = convMessages
	}

	fmt.Printf("WhatsApp: Loaded %d messages from database across %d conversations\n", len(messages), len(w.conversationMessages))

	// Also load conversations from database
	w.loadConversationsFromDatabaseLocked()
}

func (w *WhatsAppProvider) SendMessage(conversationID string, text string, file *core.Attachment, threadID *string) (*models.Message, error) {
	if w.client == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	markUnused(file, threadID)

	// Parse conversation ID (JID)
	jid, err := types.ParseJID(conversationID)
	if err != nil {
		return nil, fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Create message
	msg := &waE2E.Message{
		Conversation: &text,
	}

	// Send message
	resp, err := w.client.SendMessage(w.ctx, jid, msg)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	// Convert to our Message model
	sentMessage := &models.Message{
		ProtocolConvID: conversationID,
		ProtocolMsgID:  resp.ID,
		SenderID:       w.client.Store.ID.String(),
		Body:           text,
		Timestamp:      time.Now(),
		IsFromMe:       true,
	}

	// Store message in conversation cache and database
	w.appendMessageToConversation(sentMessage)

	// Emit MessageEvent to notify frontend
	select {
	case w.eventChan <- core.MessageEvent{Message: *sentMessage}:
		fmt.Printf("WhatsApp: MessageEvent emitted successfully for sent message %s\n", sentMessage.ProtocolMsgID)
	default:
		fmt.Printf("WhatsApp: WARNING - Failed to emit MessageEvent (channel full) for sent message %s\n", sentMessage.ProtocolMsgID)
	}

	return sentMessage, nil
}

func (w *WhatsAppProvider) SendReply(conversationID string, text string, quotedMessageID string) (*models.Message, error) {
	fmt.Printf("WhatsApp: SendReply called: conversationID=%s, quotedMessageID=%s\n", conversationID, quotedMessageID)
	if w.client == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	// Parse conversation ID (JID)
	jid, err := types.ParseJID(conversationID)
	if err != nil {
		return nil, fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Find the quoted message
	var quotedMessage *models.Message
	w.mu.RLock()
	if msgs, ok := w.conversationMessages[conversationID]; ok {
		for _, msg := range msgs {
			if msg.ProtocolMsgID == quotedMessageID {
				quotedMessage = &msg
				break
			}
		}
	}
	w.mu.RUnlock()

	// If not found in cache, try database
	if quotedMessage == nil && db.DB != nil {
		var dbMsg models.Message
		if err := db.DB.Where("protocol_msg_id = ?", quotedMessageID).First(&dbMsg).Error; err == nil {
			quotedMessage = &dbMsg
			fmt.Printf("WhatsApp: SendReply: Found quoted message in database\n")
		} else {
			fmt.Printf("WhatsApp: SendReply: Quoted message not found in database: %v\n", err)
		}
	}

	if quotedMessage == nil {
		return nil, fmt.Errorf("quoted message not found: %s", quotedMessageID)
	}

	// Parse sender JID from quoted message
	senderJID, err := types.ParseJID(quotedMessage.SenderID)
	if err != nil {
		fmt.Printf("WhatsApp: SendReply: Failed to parse sender JID: %v\n", err)
		return nil, fmt.Errorf("invalid sender ID in quoted message: %w", err)
	}

	// Create ExtendedTextMessage with ContextInfo for the quoted message
	msg := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: &text,
			ContextInfo: &waE2E.ContextInfo{
				StanzaID:    &quotedMessageID,
				Participant: proto.String(senderJID.String()),
				QuotedMessage: &waE2E.Message{
					Conversation: &quotedMessage.Body,
				},
			},
		},
	}

	// Send message
	resp, err := w.client.SendMessage(w.ctx, jid, msg)
	if err != nil {
		return nil, fmt.Errorf("failed to send reply: %w", err)
	}

	// Get quoted sender name for display
	var quotedSenderName string
	isGroup := jid.Server == types.GroupServer
	if isGroup {
		quotedSenderName = w.lookupSenderNameInGroup(senderJID, jid)
	} else {
		quotedSenderName = w.lookupSenderName(senderJID)
	}
	if quotedSenderName == "" {
		quotedSenderName = quotedMessage.SenderID
	}

	// Convert to our Message model
	sentMessage := &models.Message{
		ProtocolConvID:   conversationID,
		ProtocolMsgID:    resp.ID,
		SenderID:         w.client.Store.ID.String(),
		Body:             text,
		Timestamp:        time.Now(),
		IsFromMe:         true,
		QuotedMessageID:  &quotedMessageID,
		QuotedSenderID:   &quotedMessage.SenderID,
		QuotedBody:       &quotedMessage.Body,
		QuotedSenderName: quotedSenderName,
	}

	// Store message in conversation cache and database
	w.appendMessageToConversation(sentMessage)

	// Emit MessageEvent to notify frontend
	select {
	case w.eventChan <- core.MessageEvent{Message: *sentMessage}:
		fmt.Printf("WhatsApp: MessageEvent emitted successfully for sent reply %s\n", sentMessage.ProtocolMsgID)
	default:
		fmt.Printf("WhatsApp: WARNING - Failed to emit MessageEvent (channel full) for sent reply %s\n", sentMessage.ProtocolMsgID)
	}

	return sentMessage, nil
}

func (w *WhatsAppProvider) EditMessage(conversationID string, messageID string, newText string) (*models.Message, error) {
	fmt.Printf("WhatsApp: EditMessage called: conversationID=%s, messageID=%s\n", conversationID, messageID)
	if w.client == nil {
		fmt.Printf("WhatsApp: EditMessage error: client not initialized\n")
		return nil, fmt.Errorf("client not initialized")
	}

	// Parse conversation ID (JID)
	jid, err := types.ParseJID(conversationID)
	if err != nil {
		fmt.Printf("WhatsApp: EditMessage error: invalid conversation ID: %v\n", err)
		return nil, fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Find the original message
	var originalMsg *models.Message
	w.mu.RLock()
	if msgs, ok := w.conversationMessages[conversationID]; ok {
		for _, msg := range msgs {
			if msg.ProtocolMsgID == messageID {
				originalMsg = &msg
				break
			}
		}
	}
	w.mu.RUnlock()

	// If not found in cache, try database
	if originalMsg == nil && db.DB != nil {
		var dbMsg models.Message
		if err := db.DB.Where("protocol_msg_id = ?", messageID).First(&dbMsg).Error; err == nil {
			originalMsg = &dbMsg
			fmt.Printf("WhatsApp: EditMessage: Found message in database\n")
		} else {
			fmt.Printf("WhatsApp: EditMessage: Message not found in database: %v\n", err)
		}
	}

	if originalMsg == nil {
		fmt.Printf("WhatsApp: EditMessage error: message not found: %s\n", messageID)
		return nil, fmt.Errorf("message not found: %s", messageID)
	}

	// Create a ProtocolMessage of type MESSAGE_EDIT
	protocolMsg := &waProto.ProtocolMessage{
		Type: waProto.ProtocolMessage_MESSAGE_EDIT.Enum(),
		Key: &waProto.MessageKey{
			RemoteJID: proto.String(conversationID),
			ID:        proto.String(messageID),
		},
		EditedMessage: &waE2E.Message{
			Conversation: &newText,
		},
	}

	// Create the message with the protocol message
	msg := &waE2E.Message{
		ProtocolMessage: protocolMsg,
	}

	fmt.Printf("WhatsApp: EditMessage: Sending edit protocol message to WhatsApp server\n")
	// Send the edit message
	_, err = w.client.SendMessage(w.ctx, jid, msg)
	if err != nil {
		fmt.Printf("WhatsApp: EditMessage error: failed to send edit message: %v\n", err)
		return nil, fmt.Errorf("failed to send edit message: %w", err)
	}
	fmt.Printf("WhatsApp: EditMessage: Edit message sent to WhatsApp server\n")

	// Update the message in our cache and database
	updatedMessage := *originalMsg
	updatedMessage.Body = newText
	editedAt := time.Now()
	updatedMessage.IsEdited = true
	updatedMessage.EditedTimestamp = &editedAt

	w.mu.Lock()
	if msgs, ok := w.conversationMessages[conversationID]; ok {
		for idx := range msgs {
			if msgs[idx].ProtocolMsgID == messageID {
				msgs[idx] = updatedMessage
				w.conversationMessages[conversationID][idx] = msgs[idx]
				break
			}
		}
	}
	w.mu.Unlock()

	// Update in database
	if db.DB != nil {
		updates := map[string]interface{}{
			"body":             newText,
			"is_edited":        true,
			"edited_timestamp": editedAt,
		}
		if err := db.DB.Model(&models.Message{}).
			Where("protocol_msg_id = ?", messageID).
			Updates(updates).Error; err != nil {
			fmt.Printf("WhatsApp: Failed to update message body in database: %v\n", err)
		}
	}

	// Emit MessageEvent to notify frontend
	select {
	case w.eventChan <- core.MessageEvent{Message: updatedMessage}:
		fmt.Printf("WhatsApp: MessageEvent emitted for edited message %s\n", messageID)
	default:
		fmt.Printf("WhatsApp: WARNING - Failed to emit MessageEvent for edited message %s\n", messageID)
	}

	return &updatedMessage, nil
}

func (w *WhatsAppProvider) DeleteMessage(conversationID string, messageID string) error {
	fmt.Printf("WhatsApp: DeleteMessage called: conversationID=%s, messageID=%s\n", conversationID, messageID)
	if w.client == nil {
		fmt.Printf("WhatsApp: DeleteMessage error: client not initialized\n")
		return fmt.Errorf("client not initialized")
	}

	// Parse conversation ID (JID)
	jid, err := types.ParseJID(conversationID)
	if err != nil {
		fmt.Printf("WhatsApp: DeleteMessage error: invalid conversation ID: %v\n", err)
		return fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Find the message to verify it exists and get its details
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
			fmt.Printf("WhatsApp: DeleteMessage: Found message in database\n")
		} else {
			fmt.Printf("WhatsApp: DeleteMessage: Message not found in database: %v\n", err)
		}
	}

	if message == nil {
		fmt.Printf("WhatsApp: DeleteMessage error: message not found: %s\n", messageID)
		return fmt.Errorf("message not found: %s", messageID)
	}

	fmt.Printf("WhatsApp: DeleteMessage: Found message, revoking on WhatsApp server\n")
	// Revoke the message using WhatsApp's revoke functionality
	_, err = w.client.RevokeMessage(w.ctx, jid, types.MessageID(messageID))
	if err != nil {
		fmt.Printf("WhatsApp: DeleteMessage error: failed to revoke message: %v\n", err)
		return fmt.Errorf("failed to revoke message: %w", err)
	}
	fmt.Printf("WhatsApp: DeleteMessage: Message revoked on WhatsApp server\n")

	// Mark message as deleted in our cache and database
	deletedBy := ""
	if w.client.Store != nil && w.client.Store.ID != nil {
		deletedBy = w.client.Store.ID.String()
	}
	deletedAt := time.Now()

	updated := w.markMessageAsDeleted(conversationID, messageID, deletedBy, "deleted", deletedAt)
	if updated == nil {
		fmt.Printf("WhatsApp: Warning - Message %s was revoked but not found in cache\n", messageID)
	}

	// Emit MessageEvent to notify frontend
	if updated != nil {
		select {
		case w.eventChan <- core.MessageEvent{Message: *updated}:
			fmt.Printf("WhatsApp: MessageEvent emitted for deleted message %s\n", messageID)
		default:
			fmt.Printf("WhatsApp: WARNING - Failed to emit MessageEvent for deleted message %s\n", messageID)
		}
	}

	return nil
}

func (w *WhatsAppProvider) SendFile(conversationID string, file *core.Attachment, threadID *string) (*models.Message, error) {
	if w.client == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	if file == nil {
		return nil, fmt.Errorf("file is required")
	}

	markUnused(threadID)

	// Parse conversation ID (JID)
	jid, err := types.ParseJID(conversationID)
	if err != nil {
		return nil, fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Determine media type and upload with correct type
	var attachmentType string
	var uploadType whatsmeow.MediaType

	mimeType := strings.ToLower(file.MimeType)
	if strings.HasPrefix(mimeType, "image/") {
		uploadType = whatsmeow.MediaImage
		attachmentType = "image"
	} else if strings.HasPrefix(mimeType, "video/") {
		uploadType = whatsmeow.MediaVideo
		attachmentType = "video"
	} else if strings.HasPrefix(mimeType, "audio/") {
		uploadType = whatsmeow.MediaAudio
		attachmentType = "audio"
	} else {
		uploadType = whatsmeow.MediaDocument
		attachmentType = "document"
	}

	// Upload the file
	uploadResp, err := w.client.Upload(w.ctx, file.Data, uploadType)
	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	// Create message based on media type
	var msg *waE2E.Message
	if attachmentType == "image" {
		msg = &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				URL:           &uploadResp.URL,
				DirectPath:    &uploadResp.DirectPath,
				Mimetype:      &file.MimeType,
				Caption:       nil,
				FileSHA256:    uploadResp.FileSHA256,
				FileLength:    &uploadResp.FileLength,
				MediaKey:      uploadResp.MediaKey,
				FileEncSHA256: uploadResp.FileEncSHA256,
			},
		}
	} else if attachmentType == "video" {
		msg = &waE2E.Message{
			VideoMessage: &waE2E.VideoMessage{
				URL:           &uploadResp.URL,
				DirectPath:    &uploadResp.DirectPath,
				Mimetype:      &file.MimeType,
				Caption:       nil,
				FileSHA256:    uploadResp.FileSHA256,
				FileLength:    &uploadResp.FileLength,
				MediaKey:      uploadResp.MediaKey,
				FileEncSHA256: uploadResp.FileEncSHA256,
			},
		}
	} else if attachmentType == "audio" {
		msg = &waE2E.Message{
			AudioMessage: &waE2E.AudioMessage{
				URL:           &uploadResp.URL,
				DirectPath:    &uploadResp.DirectPath,
				Mimetype:      &file.MimeType,
				FileSHA256:    uploadResp.FileSHA256,
				FileLength:    &uploadResp.FileLength,
				MediaKey:      uploadResp.MediaKey,
				FileEncSHA256: uploadResp.FileEncSHA256,
			},
		}
	} else {
		// Send as document
		fileName := file.FileName
		if fileName == "" {
			fileName = "file"
		}
		msg = &waE2E.Message{
			DocumentMessage: &waE2E.DocumentMessage{
				URL:           &uploadResp.URL,
				DirectPath:    &uploadResp.DirectPath,
				Mimetype:      &file.MimeType,
				FileName:      &fileName,
				FileSHA256:    uploadResp.FileSHA256,
				FileLength:    &uploadResp.FileLength,
				MediaKey:      uploadResp.MediaKey,
				FileEncSHA256: uploadResp.FileEncSHA256,
			},
		}
	}

	// Send message
	resp, err := w.client.SendMessage(w.ctx, jid, msg)
	if err != nil {
		return nil, fmt.Errorf("failed to send file: %w", err)
	}

	// Cache the file locally
	configDir, err := os.UserConfigDir()
	if err == nil {
		cacheDir := filepath.Join(configDir, "Loom", "whatsapp", "attachments")
		os.MkdirAll(cacheDir, 0700)

		hash := sha256.Sum256([]byte(resp.ID + attachmentType))
		ext := filepath.Ext(file.FileName)
		if ext == "" {
			// Determine extension from mime type
			switch {
			case strings.HasPrefix(mimeType, "image/"):
				ext = ".jpg"
			case strings.HasPrefix(mimeType, "video/"):
				ext = ".mp4"
			case strings.HasPrefix(mimeType, "audio/"):
				ext = ".mp3"
			case mimeType == "application/pdf":
				ext = ".pdf"
			default:
				ext = ".bin"
			}
		}
		filename := hex.EncodeToString(hash[:]) + ext
		cachePath := filepath.Join(cacheDir, filename)

		// Save file to cache
		if err := os.WriteFile(cachePath, file.Data, 0644); err == nil {
			// Create attachment info
			attachment := models.Attachment{
				Type:     attachmentType,
				URL:      cachePath,
				FileName: file.FileName,
				FileSize: int64(file.FileSize),
				MimeType: file.MimeType,
			}

			// Convert to JSON for storage
			attachmentsJSON, _ := json.Marshal([]models.Attachment{attachment})

			// Convert to our Message model
			sentMessage := &models.Message{
				ProtocolConvID: conversationID,
				ProtocolMsgID:  resp.ID,
				SenderID:       w.client.Store.ID.String(),
				Body:           "",
				Attachments:    string(attachmentsJSON),
				Timestamp:      time.Now(),
				IsFromMe:       true,
			}

			// Store message in conversation cache and database
			w.appendMessageToConversation(sentMessage)

			// Emit MessageEvent to notify frontend
			select {
			case w.eventChan <- core.MessageEvent{Message: *sentMessage}:
				fmt.Printf("WhatsApp: MessageEvent emitted successfully for sent file %s\n", sentMessage.ProtocolMsgID)
			default:
				fmt.Printf("WhatsApp: WARNING - Failed to emit MessageEvent (channel full) for sent file %s\n", sentMessage.ProtocolMsgID)
			}

			return sentMessage, nil
		}
	}

	// If caching failed, still return the message without attachment info
	sentMessage := &models.Message{
		ProtocolConvID: conversationID,
		ProtocolMsgID:  resp.ID,
		SenderID:       w.client.Store.ID.String(),
		Body:           "",
		Timestamp:      time.Now(),
		IsFromMe:       true,
	}

	w.appendMessageToConversation(sentMessage)

	select {
	case w.eventChan <- core.MessageEvent{Message: *sentMessage}:
		fmt.Printf("WhatsApp: MessageEvent emitted successfully for sent file %s\n", sentMessage.ProtocolMsgID)
	default:
		fmt.Printf("WhatsApp: WARNING - Failed to emit MessageEvent (channel full) for sent file %s\n", sentMessage.ProtocolMsgID)
	}

	return sentMessage, nil
}

// inferGroupReceipts infers delivery/read receipts for group messages based on participant activity.
// Logic: If at least 1 participant replied/reacted → all get "delivered"
//
//	If ALL participants replied/reacted → all get "read"
func (w *WhatsAppProvider) inferGroupReceipts(messages []models.Message, conversationID string) {
	fmt.Printf("WhatsApp: inferGroupReceipts called for %s with %d messages\n", conversationID, len(messages))

	if len(messages) == 0 {
		return
	}

	// Only process group chats
	if len(conversationID) < 5 || conversationID[len(conversationID)-5:] != "@g.us" {
		fmt.Printf("WhatsApp: Skipping inference - not a group chat: %s\n", conversationID)
		return
	}

	// Get current user ID
	var currentUserID string
	w.mu.RLock()
	if w.client != nil && w.client.Store != nil && w.client.Store.ID != nil {
		currentUserID = w.client.Store.ID.String()
	}
	w.mu.RUnlock()
	if currentUserID == "" {
		return
	}

	// Build a map of participant activity (latest timestamp of reply or reaction)
	participantActivity := make(map[string]time.Time)

	// Collect all participants who have sent messages or reactions
	for _, msg := range messages {
		if msg.SenderID == currentUserID {
			continue // Skip current user's messages
		}

		// Track message timestamp as activity
		if existing, ok := participantActivity[msg.SenderID]; !ok || msg.Timestamp.After(existing) {
			participantActivity[msg.SenderID] = msg.Timestamp
		}

		// Track reactions as activity
		for _, reaction := range msg.Reactions {
			if reaction.UserID == currentUserID {
				continue // Skip current user's reactions
			}
			if existing, ok := participantActivity[reaction.UserID]; !ok || reaction.CreatedAt.After(existing) {
				participantActivity[reaction.UserID] = reaction.CreatedAt
			}
		}
	}

	if len(participantActivity) == 0 {
		fmt.Printf("WhatsApp: No participant activity found for group %s\n", conversationID)
		return
	}

	fmt.Printf("WhatsApp: Found %d active participants in group %s\n", len(participantActivity), conversationID)

	// Process each message sent by current user
	for i := range messages {
		msg := &messages[i]

		// Only process messages from current user
		if !msg.IsFromMe {
			continue
		}

		// Skip if message already has receipts from actual events
		if len(msg.Receipts) > 0 {
			continue
		}

		// Count participants with activity after this message
		participantsWithActivity := 0
		for _, activityTime := range participantActivity {
			if activityTime.After(msg.Timestamp) {
				participantsWithActivity++
			}
		}

		if participantsWithActivity == 0 {
			continue // No activity after this message
		}

		// Determine receipt type
		var receiptType string
		if participantsWithActivity == len(participantActivity) {
			// ALL participants have activity → "read"
			receiptType = "read"
		} else {
			// At least 1 participant has activity → "delivered"
			receiptType = "delivery"
		}

		// Create receipts for all participants
		receipts := make([]models.MessageReceipt, 0, len(participantActivity))
		for userID := range participantActivity {
			receipts = append(receipts, models.MessageReceipt{
				UserID:      userID,
				ReceiptType: receiptType,
				Timestamp:   msg.Timestamp, // Use message timestamp as we don't have exact receipt time
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			})
		}

		msg.Receipts = receipts
		fmt.Printf("WhatsApp: Inferred %d %s receipts for group message %s\n", len(receipts), receiptType, msg.ProtocolMsgID)
	}
}
