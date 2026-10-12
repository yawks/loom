package slack

import (
	"context"
	"encoding/json"
	"time"

	"github.com/slack-go/slack"
)

// slack-go omits attachment.files. Promote them before decoding so forwarded
// documents use the same canonical attachment conversion as ordinary files.
func normalizeForwardedSlackFiles(object map[string]json.RawMessage) bool {
	changed := false
	var attachments []struct {
		Files []json.RawMessage `json:"files"`
	}
	if json.Unmarshal(object["attachments"], &attachments) == nil {
		var files []json.RawMessage
		if len(object["files"]) == 0 || json.Unmarshal(object["files"], &files) == nil {
			for _, attachment := range attachments {
				files = append(files, attachment.Files...)
				changed = changed || len(attachment.Files) > 0
			}
			if changed {
				object["files"], _ = json.Marshal(files)
			}
		}
	}
	for _, key := range []string{"messages", "items"} {
		var children []map[string]json.RawMessage
		if json.Unmarshal(object[key], &children) != nil {
			continue
		}
		childChanged := false
		for _, child := range children {
			childChanged = normalizeForwardedSlackFiles(child) || childChanged
		}
		if childChanged {
			object[key], _ = json.Marshal(children)
			changed = true
		}
	}
	for _, key := range []string{"message", "event"} {
		var child map[string]json.RawMessage
		if json.Unmarshal(object[key], &child) == nil && normalizeForwardedSlackFiles(child) {
			object[key], _ = json.Marshal(child)
			changed = true
		}
	}
	return changed
}

// RTM has already discarded attachment.files; retrieve only the forwarded
// message, with a deadline, rather than fetching each file separately.
func (p *SlackProvider) hydrateForwardedFiles(msg *slack.Msg, channelID string) {
	forwarded := false
	for _, attachment := range msg.Attachments {
		forwarded = forwarded || attachment.Footer == "Slack Conversation" && attachment.FromURL != ""
	}
	if !forwarded {
		return
	}
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	history, err := client.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
		ChannelID: channelID, Oldest: msg.Timestamp, Latest: msg.Timestamp, Inclusive: true, Limit: 1,
	})
	if err != nil {
		p.log("SlackProvider: failed to retrieve forwarded files for %s: %v\n", msg.Timestamp, err)
		return
	}
	for _, message := range history.Messages {
		if message.Timestamp == msg.Timestamp {
			msg.Files = message.Files
		}
	}
}
