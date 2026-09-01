package googlechat

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
)

// pollLoop polls all spaces for new messages every 10 seconds.
func (p *GoogleChatProvider) pollLoop(ctx context.Context) {
	// Give the initial contact sync a moment to populate lastSeen.
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			p.pollAllSpaces(ctx)
			timer.Reset(10 * time.Second)
		}
	}
}

func (p *GoogleChatProvider) pollAllSpaces(ctx context.Context) {
	if p.getHTTPClient() == nil {
		return
	}

	// List all spaces.
	var spaces []Space
	pageToken := ""
	for {
		params := url.Values{"pageSize": {"100"}}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}
		var resp SpaceListResponse
		if err := p.apiGet("/spaces", params, &resp); err != nil {
			p.log("GoogleChatProvider.pollAllSpaces: list error: %v\n", err)
			return
		}
		spaces = append(spaces, resp.Spaces...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	instanceID := p.getInstanceID()
	selfID := p.getSelfID()

	for _, space := range spaces {
		select {
		case <-ctx.Done():
			return
		default:
		}

		p.lastSeenMu.Lock()
		since, ok := p.lastSeen[space.Name]
		if !ok {
			// First poll: only fetch very recent messages (last 5 min) to avoid backfill spam.
			since = time.Now().Add(-5 * time.Minute)
		}
		p.lastSeenMu.Unlock()

		// Include the boundary message again after the first poll. Google Chat
		// changes reaction resources without changing the message createTime, so a
		// strictly forward-only query can never observe a reaction on the latest
		// message while Loom remains connected.
		querySince := since
		if ok {
			querySince = since.Add(-time.Nanosecond)
		}
		filter := `createTime > "` + querySince.UTC().Format(time.RFC3339Nano) + `"`
		params := url.Values{
			"pageSize": {"50"},
			"filter":   {filter},
			"orderBy":  {"createTime asc"},
		}
		var resp MessageListResponse
		if err := p.apiGet("/"+space.Name+"/messages", params, &resp); err != nil {
			continue
		}

		// Collect non-deleted messages and build thread root map for this batch.
		rawMsgs := make([]ChatMessage, 0, len(resp.Messages))
		for _, msg := range resp.Messages {
			if msg.DeleteTime == nil {
				rawMsgs = append(rawMsgs, msg)
			}
		}
		batchRoots := buildThreadRoots(rawMsgs)

		var latest time.Time
		newMessages := make([]models.Message, 0, len(rawMsgs))
		reactionSnapshots := make(map[string][]models.Reaction)
		for _, msg := range rawMsgs {
			m := p.convertMessage(msg, space.Name, selfID)
			m.ThreadID = resolveThreadID(msg, batchRoots)
			// Message resources only contain reaction summaries. Fetch the actual
			// reaction authors for both new messages and the re-read boundary message.
			// An empty summary is authoritative and also detects removals.
			if len(msg.EmojiReactionSummaries) == 0 {
				reactionSnapshots[msg.Name] = nil
			} else if reactions, err := p.listMessageReactions(msg.Name); err != nil {
				p.log("GoogleChatProvider.pollAllSpaces: list reactions for %s: %v\n", msg.Name, err)
			} else {
				m.Reactions = reactions
				reactionSnapshots[msg.Name] = reactions
			}

			isBoundaryRefresh := ok && !msg.CreateTime.After(since)
			if isBoundaryRefresh {
				if reactions, authoritative := reactionSnapshots[msg.Name]; authoritative {
					p.emitReactionDiff(space.Name, msg.Name, reactions)
				}
				continue
			}
			// If the parent is not in this batch, fetch it via API.
			if msg.ThreadReply && m.ThreadID == nil && msg.Thread != nil && msg.Thread.Name != "" {
				if parentID := p.resolveThreadParentFromAPI(space.Name, msg.Thread.Name); parentID != "" {
					m.ThreadID = &parentID
				} else {
					// API call failed; use the thread name as a temporary marker so the frontend
					// classifies this as a thread reply and does not clear the unread badge.
					// GetConversationHistory will patch this to the real parent ID once the parent
					// message is available in the batch (see the threadRoots DB update there).
					threadName := msg.Thread.Name
					m.ThreadID = &threadName
				}
			}
			p.emit(core.MessageEvent{
				InstanceID: instanceID,
				Message:    m,
			})
			newMessages = append(newMessages, m)
			if msg.CreateTime.After(latest) {
				latest = msg.CreateTime
			}
		}
		p.storeMessagesForConversation(space.Name, newMessages, reactionSnapshots)

		if !latest.IsZero() {
			p.lastSeenMu.Lock()
			p.lastSeen[space.Name] = latest
			p.lastSeenMu.Unlock()
		} else if !ok {
			// Even with no messages, record that we checked.
			p.lastSeenMu.Lock()
			p.lastSeen[space.Name] = time.Now()
			p.lastSeenMu.Unlock()
		}
	}
}

// emitReactionDiff turns a polled reaction snapshot into canonical live events.
// Persistence is still handled centrally by the app event loop.
func (p *GoogleChatProvider) emitReactionDiff(convID, messageID string, current []models.Reaction) {
	if db.DB == nil {
		return
	}
	var message models.Message
	namespacedConvID := core.BuildConvID(p.getInstanceID(), core.StripConvID(convID))
	if err := db.DB.Where("protocol_msg_id = ? AND protocol_conv_id = ?", messageID, namespacedConvID).First(&message).Error; err != nil {
		return
	}
	var previous []models.Reaction
	if err := db.DB.Where("message_id = ?", message.ID).Find(&previous).Error; err != nil {
		return
	}
	key := func(reaction models.Reaction) string { return reaction.UserID + "\x00" + reaction.Emoji }
	previousByKey := make(map[string]models.Reaction, len(previous))
	currentByKey := make(map[string]models.Reaction, len(current))
	for _, reaction := range previous {
		previousByKey[key(reaction)] = reaction
	}
	for _, reaction := range current {
		currentByKey[key(reaction)] = reaction
		if _, exists := previousByKey[key(reaction)]; !exists {
			timestamp := reaction.CreatedAt.Unix()
			if timestamp <= 0 {
				timestamp = time.Now().Unix()
			}
			p.emit(core.ReactionEvent{InstanceID: p.getInstanceID(), ConversationID: namespacedConvID, MessageID: messageID, UserID: reaction.UserID, Emoji: reaction.Emoji, Added: true, Timestamp: timestamp})
		}
	}
	for _, reaction := range previous {
		if _, exists := currentByKey[key(reaction)]; !exists {
			p.emit(core.ReactionEvent{InstanceID: p.getInstanceID(), ConversationID: namespacedConvID, MessageID: messageID, UserID: reaction.UserID, Emoji: reaction.Emoji, Added: false, Timestamp: time.Now().Unix()})
		}
	}
}

// GetParticipantNames resolves a list of user IDs to display names.
func (p *GoogleChatProvider) GetParticipantNames(ids []string) (map[string]string, error) {
	result := make(map[string]string, len(ids))
	p.userMu.RLock()
	for _, id := range ids {
		if u, ok := p.userCache[id]; ok {
			result[id] = u.Name
		}
	}
	p.userMu.RUnlock()
	return result, nil
}

// SendTypingIndicator is not supported by the Google Chat REST API.
func (p *GoogleChatProvider) SendTypingIndicator(convID string, isTyping bool) error {
	return nil
}

// SendStatusMessage is not supported.
func (p *GoogleChatProvider) SendStatusMessage(text string, file *core.Attachment) (*models.Message, error) {
	return nil, fmt.Errorf("googlechat: SendStatusMessage not supported")
}

// SendRetryReceipt is not supported.
func (p *GoogleChatProvider) SendRetryReceipt(convID, messageID string) error {
	return nil
}

func spaceName(msgOrConvID string) string {
	if idx := strings.Index(msgOrConvID, "/messages/"); idx >= 0 {
		return msgOrConvID[:idx]
	}
	return msgOrConvID
}
