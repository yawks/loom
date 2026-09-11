package slack

import (
	"Loom/pkg/core"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// Socket Mode delivers app subscriptions, which need not match the user's
// memberships. Public-channel history is also readable after leaving. Share a
// paginated user-membership snapshot across ingestion paths, not an API call per
// message. users.conversations implies membership; its objects omit is_member.
type slackMembershipCache struct {
	mu       sync.Mutex
	client   *slack.Client
	channels map[string]bool
	expires  time.Time
	err      error
}

func (p *SlackProvider) canIngestConversation(ctx context.Context, conversationID string) (bool, error) {
	p.mu.RLock()
	mode, _ := p.config.GetString("slack_mode")
	client := p.client
	instanceID, _ := p.config.GetString("_instance_id")
	p.mu.RUnlock()
	if mode != slackModeOfficial {
		return true, nil
	}
	if instanceID == "" {
		return false, fmt.Errorf("Slack membership check requires a provider instance ID")
	}
	if strings.Contains(conversationID, "::") && !strings.HasPrefix(conversationID, instanceID+"::") {
		return false, fmt.Errorf("conversation belongs to another provider")
	}
	rawID := core.StripConvID(conversationID)
	if rawID == "" {
		return false, fmt.Errorf("conversation ID is required")
	}
	// Direct messages use D IDs on the wire and U/W IDs in Loom. They have
	// no channel membership to leave and must keep working independently.
	if strings.HasPrefix(rawID, "D") || strings.HasPrefix(rawID, "U") || strings.HasPrefix(rawID, "W") {
		return true, nil
	}
	if client == nil {
		return false, fmt.Errorf("slack client not initialized")
	}
	c := &p.membership
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == client && time.Now().Before(c.expires) {
		return c.err == nil && c.channels[rawID], c.err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	channels := make(map[string]bool)
	params := &slack.GetConversationsForUserParameters{
		Types:           []string{"public_channel", "private_channel", "mpim"},
		ExcludeArchived: true, Limit: 200,
	}
	seen := make(map[string]bool)
	for {
		page, cursor, err := client.GetConversationsForUserContext(ctx, params)
		if err == nil && cursor != "" && seen[cursor] {
			err = fmt.Errorf("repeated Slack membership cursor")
		}
		if err != nil {
			// Never accept a partial list or stale positive membership on error.
			// Briefly cache failures to avoid hammering Slack for every message.
			c.client, c.channels, c.err = client, nil, err
			c.expires = time.Now().Add(10 * time.Second)
			p.log("SlackProvider: unable to refresh memberships; channel ingestion paused: %v\n", err)
			return false, err
		}
		for _, channel := range page {
			if !channel.IsArchived {
				channels[channel.ID] = true
			}
		}
		if cursor == "" {
			break
		}
		seen[cursor] = true
		params.Cursor = cursor
	}
	c.client, c.channels, c.err = client, channels, nil
	c.expires = time.Now().Add(time.Minute)
	return channels[rawID], nil
}

func (p *SlackProvider) invalidateMemberships() {
	p.membership.mu.Lock()
	p.membership.expires = time.Time{}
	p.membership.mu.Unlock()
}

func (p *SlackProvider) handleMembershipChange(userID string) {
	p.mu.RLock()
	selfID := p.selfUserID
	p.mu.RUnlock()
	if selfID == "" || userID == selfID {
		p.invalidateMemberships()
	}
}
