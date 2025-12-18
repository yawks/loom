package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	"strconv"
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
	// We need self ID. For now assume false or check against cache if possible.
	// In the future we should store self userID in struct.
	isFromMe := false // TODO: Check specific user ID

	// Resolve sender name
	senderName := ev.User
	p.userCacheMu.RLock()
	if user, ok := p.userCache[ev.User]; ok {
		senderName = user.RealName
		if senderName == "" {
			senderName = user.Name
		}
	} else {
		// Try to fetch if not in cache (optional, might be slow in event loop)
		// For now we skip API call to avoid blocking, relying on eventual consistency or cached contacts
	}
	p.userCacheMu.RUnlock()

	// Basic message construction
	msg := models.Message{
		ProtocolConvID: ev.Channel,
		ProtocolMsgID:  ev.TimeStamp, // Slack uses timestamp as ID
		SenderID:       ev.User,
		SenderName:     senderName,
		Body:           ev.Text,
		Timestamp:      timestamp,
		IsFromMe:       isFromMe,
		Attachments:    "[]", // Handle attachments if any
	}

	// Create the event
	event := core.MessageEvent{
		Message: msg,
	}

	// Emit the message
	p.eventChan <- event
}
