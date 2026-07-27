package googlechat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
)

func (p *GoogleChatProvider) GetConversationHistory(convID string, limit int, beforeTS *time.Time, sinceTS *time.Time) ([]models.Message, error) {
	if p.getHTTPClient() == nil {
		return nil, fmt.Errorf("not connected")
	}
	if limit <= 0 {
		limit = 50
	}

	var filters []string
	if beforeTS != nil {
		filters = append(filters, `createTime < "`+beforeTS.UTC().Format(time.RFC3339)+`"`)
	}
	if sinceTS != nil {
		filters = append(filters, `createTime > "`+sinceTS.UTC().Format(time.RFC3339)+`"`)
	}

	params := url.Values{
		"pageSize": {fmt.Sprintf("%d", limit)},
		"orderBy":  {"createTime desc"},
	}
	if len(filters) > 0 {
		params.Set("filter", strings.Join(filters, " AND "))
	}

	var resp MessageListResponse
	if err := p.apiGet("/"+convID+"/messages", params, &resp); err != nil {
		return nil, err
	}

	// Collect non-deleted raw messages oldest-first.
	rawMsgs := make([]ChatMessage, 0, len(resp.Messages))
	for i := len(resp.Messages) - 1; i >= 0; i-- {
		if resp.Messages[i].DeleteTime == nil {
			rawMsgs = append(rawMsgs, resp.Messages[i])
		}
	}

	// Build thread root map: Google Chat thread name → parent's ProtocolMsgID.
	threadRoots := buildThreadRoots(rawMsgs)

	// Fix any stale DB records that stored the thread name instead of the parent ID.
	if db.DB != nil {
		for threadName, parentID := range threadRoots {
			db.DB.Model(&models.Message{}).
				Where("thread_id = ? AND protocol_conv_id = ?", threadName, convID).
				Update("thread_id", parentID)
		}
	}

	// Cache thread names: msgID → thread name, for use in SendReply.
	p.threadNameByMsgIDMu.Lock()
	for _, msg := range rawMsgs {
		if msg.Thread != nil && msg.Thread.Name != "" && msg.Name != "" {
			p.threadNameByMsgID[msg.Name] = msg.Thread.Name
		}
	}
	p.threadNameByMsgIDMu.Unlock()

	selfID := p.getSelfID()
	messages := make([]models.Message, 0, len(rawMsgs))
	for _, msg := range rawMsgs {
		m := p.convertMessage(msg, convID, selfID)
		m.ThreadID = resolveThreadID(msg, threadRoots)
		messages = append(messages, m)
	}

	p.storeMessagesForConversation(convID, messages)
	return messages, nil
}

func (p *GoogleChatProvider) SendMessage(convID, text string, file *core.Attachment, threadID *string) (*models.Message, error) {
	if p.getHTTPClient() == nil {
		return nil, fmt.Errorf("not connected")
	}

	type threadRef struct {
		Name string `json:"name"`
	}
	type msgBody struct {
		Text   string     `json:"text,omitempty"`
		Thread *threadRef `json:"thread,omitempty"`
	}

	body := msgBody{Text: text}
	path := "/" + convID + "/messages"
	// canonicalThreadID is the parent message's ProtocolMsgID (used as ThreadID in the model).
	// It stays empty when threadID is a thread resource name (e.g. from SendReply).
	var canonicalThreadID string
	if threadID != nil && *threadID != "" {
		threadName := *threadID
		if strings.Contains(threadName, "/messages/") {
			// Caller passed a message resource name — resolve to thread name for the API
			// and keep the original as the canonical parent message ID.
			canonicalThreadID = threadName
			if resolved := p.getMessageThreadName(threadName); resolved != "" {
				threadName = resolved
			}
		}
		body.Thread = &threadRef{Name: threadName}
		path += "?messageReplyOption=REPLY_MESSAGE_OR_FAIL"
	}

	var result ChatMessage
	if err := p.apiPost(path, body, &result); err != nil {
		return nil, err
	}

	selfID := p.getSelfID()
	m := p.convertMessage(result, convID, selfID)
	// We know we sent this message — enforce correct identity regardless of API response.
	m.IsFromMe = true
	if m.SenderID == "" && selfID != "" {
		m.SenderID = selfID
	}
	if m.SenderName == "" {
		p.mu.RLock()
		m.SenderName = p.selfName
		p.mu.RUnlock()
	}
	if canonicalThreadID != "" {
		m.ThreadID = &canonicalThreadID
	}
	if db.DB != nil {
		convDBID, _ := p.ensureConversation(convID)
		m.ConversationID = convDBID
		db.DB.Create(&m)
	}
	return &m, nil
}

