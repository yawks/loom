package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// slackHuddleRoom backports the stable subset of slack.HuddleRoom introduced
// after slack-go v0.17. Slack updates huddle_thread messages throughout a call;
// HasEnded/DateEnd are the reliable end signal (a message_changed alone is not).
type slackHuddleRoom struct {
	DateStart          int64    `json:"date_start"`
	DateEnd            int64    `json:"date_end"`
	Participants       []string `json:"participants,omitempty"`
	ParticipantHistory []string `json:"participant_history,omitempty"`
	IsDMCall           bool     `json:"is_dm_call"`
	HasEnded           bool     `json:"has_ended"`
	HuddleLink         string   `json:"huddle_link,omitempty"`
}

type slackHuddleHistoryResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
	Messages []struct {
		Timestamp string           `json:"ts"`
		SubType   string           `json:"subtype"`
		Room      *slackHuddleRoom `json:"room,omitempty"`
	} `json:"messages"`
}

func (p *SlackProvider) fetchHuddleRoom(ctx context.Context, channelID, messageTS string) (*slackHuddleRoom, error) {
	if channelID == "" || messageTS == "" {
		return nil, fmt.Errorf("Slack huddle channel and timestamp are required")
	}
	p.apiClientMu.RLock()
	client := p.apiHTTPClient
	token := p.authToken
	baseURL := p.apiBaseURL
	p.apiClientMu.RUnlock()
	if client == nil || token == "" {
		return nil, fmt.Errorf("Slack HTTP client is not initialized")
	}

	query := url.Values{
		"channel":   {channelID},
		"latest":    {messageTS},
		"inclusive": {"true"},
		"limit":     {"1"},
	}
	if baseURL == "" {
		baseURL = "https://slack.com/api"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/conversations.history?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Slack conversations.history returned HTTP %d", resp.StatusCode)
	}
	var payload slackHuddleHistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if !payload.OK {
		return nil, fmt.Errorf("Slack conversations.history: %s", payload.Error)
	}
	for _, message := range payload.Messages {
		if message.Timestamp == messageTS && isSlackHuddleSubtype(message.SubType) && message.Room != nil {
			return message.Room, nil
		}
	}
	return nil, fmt.Errorf("Slack huddle metadata not found")
}

func applySlackHuddleRoom(message *models.Message, room *slackHuddleRoom) {
	if message == nil || room == nil {
		return
	}
	participants := room.ParticipantHistory
	if len(participants) == 0 {
		participants = room.Participants
	}
	if len(participants) > 0 {
		if raw, err := json.Marshal(participants); err == nil {
			message.CallParticipants = string(raw)
		}
	}
	if room.HuddleLink != "" && !room.HasEnded && room.DateEnd == 0 {
		message.CallUrl = room.HuddleLink
		message.CallLinkAction = "join"
	}
	if room.HasEnded || room.DateEnd > 0 {
		message.CallType = "call_ended"
		message.CallOutcome = "CONNECTED"
		message.CallUrl = ""
		message.CallLinkAction = ""
		if room.DateStart > 0 && room.DateEnd > room.DateStart {
			duration := room.DateEnd - room.DateStart
			if duration > int64(^uint32(0)>>1) {
				duration = int64(^uint32(0) >> 1)
			}
			seconds := int32(duration)
			message.CallDurationSecs = &seconds
		}
		return
	}
	if room.IsDMCall {
		message.CallType = "incoming_call"
	} else {
		message.CallType = "incoming_group_call"
	}
}

// handleHuddleRoomUpdate applies a live huddle_thread update to the original
// stored call message. It returns true only when the target is a known huddle.
func (p *SlackProvider) handleHuddleRoomUpdate(channelID, messageTS string, room *slackHuddleRoom) bool {
	if db.DB == nil || room == nil {
		return false
	}
	normalizedConvID := core.BuildConvID(p.getInstanceId(), p.normalizeDMConversationID(channelID))
	var message models.Message
	if err := db.DB.Where("protocol_conv_id = ? AND protocol_msg_id = ? AND call_type <> ''", normalizedConvID, messageTS).
		First(&message).Error; err != nil {
		return false
	}
	applySlackHuddleRoom(&message, room)
	if err := db.DB.Omit("Reactions").Save(&message).Error; err != nil {
		p.log("SlackProvider: failed to update ended huddle %s: %v\n", messageTS, err)
		return true
	}
	p.log("SlackProvider: updated huddle %s (type=%s, duration=%v)\n", messageTS, message.CallType, message.CallDurationSecs)
	select {
	case p.eventChan <- core.MessageEvent{InstanceID: p.getInstanceId(), Message: message}:
	default:
		p.log("SlackProvider: event channel full while updating huddle %s\n", messageTS)
	}
	if strings.EqualFold(message.CallType, "call_ended") {
		select {
		case p.eventChan <- core.ContactStatusEvent{InstanceID: p.getInstanceId(), UserID: "refresh", Status: "call_ended"}:
		default:
		}
	}
	return true
}

