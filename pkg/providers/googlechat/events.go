package googlechat

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"Loom/pkg/core"
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

		filter := `createTime > "` + since.UTC().Format(time.RFC3339) + `"`
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
		for _, msg := range rawMsgs {
			m := p.convertMessage(msg, space.Name, selfID)
			m.ThreadID = resolveThreadID(msg, batchRoots)
			// If the parent is not in this batch, fetch it via API.
			if msg.ThreadReply && m.ThreadID == nil && msg.Thread != nil && msg.Thread.Name != "" {
				if parentID := p.resolveThreadParentFromAPI(space.Name, msg.Thread.Name); parentID != "" {
					m.ThreadID = &parentID
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
		p.storeMessagesForConversation(space.Name, newMessages)

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