func (p *GoogleChatProvider) SendReply(convID, text, quotedMessageID string) (*models.Message, error) {
	threadName := p.getMessageThreadName(quotedMessageID)
	if threadName == "" {
		return nil, fmt.Errorf("googlechat: could not resolve thread for message %s", quotedMessageID)
	}
	msg, err := p.SendMessage(convID, text, nil, &threadName)
	if err != nil {
		return nil, err
	}
	// Set ThreadID to the parent's ProtocolMsgID (not the Google Chat thread name)
	// so the frontend can correctly group this reply under the parent.
	msg.ThreadID = &quotedMessageID
	if db.DB != nil && msg.ProtocolMsgID != "" {
		db.DB.Model(&models.Message{}).
			Where("protocol_msg_id = ?", msg.ProtocolMsgID).
			Update("thread_id", quotedMessageID)
	}
	return msg, nil
}

func (p *GoogleChatProvider) SendFile(convID string, file *core.Attachment, threadID *string) (*models.Message, error) {
	if file == nil {
		return nil, fmt.Errorf("no file provided")
	}
	text := fmt.Sprintf("[file: %s]", file.FileName)
	return p.SendMessage(convID, text, nil, threadID)
}

func (p *GoogleChatProvider) EditMessage(convID, messageID, newText string) (*models.Message, error) {
	if p.getHTTPClient() == nil {
		return nil, fmt.Errorf("not connected")
	}

	type editBody struct {
		Text string `json:"text"`
	}

	msgPath := ensureMessagePath(convID, messageID)
	var result ChatMessage
	if err := p.apiPatch("/"+msgPath, "text", editBody{Text: newText}, &result); err != nil {
		return nil, err
	}

	if db.DB != nil {
		now := time.Now()
		db.DB.Model(&models.Message{}).
			Where("protocol_msg_id = ?", messageID).
			Updates(map[string]interface{}{
				"body":             newText,
				"is_edited":        true,
				"edited_timestamp": now,
			})
	}

	m := p.convertMessage(result, convID, p.getSelfID())
	return &m, nil
}

func (p *GoogleChatProvider) DeleteMessage(convID, messageID string) error {
	if p.getHTTPClient() == nil {
		return fmt.Errorf("not connected")
	}

	msgPath := ensureMessagePath(convID, messageID)
	if err := p.apiDelete("/" + msgPath); err != nil {
		return err
	}

	if db.DB != nil {
		now := time.Now()
		db.DB.Model(&models.Message{}).
			Where("protocol_msg_id = ?", messageID).
			Updates(map[string]interface{}{
				"is_deleted":        true,
				"deleted_timestamp": now,
			})
	}
	return nil
}

func (p *GoogleChatProvider) GetThreads(parentMessageID string) ([]models.Message, error) {
	if db.DB == nil {
		return nil, nil
	}
	var messages []models.Message
	db.DB.Where("thread_id = ? AND protocol_msg_id != ?", parentMessageID, parentMessageID).
		Order("timestamp ASC").
		Find(&messages)
	return messages, nil
}