func (p *SlackProvider) pollActiveHuddles(ctx context.Context) {
	if db.DB == nil {
		return
	}
	instancePrefix := p.getInstanceId() + "::%"
	var active []models.Message
	if err := db.DB.Where(
		"protocol_conv_id LIKE ? AND call_type IN ? AND timestamp >= ?",
		instancePrefix,
		[]string{"incoming_call", "incoming_group_call"},
		time.Now().Add(-24*time.Hour),
	).Find(&active).Error; err != nil {
		p.log("SlackProvider.pollActiveHuddles: failed loading active huddles: %v\n", err)
		return
	}
	for _, message := range active {
		if ctx.Err() != nil {
			return
		}
		channelID, err := p.slackPinChannel(message.ProtocolConvID)
		if err != nil {
			p.log("SlackProvider.pollActiveHuddles: failed resolving channel for %s: %v\n", message.ProtocolMsgID, err)
			continue
		}
		fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		room, err := p.fetchHuddleRoom(fetchCtx, channelID, message.ProtocolMsgID)
		cancel()
		if err != nil {
			p.log("SlackProvider.pollActiveHuddles: failed fetching %s: %v\n", message.ProtocolMsgID, err)
			continue
		}
		if room.HasEnded || room.DateEnd > 0 {
			p.handleHuddleRoomUpdate(channelID, message.ProtocolMsgID, room)
		}
	}
}

// pollLatestHuddles discovers newly started huddles without search.messages.
// Slack includes the latest message in conversations.list results; while a
// huddle is active its huddle_thread message is normally the conversation's
// latest item. This costs one paginated conversations.list pass rather than a
// history request for every conversation.
func (p *SlackProvider) pollLatestHuddles(ctx context.Context) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil || db.DB == nil {
		return
	}

	cursor := ""
	for {
		if ctx.Err() != nil {
			return
		}
		channels, nextCursor, err := client.GetConversations(&slack.GetConversationsParameters{
			Types:           []string{"public_channel", "private_channel", "mpim", "im"},
			Limit:           1000,
			Cursor:          cursor,
			ExcludeArchived: true,
		})
		if err != nil {
			p.log("SlackProvider.pollLatestHuddles: failed listing conversations: %v\n", err)
			return
		}

		for _, channel := range channels {
			latest := channel.Latest
			if latest == nil || latest.Timestamp == "" || !isSlackHuddleSubtype(latest.SubType) {
				continue
			}
			normalizedConvID := core.BuildConvID(p.getInstanceId(), p.normalizeDMConversationID(channel.ID))
			var count int64
			if err := db.DB.Model(&models.Message{}).
				Where("protocol_conv_id = ? AND protocol_msg_id = ?", normalizedConvID, latest.Timestamp).
				Count(&count).Error; err != nil || count > 0 {
				continue
			}

			timestamp, err := strconv.ParseFloat(latest.Timestamp, 64)
			if err != nil {
				p.log("SlackProvider.pollLatestHuddles: invalid timestamp %q: %v\n", latest.Timestamp, err)
				continue
			}
			seconds := int64(timestamp)
			startedAt := time.Unix(seconds, int64((timestamp-float64(seconds))*1e9))
			// Leave a full second of margin: Slack timestamps have microsecond
			// precision and converting through float64 can round a few nanoseconds.
			since := startedAt.Add(-time.Second)
			messages, err := p.GetConversationHistory(channel.ID, 10, nil, &since)
			if err != nil {
				p.log("SlackProvider.pollLatestHuddles: failed fetching huddle %s in %s: %v\n", latest.Timestamp, channel.ID, err)
				continue
			}
			for _, message := range messages {
				if message.ProtocolMsgID != latest.Timestamp || message.CallType == "" {
					continue
				}
				select {
				case p.eventChan <- core.MessageEvent{InstanceID: p.getInstanceId(), Message: message}:
					p.log("SlackProvider.pollLatestHuddles: discovered huddle %s in %s\n", latest.Timestamp, channel.ID)
				default:
					p.log("SlackProvider.pollLatestHuddles: event channel full for huddle %s\n", latest.Timestamp)
				}
			}
		}

		if nextCursor == "" {
			return
		}
		cursor = nextCursor
	}
}

func huddleRoomContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}
