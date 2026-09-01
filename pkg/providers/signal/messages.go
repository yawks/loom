package signal

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/events"
	signalpb "go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf"
	signaltypes "go.mau.fi/mautrix-signal/pkg/signalmeow/types"
	"google.golang.org/protobuf/proto"
)

func (p *Provider) sendData(conversationID string, dm *signalpb.DataMessage) (*models.Message, error) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return nil, fmt.Errorf("signal is not paired")
	}
	rawID := core.StripConvID(conversationID)
	ts := uint64(time.Now().UnixMilli())
	dm.Timestamp = proto.Uint64(ts)
	content := &signalpb.Content{Content: &signalpb.Content_DataMessage{DataMessage: dm}}
	if len(rawID) == 44 {
		result, err := client.SendGroupMessage(context.Background(), signaltypes.GroupIdentifier(rawID), content)
		if err != nil {
			return nil, err
		}
		if len(result.FailedToSendTo) > 0 && len(result.SuccessfullySentTo) == 0 {
			return nil, result.FailedToSendTo[0].Error
		}
	} else {
		recipient, err := libsignalgo.ServiceIDFromString(rawID)
		if err != nil {
			return nil, err
		}
		result := client.SendMessage(context.Background(), recipient, content)
		if result.Error != nil {
			return nil, result.Error
		}
	}
	m := models.Message{ProtocolConvID: core.BuildConvID(p.instanceID(), rawID), ProtocolMsgID: fmt.Sprintf("%s|%d", client.Store.ACI, ts), SenderID: client.Store.ACI.String(), Body: dm.GetBody(), Timestamp: time.UnixMilli(int64(ts)), IsFromMe: true}
	if dm.Quote != nil {
		q := strconv.FormatUint(dm.Quote.GetId(), 10)
		m.QuotedMessageID = &q
	}
	p.remember(m)
	if err := p.persistCanonicalMessages(rawID, rawID, len(rawID) == 44, []models.Message{m}); err != nil {
		return nil, fmt.Errorf("persist sent Signal message: %w", err)
	}
	return &m, nil
}

func (p *Provider) SendMessage(c, text string, file *core.Attachment, thread *string) (*models.Message, error) {
	dm := &signalpb.DataMessage{Body: proto.String(text)}
	if file != nil {
		p.mu.RLock()
		client := p.client
		p.mu.RUnlock()
		ptr, err := client.UploadAttachment(context.Background(), file.Data)
		if err != nil {
			return nil, err
		}
		ptr.ContentType = proto.String(file.MimeType)
		ptr.FileName = proto.String(file.FileName)
		dm.Attachments = []*signalpb.AttachmentPointer{ptr}
	}
	return p.sendData(c, dm)
}
func (p *Provider) SendFile(c string, file *core.Attachment, t *string) (*models.Message, error) {
	return p.SendMessage(c, "", file, t)
}
func (p *Provider) SendReply(c, text, q string) (*models.Message, error) {
	ts, _ := messageTimestamp(q)
	return p.sendData(c, &signalpb.DataMessage{Body: proto.String(text), Quote: &signalpb.DataMessage_Quote{Id: proto.Uint64(ts)}})
}
func (p *Provider) SendThreadReply(c, text, thread, q string) (*models.Message, error) {
	return p.SendReply(c, text, q)
}
func messageTimestamp(id string) (uint64, error) {
	parts := []byte(id)
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == '|' {
			return strconv.ParseUint(string(parts[i+1:]), 10, 64)
		}
	}
	return strconv.ParseUint(id, 10, 64)
}
func (p *Provider) AddReaction(c, id, emoji string) error {
	ts, err := messageTimestamp(id)
	if err != nil {
		return err
	}
	_, err = p.sendData(c, &signalpb.DataMessage{Reaction: &signalpb.DataMessage_Reaction{Emoji: proto.String(emoji), TargetSentTimestamp: proto.Uint64(ts), Remove: proto.Bool(false)}})
	return err
}
func (p *Provider) RemoveReaction(c, id, emoji string) error {
	ts, err := messageTimestamp(id)
	if err != nil {
		return err
	}
	_, err = p.sendData(c, &signalpb.DataMessage{Reaction: &signalpb.DataMessage_Reaction{Emoji: proto.String(emoji), TargetSentTimestamp: proto.Uint64(ts), Remove: proto.Bool(true)}})
	return err
}
func (p *Provider) DeleteMessage(c, id string) error {
	ts, err := messageTimestamp(id)
	if err != nil {
		return err
	}
	_, err = p.sendData(c, &signalpb.DataMessage{Delete: &signalpb.DataMessage_Delete{TargetSentTimestamp: proto.Uint64(ts)}})
	return err
}
func (p *Provider) EditMessage(c, id, text string) (*models.Message, error) {
	ts, err := messageTimestamp(id)
	if err != nil {
		return nil, err
	}
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return nil, fmt.Errorf("signal is not paired")
	}
	recipient, err := libsignalgo.ServiceIDFromString(core.StripConvID(c))
	if err != nil {
		return nil, err
	}
	now := uint64(time.Now().UnixMilli())
	content := signalmeow.WrapEditMessage(&signalpb.EditMessage{TargetSentTimestamp: proto.Uint64(ts), DataMessage: &signalpb.DataMessage{Timestamp: proto.Uint64(now), Body: proto.String(text)}})
	result := client.SendMessage(context.Background(), recipient, content)
	if result.Error != nil {
		return nil, result.Error
	}
	m := &models.Message{ProtocolConvID: core.BuildConvID(p.instanceID(), core.StripConvID(c)), ProtocolMsgID: fmt.Sprintf("%s|%d", client.Store.ACI, now), SenderID: client.Store.ACI.String(), Body: text, Timestamp: time.UnixMilli(int64(now)), IsFromMe: true, IsEdited: true}
	return m, nil
}
func (p *Provider) SendTypingIndicator(c string, on bool) error {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("signal is not paired")
	}
	recipient, err := libsignalgo.ServiceIDFromString(core.StripConvID(c))
	if err != nil {
		return err
	}
	result := client.SendMessage(context.Background(), recipient, signalmeow.TypingMessage(on))
	return result.Error
}