func (p *GoogleChatProvider) AddReaction(convID, messageID, emoji string) error {
	if p.getHTTPClient() == nil {
		return fmt.Errorf("not connected")
	}

	type emojiRef struct {
		Unicode string `json:"unicode"`
	}
	type reactionBody struct {
		Emoji emojiRef `json:"emoji"`
	}

	msgPath := ensureMessagePath(convID, messageID)
	if err := p.apiPost("/"+msgPath+"/reactions", reactionBody{Emoji: emojiRef{Unicode: stripVariationSelectors(emoji)}}, nil); err != nil {
		return err
	}

	selfID := p.getSelfID()
	if db.DB != nil && selfID != "" {
		var msg models.Message
		if db.DB.Where("protocol_msg_id = ?", messageID).First(&msg).Error == nil {
			reaction := models.Reaction{
				MessageID: msg.ID,
				UserID:    selfID,
				Emoji:     emoji,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			db.DB.Where("message_id = ? AND user_id = ? AND emoji = ?", msg.ID, selfID, emoji).
				FirstOrCreate(&reaction)
		}
	}
	if selfID != "" {
		p.emit(core.ReactionEvent{
			InstanceID:     p.getInstanceID(),
			ConversationID: convID,
			MessageID:      messageID,
			UserID:         selfID,
			Emoji:          emoji,
			Added:          true,
			Timestamp:      time.Now().Unix(),
		})
	}
	return nil
}

// stripVariationSelectors removes Unicode variation selectors from an emoji string.
func stripVariationSelectors(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 0xFE00 && r <= 0xFE0F {
			return -1
		}
		return r
	}, s)
}

func (p *GoogleChatProvider) RemoveReaction(convID, messageID, emoji string) error {
	if p.getHTTPClient() == nil {
		return fmt.Errorf("not connected")
	}

	msgPath := ensureMessagePath(convID, messageID)
	var resp ReactionListResponse
	if err := p.apiGet("/"+msgPath+"/reactions", url.Values{"pageSize": {"100"}}, &resp); err != nil {
		return err
	}

	target := stripVariationSelectors(emoji)
	selfID := p.getSelfID()
	for _, r := range resp.Reactions {
		if r.Emoji == nil || r.User == nil {
			continue
		}
		if r.Emoji.Unicode == target && strings.TrimPrefix(r.User.Name, "users/") == selfID {
			if err := p.apiDelete("/" + r.Name); err != nil {
				return err
			}
			if db.DB != nil && selfID != "" {
				var msg models.Message
				if db.DB.Where("protocol_msg_id = ?", messageID).First(&msg).Error == nil {
					db.DB.Where("message_id = ? AND user_id = ? AND emoji = ?", msg.ID, selfID, emoji).
						Delete(&models.Reaction{})
				}
			}
			p.emit(core.ReactionEvent{
				InstanceID:     p.getInstanceID(),
				ConversationID: convID,
				MessageID:      messageID,
				UserID:         selfID,
				Emoji:          emoji,
				Added:          false,
				Timestamp:      time.Now().Unix(),
			})
			return nil
		}
	}
	return nil
}

