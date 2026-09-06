package matrix

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
)

type matrixEvent struct {
	Type           string          `json:"type"`
	EventID        string          `json:"event_id"`
	Sender         string          `json:"sender"`
	OriginServerTS int64           `json:"origin_server_ts"`
	StateKey       *string         `json:"state_key,omitempty"`
	Content        json.RawMessage `json:"content"`
	Unsigned       struct {
		RedactedBecause json.RawMessage `json:"redacted_because"`
	} `json:"unsigned"`
}

type messageContent struct {
	MsgType  string `json:"msgtype"`
	Body     string `json:"body"`
	URL      string `json:"url,omitempty"`
	FileName string `json:"filename,omitempty"`
	Info     struct {
		MimeType string `json:"mimetype"`
		Size     int64  `json:"size"`
		W        int    `json:"w"`
		H        int    `json:"h"`
		Duration uint32 `json:"duration"`
	} `json:"info,omitempty"`
	NewContent *messageContent `json:"m.new_content,omitempty"`
	Mentions   *struct {
		UserIDs []string `json:"user_ids,omitempty"`
	} `json:"m.mentions,omitempty"`
	RelatesTo *struct {
		RelType   string `json:"rel_type,omitempty"`
		EventID   string `json:"event_id,omitempty"`
		Key       string `json:"key,omitempty"`
		InReplyTo *struct {
			EventID string `json:"event_id"`
		} `json:"m.in_reply_to,omitempty"`
	} `json:"m.relates_to,omitempty"`
}

func (p *Provider) eventToMessage(roomID string, event matrixEvent) (models.Message, bool) {
	if event.Type != "m.room.message" {
		return models.Message{}, false
	}
	var content messageContent
	if json.Unmarshal(event.Content, &content) != nil {
		return models.Message{}, false
	}
	if content.RelatesTo != nil && content.RelatesTo.RelType == "m.replace" {
		return models.Message{}, false
	}
	p.mu.RLock()
	self := p.userID
	p.mu.RUnlock()
	m := models.Message{ProtocolConvID: p.namespacedRoom(roomID), ProtocolMsgID: event.EventID, SenderID: event.Sender, SenderName: event.Sender, Body: content.Body, Timestamp: time.UnixMilli(event.OriginServerTS), IsFromMe: event.Sender == self}
	if !m.IsFromMe && content.Mentions != nil {
		for _, mentionedUserID := range content.Mentions.UserIDs {
			if mentionedUserID == self {
				m.HighlightReasons = []string{models.HighlightReasonDirectMention}
				break
			}
		}
	}
	if content.RelatesTo != nil {
		if content.RelatesTo.RelType == "m.thread" {
			id := content.RelatesTo.EventID
			m.ThreadID = &id
		}
		if content.RelatesTo.InReplyTo != nil {
			id := content.RelatesTo.InReplyTo.EventID
			m.QuotedMessageID = &id
		}
	}
	if content.URL != "" {
		attachmentType := map[string]string{"m.image": "image", "m.video": "video", "m.audio": "audio"}[content.MsgType]
		if attachmentType == "" {
			attachmentType = "document"
		}
		name := content.FileName
		if name == "" {
			name = content.Body
		}
		att, _ := json.Marshal([]models.Attachment{{Type: attachmentType, URL: p.mediaURL(content.URL), FileName: name, FileSize: content.Info.Size, MimeType: content.Info.MimeType, Duration: content.Info.Duration}})
		m.Attachments = string(att)
	}
	return m, true
}

func (p *Provider) mediaURL(mxc string) string {
	if !strings.HasPrefix(mxc, "mxc://") {
		return mxc
	}
	parts := strings.SplitN(strings.TrimPrefix(mxc, "mxc://"), "/", 2)
	if len(parts) != 2 {
		return ""
	}
	p.mu.RLock()
	base := p.homeserver
	p.mu.RUnlock()
	return base + "/_matrix/media/v3/download/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
}

func (p *Provider) GetConversationHistory(roomID string, limit int, before, since *time.Time) ([]models.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	q := url.Values{"dir": {"b"}, "limit": {fmt.Sprint(limit)}}
	var response struct {
		Chunk []matrixEvent `json:"chunk"`
	}
	if err := p.do(noCancel(), http.MethodGet, p.roomPath(roomID)+"/messages", q, nil, &response); err != nil {
		return nil, err
	}
	messages := make([]models.Message, 0, len(response.Chunk))
	reactions := make(map[string][]models.Reaction)
	for _, event := range response.Chunk {
		if event.Type == "m.reaction" {
			var content messageContent
			if json.Unmarshal(event.Content, &content) == nil && content.RelatesTo != nil && content.RelatesTo.EventID != "" {
				reactions[content.RelatesTo.EventID] = append(reactions[content.RelatesTo.EventID], models.Reaction{
					UserID: event.Sender, Emoji: content.RelatesTo.Key, CreatedAt: time.UnixMilli(event.OriginServerTS),
				})
			}
			continue
		}
		if m, ok := p.eventToMessage(core.StripConvID(roomID), event); ok && (before == nil || m.Timestamp.Before(*before)) && (since == nil || m.Timestamp.After(*since)) {
			m.Reactions = reactions[event.EventID]
			messages = append(messages, m)
		}
	}
	for index := range messages {
		if attached := reactions[messages[index].ProtocolMsgID]; len(attached) > 0 {
			messages[index].Reactions = attached
		}
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i].Timestamp.Before(messages[j].Timestamp) })
	p.storeMessages(core.StripConvID(roomID), messages)
	return messages, nil
}