func (p *Provider) handleChatEvent(evt *events.ChatEvent) {
	if typing, ok := evt.Event.(*signalpb.TypingMessage); ok {
		p.emit(core.TypingEvent{InstanceID: p.instanceID(), ConversationID: core.BuildConvID(p.instanceID(), evt.Info.ChatID), UserID: evt.Info.Sender.String(), IsTyping: typing.GetAction() == signalpb.TypingMessage_STARTED})
		return
	}
	dm, ok := evt.Event.(*signalpb.DataMessage)
	if !ok {
		if edit, isEdit := evt.Event.(*signalpb.EditMessage); isEdit {
			dm = edit.GetDataMessage()
		} else {
			return
		}
	}
	conv := core.BuildConvID(p.instanceID(), evt.Info.ChatID)
	ts := dm.GetTimestamp()
	if ts == 0 {
		ts = evt.Info.ServerTimestamp
	}
	if reaction := dm.GetReaction(); reaction != nil {
		p.emit(core.ReactionEvent{InstanceID: p.instanceID(), ConversationID: conv, MessageID: strconv.FormatUint(reaction.GetTargetSentTimestamp(), 10), UserID: evt.Info.Sender.String(), Emoji: reaction.GetEmoji(), Added: !reaction.GetRemove(), Timestamp: int64(ts)})
		return
	}
	if del := dm.GetDelete(); del != nil {
		m := models.Message{ProtocolConvID: conv, ProtocolMsgID: strconv.FormatUint(del.GetTargetSentTimestamp(), 10), SenderID: evt.Info.Sender.String(), Timestamp: time.UnixMilli(int64(ts)), IsDeleted: true, DeletedReason: "remote_delete"}
		p.emit(core.MessageEvent{InstanceID: p.instanceID(), Message: m})
		return
	}
	m := models.Message{ProtocolConvID: conv, ProtocolMsgID: fmt.Sprintf("%s|%d", evt.Info.Sender, ts), SenderID: evt.Info.Sender.String(), Body: dm.GetBody(), Timestamp: time.UnixMilli(int64(ts))}
	p.mu.RLock()
	if p.client != nil {
		m.IsFromMe = evt.Info.Sender == p.client.Store.ACI
	}
	p.mu.RUnlock()
	if q := dm.GetQuote(); q != nil {
		id := strconv.FormatUint(q.GetId(), 10)
		m.QuotedMessageID = &id
		if q.GetText() != "" {
			body := q.GetText()
			m.QuotedBody = &body
		}
	}
	if len(dm.Attachments) > 0 {
		a := make([]map[string]any, 0, len(dm.Attachments))
		for index, ptr := range dm.Attachments {
			mimeType := ptr.GetContentType()
			kind := "file"
			if strings.HasPrefix(mimeType, "image/") {
				kind = "image"
			} else if strings.HasPrefix(mimeType, "video/") {
				kind = "video"
			} else if strings.HasPrefix(mimeType, "audio/") {
				kind = "audio"
			}
			name := filepath.Base(ptr.GetFileName())
			if name == "." || name == "" {
				name = fmt.Sprintf("attachment-%d", index)
			}
			url := ""
			if data, err := signalmeow.DownloadAttachmentWithPointer(context.Background(), ptr, nil, nil); err == nil {
				if base, dirErr := os.UserConfigDir(); dirErr == nil {
					dir := filepath.Join(base, "Loom", p.instanceID(), "attachments")
					if os.MkdirAll(dir, 0700) == nil {
						path := filepath.Join(dir, fmt.Sprintf("%d-%s", ts, name))
						if os.WriteFile(path, data, 0600) == nil {
							url = path
						}
					}
				}
			}
			a = append(a, map[string]any{"type": kind, "mimeType": mimeType, "fileName": name, "fileSize": ptr.GetSize(), "url": url})
		}
		b, _ := json.Marshal(a)
		m.Attachments = string(b)
	}
	p.remember(m)
	// Realtime Signal events must cross the same canonical persistence boundary
	// as transferred history; frontend events alone are intentionally transient.
	_ = p.persistCanonicalMessages(evt.Info.ChatID, evt.Info.ChatID, len(evt.Info.ChatID) == 44, []models.Message{m})
	p.emit(core.MessageEvent{InstanceID: p.instanceID(), Message: m})
}
func (p *Provider) handleReceipt(evt *events.Receipt) {
	typ := core.ReceiptTypeDelivery
	if evt.Content.GetType() == signalpb.ReceiptMessage_READ {
		typ = core.ReceiptTypeRead
	}
	for _, ts := range evt.Content.GetTimestamp() {
		p.emit(core.ReceiptEvent{InstanceID: p.instanceID(), MessageID: strconv.FormatUint(ts, 10), ReceiptType: typ, UserID: evt.Sender.String(), Timestamp: time.Now().Unix()})
	}
}
func (p *Provider) handleReadSelf(evt *events.ReadSelf) {
	for _, read := range evt.Messages {
		p.emit(core.ReceiptEvent{InstanceID: p.instanceID(), MessageID: strconv.FormatUint(read.GetTimestamp(), 10), ReceiptType: core.ReceiptTypeSelfRead, Timestamp: time.Now().Unix()})
	}
}