func (p *GoogleChatProvider) convertMessage(m ChatMessage, convID, selfID string) models.Message {
	senderID := ""
	senderName := ""
	senderAvatarURL := ""
	if m.Sender != nil {
		senderID = strings.TrimPrefix(m.Sender.Name, "users/")
		senderName = m.Sender.DisplayName
		senderAvatarURL = m.Sender.AvatarUrl

		if senderID != "" {
			p.userMu.Lock()
			p.userCache[senderID] = cachedUser{Name: senderName, AvatarURL: senderAvatarURL}
			p.userMu.Unlock()
		}
	}

	// ThreadID is resolved by the caller via buildThreadRoots + resolveThreadID.
	var threadID *string

	var reactions []models.Reaction
	for _, r := range m.EmojiReactionSummaries {
		if r.Emoji != nil && r.Emoji.Unicode != "" {
			reactions = append(reactions, models.Reaction{Emoji: r.Emoji.Unicode})
		}
	}

	var attachments []models.Attachment
	for _, att := range m.Attachment {
		url := att.DownloadUri
		// Prefer the authenticated media endpoint over the web UI download URL.
		// downloadUri uses chat.google.com which requires browser cookies (401 with OAuth2).
		// The media endpoint uses chat.googleapis.com and works with OAuth2 bearer tokens.
		if att.AttachmentDataRef != nil && att.AttachmentDataRef.ResourceName != "" {
			url = chatAPIBase + "/media/" + att.AttachmentDataRef.ResourceName + "?alt=media"
		}
		attachments = append(attachments, models.Attachment{
			Type:     attachmentTypeFromMime(att.ContentType),
			URL:      url,
			FileName: att.ContentName,
			MimeType: att.ContentType,
		})
	}

	isEdited := m.LastUpdateTime.After(m.CreateTime.Add(time.Second))
	var editedTS *time.Time
	if isEdited {
		t := m.LastUpdateTime
		editedTS = &t
	}

	body := m.Text
	if body == "" {
		body = m.FormattedText
	}

	return models.Message{
		ProtocolConvID:  convID,
		ProtocolMsgID:   m.Name,
		SenderID:        senderID,
		SenderName:      senderName,
		SenderAvatarURL: senderAvatarURL,
		Body:            body,
		Timestamp:       m.CreateTime,
		IsFromMe:        senderID == selfID,
		ThreadID:        threadID,
		Attachments:     attachmentsToJSON(attachments),
		Reactions:       reactions,
		IsDeleted:       m.DeleteTime != nil,
		IsEdited:        isEdited,
		EditedTimestamp: editedTS,
	}
}

// getMessageThreadName returns the Google Chat thread name (spaces/xxx/threads/yyy)
// for a given message ProtocolMsgID. It checks the in-memory cache first, then the API.
// The DB ThreadID field is intentionally NOT used here: it stores the parent message's
// ProtocolMsgID (for grouping in the UI), not the Google Chat thread resource name.
func (p *GoogleChatProvider) getMessageThreadName(messageID string) string {
	p.threadNameByMsgIDMu.RLock()
	if name, ok := p.threadNameByMsgID[messageID]; ok {
		p.threadNameByMsgIDMu.RUnlock()
		return name
	}
	p.threadNameByMsgIDMu.RUnlock()

	var chatMsg ChatMessage
	if err := p.apiGet("/"+messageID, nil, &chatMsg); err == nil && chatMsg.Thread != nil && chatMsg.Thread.Name != "" {
		p.threadNameByMsgIDMu.Lock()
		p.threadNameByMsgID[messageID] = chatMsg.Thread.Name
		p.threadNameByMsgIDMu.Unlock()
		return chatMsg.Thread.Name
	}
	return ""
}

// ensureConversation ensures a Conversation row exists in DB and returns its ID.
func (p *GoogleChatProvider) ensureConversation(convID string) (uint, error) {
	if db.DB == nil || convID == "" {
		return 0, fmt.Errorf("invalid params")
	}

	var conv models.Conversation
	if err := db.DB.Where("protocol_conv_id = ?", convID).First(&conv).Error; err == nil {
		return conv.ID, nil
	}

	instanceID := p.getInstanceID()
	isGroup := p.isGroupSpace(convID)
	displayName := convID

	var linkedAccount models.LinkedAccount
	if db.DB.Where("provider_instance_id = ? AND user_id = ?", instanceID, convID).First(&linkedAccount).Error != nil {
		meta := models.MetaContact{DisplayName: displayName}
		db.DB.Create(&meta)
		linkedAccount = models.LinkedAccount{
			MetaContactID:      meta.ID,
			Protocol:           "googlechat",
			ProviderInstanceID: instanceID,
			UserID:             convID,
			Username:           displayName,
			IsGroup:            isGroup,
		}
		db.DB.Create(&linkedAccount)
	}

	groupName := ""
	if isGroup {
		groupName = linkedAccount.Username
	}
	conv = models.Conversation{
		LinkedAccountID: linkedAccount.ID,
		ProtocolConvID:  convID,
		IsGroup:         isGroup,
		GroupName:       groupName,
	}
	if err := db.DB.Create(&conv).Error; err != nil {
		return 0, err
	}
	db.ContactStore.UpsertConversation(linkedAccount.ID, convID)
	return conv.ID, nil
}

