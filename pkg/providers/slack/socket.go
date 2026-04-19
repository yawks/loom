package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// startSocketMode starts the Socket Mode event listener loop
func (p *SlackProvider) startSocketMode() {
	p.log("SlackProvider.startSocketMode: starting event loop\n")

	// Create a goroutine to run the socket mode loop
	// We use p.socketClient which was initialized in Connect
	go func() {
		for evt := range p.socketClient.Events {
			switch evt.Type {
			case socketmode.EventTypeConnecting:
				p.log("SlackProvider.startSocketMode: connecting to Slack Socket Mode...\n")

			case socketmode.EventTypeConnected:
				p.log("SlackProvider.startSocketMode: connected to Slack Socket Mode\n")

			case socketmode.EventTypeEventsAPI:
				eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
				if !ok {
					p.log("SlackProvider.startSocketMode: ignored valid event (type mismatch)\n")
					continue
				}

				// Acknowledge the event
				p.socketClient.Ack(*evt.Request)

				switch eventsAPIEvent.Type {
				case slackevents.CallbackEvent:
					innerEvent := eventsAPIEvent.InnerEvent
					switch ev := innerEvent.Data.(type) {
					case *slackevents.MessageEvent:
						p.log("SlackProvider.startSocketMode: received message event\n")
						p.handleMessageEvent(ev)
					default:
						// p.log("SlackProvider.startSocketMode: ignored inner event type: %T\n", ev)
					}
				}

			default:
				// p.log("SlackProvider.startSocketMode: ignored event type: %s\n", evt.Type)
			}
		}
		p.log("SlackProvider.startSocketMode: event loop ended\n")
	}()

	// Run the socket client (blocking) - wait, Run() block, so we should run it in the goroutine above?
	// Actually socketmode.Run() blocks, so we usually run THAT in a goroutine.
	// But the example above iterates over the channel.
	// Looking at slack-go docs, client.Run() starts the loop and populates the Events channel.
	// So we need to call client.Run() separately.

	runErr := p.socketClient.Run()
	if runErr != nil {
		p.log("SlackProvider.startSocketMode: ERROR - socket client stopped with error: %v\n", runErr)
	}
}

// handleMessageEvent processes a new message from Slack
func (p *SlackProvider) handleMessageEvent(ev *slackevents.MessageEvent) {
	// Skip messages from the bot itself to avoid loops (though the UI should handle duplicates ideally)
	// User ID isn't directly on the provider, we might want to store it.
	// For now, let's process everything.

	// Convert to internal model
	p.log("SlackProvider: received message from %s in channel %s: %s\n", ev.User, ev.Channel, ev.Text)

	timestamp := time.Now()
	// event.TimeStamp is string "123456.789", parse if needed
	if floatTS, err := strconv.ParseFloat(ev.TimeStamp, 64); err == nil {
		timestamp = time.Unix(int64(floatTS), int64((floatTS-float64(int64(floatTS)))*1e9))
	}

	// Determine if from me
	// Use RTM client auth info if available or check against stored self ID
	isFromMe := false
	// Quick check using RTM info if available
	p.mu.RLock()
	rtmClient := p.rtmClient
	p.mu.RUnlock()
	if rtmClient != nil {
		info := rtmClient.GetInfo()
		if info != nil && info.User.ID == ev.User {
			isFromMe = true
		}
	}

	// Resolve sender name and avatar
	senderName, senderAvatarURL := p.resolveUserInfo(ev.User)

	// Ensure the conversation/contact exists in DB so it shows up in Recent/All lists
	displayName := p.resolveConversationName(ev.Channel, senderName)
	if err := p.ensureConversationContact(ev.Channel, displayName); err != nil {
		p.log("SlackProvider: failed to ensure conversation contact for %s: %v\n", ev.Channel, err)
	}

	// Check if this is a huddle-related message
	callType := ""
	callJoinURL := ""
	textLower := strings.ToLower(ev.Text)
	if strings.Contains(textLower, "huddle") {
		if strings.Contains(textLower, "started") || strings.Contains(textLower, "joined") {
			// Determine if it's a group or individual call
			isGroup := !strings.HasPrefix(ev.Channel, "D")
			if isGroup {
				callType = "incoming_group_call"
			} else {
				callType = "incoming_call"
			}

			// Construct Join URL
			p.mu.RLock()
			teamID := p.selfTeamID
			p.mu.RUnlock()
			if teamID != "" && ev.Channel != "" {
				// Format as requested: https://app.slack.com/client/TEAM_ID/CHANNEL_ID
				callJoinURL = fmt.Sprintf("https://app.slack.com/client/%s/%s", teamID, ev.Channel)
			}
		} else if strings.Contains(textLower, "ended") || strings.Contains(textLower, "left") {
			// Huddle ended - we'll mark it as missed
			isGroup := !strings.HasPrefix(ev.Channel, "D")
			if isGroup {
				callType = "missed_group_voice"
			} else {
				callType = "missed_voice"
			}
		}
	}

	// Basic message construction
	msg := models.Message{
		ProtocolConvID:  ev.Channel,
		ProtocolMsgID:   ev.TimeStamp, // Slack uses timestamp as ID
		SenderID:        ev.User,
		SenderName:      senderName,
		SenderAvatarURL: senderAvatarURL,
		Body:            p.preprocessMessageBody(ev.Text),
		Timestamp:       timestamp,
		IsFromMe:        isFromMe,
		Attachments:     "[]", // Handle attachments if any
		CallType:        callType,
		CallJoinURL:     callJoinURL,
	}

	// Check if this conversation already has messages in DB (to decide if we need an initial sync)
	var existingCount int64
	if db.DB != nil {
		if err := db.DB.Model(&models.Message{}).
			Where("protocol_conv_id = ?", ev.Channel).
			Count(&existingCount).Error; err != nil {
			p.log("SlackProvider: failed counting existing messages for %s: %v\n", ev.Channel, err)
		}
	}

	// Persist immediately so unread counts & sorting work
	if db.DB != nil {
		p.storeMessagesForConversation(ev.Channel, []models.Message{msg})
	}

	// Create the event
	event := core.MessageEvent{InstanceID: p.getInstanceId(),
		Message: msg,
	}

	// If this is the first time we see this conversation, trigger a history sync in background
	if existingCount == 0 {
		go p.syncConversationHistory(ev.Channel)

		// Emit a lightweight refresh signal so the UI reloads contact lists
		select {
		case p.eventChan <- core.ContactStatusEvent{InstanceID: p.getInstanceId(),
			UserID: "refresh",
			Status: "message_received",
		}:
		default:
			p.log("SlackProvider: WARNING - Failed to emit contact refresh after first message in %s\n", ev.Channel)
		}
	}

	// Emit the message
	p.eventChan <- event

	// If this is a call-related message, also emit a contact refresh to update the conversation list
	if callType != "" {
		select {
		case p.eventChan <- core.ContactStatusEvent{InstanceID: p.getInstanceId(), UserID: "refresh", Status: "call_received"}:
			p.log("SlackProvider: ContactStatusEvent emitted for huddle\n")
		default:
		}
	}
}