func (p *Provider) sendMessage(roomID, text string, file *core.Attachment, threadID, quotedID *string) (*models.Message, error) {
	return p.sendMessageWithMentions(roomID, text, file, threadID, quotedID, nil)
}

func (p *Provider) sendMessageWithMentions(roomID, text string, file *core.Attachment, threadID, quotedID *string, mentions []core.Mention) (*models.Message, error) {
	content := map[string]any{"msgtype": "m.text", "body": text}
	if len(mentions) > 0 {
		userIDs := make([]string, 0, len(mentions))
		for _, mention := range mentions {
			if mention.UserID != "" {
				userIDs = append(userIDs, mention.UserID)
			}
		}
		content["m.mentions"] = map[string]any{"user_ids": userIDs}
	}
	if file != nil {
		mxc, err := p.upload(file)
		if err != nil {
			return nil, err
		}
		msgType := "m.file"
		if strings.HasPrefix(file.MimeType, "image/") {
			msgType = "m.image"
		} else if strings.HasPrefix(file.MimeType, "video/") {
			msgType = "m.video"
		} else if strings.HasPrefix(file.MimeType, "audio/") {
			msgType = "m.audio"
		}
		content = map[string]any{"msgtype": msgType, "body": file.FileName, "filename": file.FileName, "url": mxc, "info": map[string]any{"mimetype": file.MimeType, "size": file.FileSize}}
		if text != "" {
			content["body"] = text
		}
	}
	var relation map[string]any
	if threadID != nil && *threadID != "" {
		relation = map[string]any{"rel_type": "m.thread", "event_id": *threadID, "is_falling_back": true, "m.in_reply_to": map[string]any{"event_id": *threadID}}
	} else if quotedID != nil && *quotedID != "" {
		relation = map[string]any{"m.in_reply_to": map[string]any{"event_id": *quotedID}}
	}
	if relation != nil {
		content["m.relates_to"] = relation
	}
	var response struct {
		EventID string `json:"event_id"`
	}
	rawRoom := core.StripConvID(roomID)
	if err := p.do(noCancel(), http.MethodPut, p.roomPath(rawRoom)+"/send/m.room.message/"+p.txnID(), nil, content, &response); err != nil {
		return nil, err
	}
	p.mu.RLock()
	self := p.userID
	p.mu.RUnlock()
	now := time.Now()
	message := models.Message{ProtocolConvID: p.namespacedRoom(rawRoom), ProtocolMsgID: response.EventID, SenderID: self, SenderName: self, Body: text, Timestamp: now, IsFromMe: true, ThreadID: threadID, QuotedMessageID: quotedID}
	if file != nil {
		attachmentType := "document"
		if strings.HasPrefix(file.MimeType, "image/") {
			attachmentType = "image"
		} else if strings.HasPrefix(file.MimeType, "video/") {
			attachmentType = "video"
		} else if strings.HasPrefix(file.MimeType, "audio/") {
			attachmentType = "audio"
		}
		attachments, _ := json.Marshal([]models.Attachment{{Type: attachmentType, FileName: file.FileName, FileSize: int64(file.FileSize), MimeType: file.MimeType}})
		message.Attachments = string(attachments)
	}
	p.storeMessages(rawRoom, []models.Message{message})
	return &message, nil
}

func (p *Provider) storeMessages(roomID string, messages []models.Message) {
	if db.DB == nil || len(messages) == 0 {
		return
	}
	rawRoom := core.StripConvID(roomID)
	namespacedRoom := p.namespacedRoom(rawRoom)
	var conversation models.Conversation
	db.DB.Where("protocol_conv_id = ?", namespacedRoom).First(&conversation)
	if conversation.ID == 0 {
		if summary, err := p.roomState(rawRoom); err == nil {
			userID := rawRoom
			if summary.IsDirect {
				for _, member := range summary.Members {
					if member != p.CurrentUserID() {
						userID = member
						break
					}
				}
			}
			p.persistRoom(models.LinkedAccount{Protocol: "matrix", ProviderInstanceID: p.getInstanceID(), UserID: userID, Username: summary.Name, AvatarURL: summary.Avatar, IsGroup: !summary.IsDirect}, rawRoom)
			db.DB.Where("protocol_conv_id = ?", namespacedRoom).First(&conversation)
		}
	}
	if conversation.ID == 0 {
		return
	}
	for i := range messages {
		if messages[i].ProtocolMsgID == "" {
			continue
		}
		messages[i].ConversationID = conversation.ID
		messages[i].ProtocolConvID = namespacedRoom
		var existing models.Message
		if db.DB.Where("protocol_msg_id = ?", messages[i].ProtocolMsgID).First(&existing).Error == nil {
			continue
		}
		db.DB.Create(&messages[i])
	}
}