// isGroupSpace checks if a space is a group (not a DM) via the API.
func (p *GoogleChatProvider) isGroupSpace(convID string) bool {
	if p.getHTTPClient() == nil {
		return true
	}
	var space Space
	if err := p.apiGet("/"+convID, nil, &space); err != nil {
		return true
	}
	return space.SpaceType != "DIRECT_MESSAGE"
}

// storeMessagesForConversation persists a batch of messages to the DB.
func (p *GoogleChatProvider) storeMessagesForConversation(convID string, messages []models.Message) {
	if convID == "" || len(messages) == 0 || db.DB == nil {
		return
	}

	convDBID, err := p.ensureConversation(convID)
	if err != nil {
		p.log("GoogleChatProvider.storeMessages: ensureConversation: %v\n", err)
		return
	}

	msgIDs := make([]string, 0, len(messages))
	for _, m := range messages {
		if m.ProtocolMsgID != "" {
			msgIDs = append(msgIDs, m.ProtocolMsgID)
		}
	}

	type existingRecord struct {
		ThreadID *string
	}
	var existing []models.Message
	existingMap := make(map[string]existingRecord)
	if len(msgIDs) > 0 {
		db.DB.Where("protocol_msg_id IN ?", msgIDs).Find(&existing)
		for _, m := range existing {
			existingMap[m.ProtocolMsgID] = existingRecord{ThreadID: m.ThreadID}
		}
	}

	var toCreate []models.Message
	seen := make(map[string]bool)
	for _, m := range messages {
		if m.ProtocolMsgID == "" || seen[m.ProtocolMsgID] {
			continue
		}
		seen[m.ProtocolMsgID] = true
		if rec, exists := existingMap[m.ProtocolMsgID]; exists {
			// Patch thread_id when the API now reports a thread but the DB has none.
			if m.ThreadID != nil && *m.ThreadID != "" && (rec.ThreadID == nil || *rec.ThreadID == "") {
				db.DB.Model(&models.Message{}).
					Where("protocol_msg_id = ?", m.ProtocolMsgID).
					Update("thread_id", *m.ThreadID)
			}
			// Patch attachments for existing messages that still have the old web UI URL.
			if m.Attachments != "" && !strings.Contains(m.Attachments, "chat.google.com") {
				db.DB.Model(&models.Message{}).
					Where("protocol_msg_id = ? AND attachments LIKE ?", m.ProtocolMsgID, "%chat.google.com%").
					Update("attachments", m.Attachments)
			}
			continue
		}
		m.ProtocolConvID = convID
		m.ConversationID = convDBID
		toCreate = append(toCreate, m)
	}

	if len(toCreate) == 0 {
		return
	}

	const batchSize = 100
	for i := 0; i < len(toCreate); i += batchSize {
		end := i + batchSize
		if end > len(toCreate) {
			end = len(toCreate)
		}
		batch := toCreate[i:end]
		var withReactions []models.Message
		for idx := range batch {
			if len(batch[idx].Reactions) > 0 {
				withReactions = append(withReactions, batch[idx])
				batch[idx].Reactions = nil
			}
		}
		if err := db.DB.Create(&batch).Error; err != nil {
			p.log("GoogleChatProvider.storeMessages: batch create: %v\n", err)
			continue
		}
		for _, m := range withReactions {
			var stored models.Message
			if db.DB.Where("protocol_msg_id = ?", m.ProtocolMsgID).First(&stored).Error == nil {
				for i := range m.Reactions {
					m.Reactions[i].MessageID = stored.ID
				}
				db.DB.Create(&m.Reactions)
			}
		}
	}
}

// buildThreadRoots scans a batch of messages and maps each Google Chat thread name
// to the ProtocolMsgID of the non-reply message that started that thread.
func buildThreadRoots(msgs []ChatMessage) map[string]string {
	roots := make(map[string]string)
	for _, m := range msgs {
		if !m.ThreadReply && m.Thread != nil && m.Thread.Name != "" {
			roots[m.Thread.Name] = m.Name
		}
	}
	return roots
}

// resolveThreadID returns the ThreadID that should be stored for a message.
// Replies get the parent's ProtocolMsgID; top-level messages get nil.
func resolveThreadID(msg ChatMessage, roots map[string]string) *string {
	if !msg.ThreadReply || msg.Thread == nil || msg.Thread.Name == "" {
		return nil
	}
	if parentID, ok := roots[msg.Thread.Name]; ok {
		return &parentID
	}
	return nil
}

// resolveThreadParentFromAPI fetches the oldest message in a thread from the
// Google Chat API to determine the parent's ProtocolMsgID.
func (p *GoogleChatProvider) resolveThreadParentFromAPI(spaceName, threadName string) string {
	params := url.Values{
		"pageSize": {"1"},
		"orderBy":  {"createTime asc"},
		"filter":   {`thread.name = "` + threadName + `"`},
	}
	var resp MessageListResponse
	if err := p.apiGet("/"+spaceName+"/messages", params, &resp); err != nil || len(resp.Messages) == 0 {
		return ""
	}
	return resp.Messages[0].Name
}

// ensureMessagePath returns the full resource path for a message.
// If messageID is already "spaces/AAA/messages/BBB", it's returned as-is.
func ensureMessagePath(convID, messageID string) string {
	if strings.HasPrefix(messageID, "spaces/") {
		return messageID
	}
	return convID + "/messages/" + messageID
}

// attachmentTypeFromMime returns the attachment type string for a MIME type.
func attachmentTypeFromMime(mimeType string) string {
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

// attachmentsToJSON serializes attachments to a JSON string.
func attachmentsToJSON(atts []models.Attachment) string {
	if len(atts) == 0 {
		return ""
	}
	b, err := json.Marshal(atts)
	if err != nil {
		return ""
	}
	return string(b)
}

// GetFileData downloads a Google Chat attachment using the OAuth2 client and
// returns it as a base64 data URL. Results are cached on disk.
func (p *GoogleChatProvider) GetFileData(fileURL string) (string, error) {
	client := p.getHTTPClient()
	if client == nil {
		return "", fmt.Errorf("googlechat: not connected")
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config dir: %w", err)
	}
	instanceID := p.getInstanceID()
	cacheDir := filepath.Join(configDir, "Loom", "googlechat", instanceID)
	if err := os.MkdirAll(cacheDir, 0750); err != nil {
		return "", fmt.Errorf("failed to create cache dir: %w", err)
	}

	urlHash := fmt.Sprintf("%x", []byte(fileURL))
	ext := filepath.Ext(filepath.Base(fileURL))
	if ext == "" {
		ext = ".bin"
	}
	cachePath := filepath.Join(cacheDir, urlHash+ext)

	if data, err := os.ReadFile(cachePath); err == nil {
		mimeType := mimeFromExt(ext)
		return fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data)), nil
	}

	resp, err := client.Get(fileURL) // #nosec G107 — URL from trusted provider data
	if err != nil {
		return "", fmt.Errorf("failed to download %s: %w", fileURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("failed to download %s: HTTP %d", fileURL, resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "text/html") {
		return "", fmt.Errorf("googlechat: server returned HTML for %s (token may be expired)", fileURL)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	_ = os.WriteFile(cachePath, data, 0600)

	mimeType := contentType
	if mimeType == "" {
		mimeType = mimeFromExt(ext)
	}
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data)), nil
}

func mimeFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}