func (p *Provider) SendMessage(c, t string, f *core.Attachment, thread *string) (*models.Message, error) {
	return p.sendMessage(c, t, f, thread, nil)
}
func (p *Provider) SendMessageWithMentions(c, t string, mentions []core.Mention, thread, quoted *string) (*models.Message, error) {
	return p.sendMessageWithMentions(c, t, nil, thread, quoted, mentions)
}
func (p *Provider) SendReply(c, t, q string) (*models.Message, error) {
	return p.sendMessage(c, t, nil, nil, &q)
}
func (p *Provider) SendThreadReply(c, t, thread, q string) (*models.Message, error) {
	return p.sendMessage(c, t, nil, &thread, &q)
}
func (p *Provider) SendFile(c string, f *core.Attachment, thread *string) (*models.Message, error) {
	return p.sendMessage(c, "", f, thread, nil)
}

func (p *Provider) upload(file *core.Attachment) (string, error) {
	p.mu.RLock()
	base, token := p.homeserver, p.accessToken
	p.mu.RUnlock()
	q := url.Values{"filename": {file.FileName}}
	req, err := http.NewRequest(http.MethodPost, base+"/_matrix/media/v3/upload?"+q.Encode(), strings.NewReader(string(file.Data)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", file.MimeType)
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("matrix: media upload HTTP %d", resp.StatusCode)
	}
	var out struct {
		ContentURI string `json:"content_uri"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.ContentURI, nil
}

func (p *Provider) EditMessage(c, id, text string) (*models.Message, error) {
	content := map[string]any{"msgtype": "m.text", "body": "* " + text, "m.new_content": map[string]any{"msgtype": "m.text", "body": text}, "m.relates_to": map[string]any{"rel_type": "m.replace", "event_id": id}}
	var out struct {
		EventID string `json:"event_id"`
	}
	if err := p.do(noCancel(), http.MethodPut, p.roomPath(c)+"/send/m.room.message/"+p.txnID(), nil, content, &out); err != nil {
		return nil, err
	}
	now := time.Now()
	p.mu.RLock()
	self := p.userID
	p.mu.RUnlock()
	return &models.Message{ProtocolConvID: p.namespacedRoom(core.StripConvID(c)), ProtocolMsgID: id, SenderID: self, Body: text, Timestamp: now, IsFromMe: true, IsEdited: true, EditedTimestamp: &now}, nil
}
func (p *Provider) DeleteMessage(c, id string) error {
	return p.do(noCancel(), http.MethodPut, p.roomPath(c)+"/redact/"+url.PathEscape(id)+"/"+p.txnID(), nil, map[string]string{}, nil)
}
func (p *Provider) AddReaction(c, id, emoji string) error {
	var out struct {
		EventID string `json:"event_id"`
	}
	if err := p.do(noCancel(), http.MethodPut, p.roomPath(c)+"/send/m.reaction/"+p.txnID(), nil, map[string]any{"m.relates_to": map[string]string{"rel_type": "m.annotation", "event_id": id, "key": emoji}}, &out); err != nil {
		return err
	}
	p.mu.Lock()
	p.reactionEvents[reactionKey(core.StripConvID(c), id, emoji, p.userID)] = out.EventID
	p.mu.Unlock()
	return nil
}
func (p *Provider) RemoveReaction(c, id, emoji string) error {
	p.mu.RLock()
	reactionID := p.reactionEvents[reactionKey(core.StripConvID(c), id, emoji, p.userID)]
	p.mu.RUnlock()
	if reactionID == "" {
		return fmt.Errorf("matrix: reaction event not found; synchronize the room before removing it")
	}
	return p.DeleteMessage(c, reactionID)
}
func reactionKey(room, id, emoji, user string) string {
	return room + "\x00" + id + "\x00" + emoji + "\x00" + user
}
func (p *Provider) GetThreads(parent string) ([]models.Message, error) {
	p.mu.RLock()
	room := p.eventRooms[parent]
	p.mu.RUnlock()
	if room == "" {
		return nil, fmt.Errorf("matrix: unknown room for thread %s", parent)
	}
	q := url.Values{"dir": {"f"}}
	var out struct {
		Chunk []matrixEvent `json:"chunk"`
	}
	if err := p.do(noCancel(), http.MethodGet, p.roomPath(room)+"/relations/"+url.PathEscape(parent)+"/m.thread", q, nil, &out); err != nil {
		return nil, err
	}
	msgs := []models.Message{}
	for _, e := range out.Chunk {
		if m, ok := p.eventToMessage(room, e); ok {
			msgs = append(msgs, m)
		}
	}
	return msgs, nil
}