// incrementalSync performs a per-conversation forward sync and 24-hour lookback for all
// conversations with recent activity in the local database. Called from SyncHistory as
// a background goroutine on the 2nd+ sync to catch messages that were missed because
// they were read on another client before this sync ran.
func (p *GoogleChatProvider) incrementalSync() {
	if p.getHTTPClient() == nil || db.DB == nil {
		return
	}

	var rawConvs []struct {
		ProtocolConvID string
		LastTimestamp  string // SQLite returns MAX(timestamp) as string
	}
	// Messages are stored in a shared database. Restrict the sync source to
	// conversations owned by this Google Chat instance; without the joins, the
	// provider attempts to call the Google Chat API for WhatsApp/Slack IDs.
	instanceID := p.getInstanceID()
	db.DB.Raw(`
		SELECT m.protocol_conv_id, MAX(m.timestamp) AS last_timestamp
		FROM messages AS m
		JOIN conversations AS c ON c.protocol_conv_id = m.protocol_conv_id
		JOIN linked_accounts AS la ON la.id = c.linked_account_id
		WHERE m.protocol_conv_id != ''
		  AND la.provider_instance_id = ?
		  AND la.protocol = 'googlechat'
		GROUP BY m.protocol_conv_id
	`, instanceID).Scan(&rawConvs)

	for _, r := range rawConvs {
		var lastTS time.Time
		var err error
		lastTS, err = time.Parse(time.RFC3339Nano, r.LastTimestamp)
		if err != nil {
			lastTS, err = time.Parse(time.RFC3339Nano, strings.Replace(r.LastTimestamp, " ", "T", 1))
			if err != nil {
				lastTS, err = time.Parse(time.RFC3339, strings.Replace(r.LastTimestamp, " ", "T", 1))
				if err != nil {
					p.log("GoogleChatProvider.incrementalSync: failed to parse timestamp %q for %s: %v\n", r.LastTimestamp, r.ProtocolConvID, err)
					continue
				}
			}
		}
		// Always forward-sync to avoid missing messages when the app was closed
		// for more than 48h. Restrict the 24h lookback to recent conversations only.
		recentActivity := time.Since(lastTS) < 48*time.Hour
		p.syncOneConversation(r.ProtocolConvID, lastTS, recentActivity)

	}
}

const googleChatLookbackWindow = 24 * time.Hour

// syncOneConversation does a forward sync and, if recentActivity is true, a 24h lookback.
func (p *GoogleChatProvider) syncOneConversation(convID string, lastTS time.Time, recentActivity bool) {
	// Forward sync: messages after the last known timestamp.
	since := lastTS
	newMsgs, err := p.GetConversationHistory(convID, 500, nil, &since)
	if err != nil {
		p.log("GoogleChatProvider.incrementalSync: forward sync failed for %s: %v\n", convID, err)
	} else if len(newMsgs) > 0 {
		p.emit(core.MessageBatchEvent{InstanceID: p.getInstanceID(), ConversationID: convID, Messages: newMsgs})
	}

	if !recentActivity {
		return
	}

	// Lookback: snapshot existing IDs in the 24h window before lastTS,
	// then fetch the same window from the API and emit only truly new messages.
	lookbackSince := lastTS.Add(-googleChatLookbackWindow)
	var existingIDs []string
	db.DB.Model(&models.Message{}).
		Where("protocol_conv_id = ? AND timestamp > ? AND timestamp < ?", convID, lookbackSince, lastTS).
		Pluck("protocol_msg_id", &existingIDs)
	existingSet := make(map[string]bool, len(existingIDs))
	for _, id := range existingIDs {
		existingSet[id] = true
	}

	before := lastTS
	lookbackMsgs, err := p.GetConversationHistory(convID, 500, &before, &lookbackSince)
	if err != nil {
		p.log("GoogleChatProvider.incrementalSync: lookback failed for %s: %v\n", convID, err)
		return
	}
	var missedMsgs []models.Message
	for _, msg := range lookbackMsgs {
		if !existingSet[msg.ProtocolMsgID] {
			p.log("GoogleChatProvider.incrementalSync: found missed message %s for %s\n", msg.ProtocolMsgID, convID)
			missedMsgs = append(missedMsgs, msg)
		}
	}
	if len(missedMsgs) > 0 {
		p.emit(core.MessageBatchEvent{InstanceID: p.getInstanceID(), ConversationID: convID, Messages: missedMsgs})
	}
}
