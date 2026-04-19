// Package slack implements Slack provider RTM handling.
package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// startRTM starts the Real Time Messaging (RTM) event loop.
// This is used for "User" tokens (xoxc/xoxp) which don't support Socket Mode.
func (p *SlackProvider) startRTM() {
	p.log("SlackProvider.startRTM: starting RTM event loop\n")

	// ManageConnection is blocking, so we run it in a separate goroutine if needed,
	// but here we are already inside a goroutine from Connect() usually.
	// However, we need to process events from the IncomingEvents channel.

	// Start RTM connection routines
	go p.rtmClient.ManageConnection()

	for {
		select {
		case <-p.stopChan:
			p.log("SlackProvider.startRTM: stopping RTM event loop\n")
			return
		case msg := <-p.rtmClient.IncomingEvents:
			switch ev := msg.Data.(type) {
			case *slack.ConnectedEvent:
				p.log("SlackProvider.startRTM: RTM Connected: %s (Connections: %d)\n", ev.Info.User.ID, ev.ConnectionCount)

			case *slack.HelloEvent:
				p.log("SlackProvider.startRTM: RTM Session Established (Hello received)\n")

			case *slack.MessageEvent:
				p.handleRTMMessageEvent(ev)

			case *slack.UserTypingEvent:
				p.handleRTMTypingEvent(ev)

			case *slack.ReactionAddedEvent:
				p.handleRTMReactionAddedEvent(ev)

			case *slack.ReactionRemovedEvent:
				p.handleRTMReactionRemovedEvent(ev)

			case *slack.FileChangeEvent, *slack.FileCreatedEvent, *slack.FileDeletedEvent,
				*slack.FilePublicEvent, *slack.FileSharedEvent, *slack.LatencyReport:
				// Known events we can safely ignore without spamming logs
				continue

			case *slack.RTMError:
				p.log("SlackProvider.startRTM: RTM Error: %s\n", ev.Error())

			case *slack.InvalidAuthEvent:
				p.log("SlackProvider.startRTM: Invalid Credentials\n")
				return

			case *slack.UnmarshallingErrorEvent:
				p.log("SlackProvider.startRTM: Unmarshalling Error: %v\n", ev.Error())

			case *slack.DisconnectedEvent:
				p.log("SlackProvider.startRTM: Disconnected, Intentional: %v, Cause: %v\n", ev.Intentional, ev.Cause)

			}
		}
	}
}

// handleRTMMessageEvent handles incoming RTM message events.
func (p *SlackProvider) handleRTMMessageEvent(ev *slack.MessageEvent) {
	// Skip messages with no text/user (e.g. subtype messages we don't care about)
	if ev.User == "" && ev.Text == "" {
		return
	}

	if ev.Channel == "" {
		p.log("SlackProvider: RTM message missing channel, skipping\n")
		return
	}

	p.log("SlackProvider: received RTM message from %s in channel %s: %s\n", ev.User, ev.Channel, ev.Text)

	// Check if this conversation already has messages in DB (to decide if we need an initial sync)
	var existingCount int64
	if db.DB != nil {
		if err := db.DB.Model(&models.Message{}).
			Where("protocol_conv_id = ?", ev.Channel).
			Count(&existingCount).Error; err != nil {
			p.log("SlackProvider: failed counting existing messages for %s: %v\n", ev.Channel, err)
		}
	}

	timestamp := time.Now()
	// event.Timestamp is string "123456.789", parse if needed
	if floatTS, err := strconv.ParseFloat(ev.Timestamp, 64); err == nil {
		timestamp = time.Unix(int64(floatTS), int64((floatTS-float64(int64(floatTS)))*1e9))
	}

	// Resolve sender name and avatar
	senderName, senderAvatarURL := p.resolveUserInfo(ev.User)

	// Ensure the conversation/contact exists in DB so it shows up in Recent/All lists
	displayName := p.resolveConversationName(ev.Channel, senderName)
	if err := p.ensureConversationContact(ev.Channel, displayName); err != nil {
		p.log("SlackProvider: failed to ensure conversation contact for %s: %v\n", ev.Channel, err)
	}

	// Determine if from me
	// Use RTM client auth info if available or check against stored self ID
	isFromMe := false
	// Quick check using RTM info
	info := p.rtmClient.GetInfo()
	if info != nil && info.User.ID == ev.User {
		isFromMe = true
	}

	// Check if this is a huddle-related message
	callType := ""
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
		ProtocolMsgID:   ev.Timestamp, // Slack uses timestamp as ID
		SenderID:        ev.User,
		SenderName:      senderName,
		SenderAvatarURL: senderAvatarURL,
		Body:            p.preprocessMessageBody(ev.Text),
		Timestamp:       timestamp,
		IsFromMe:        isFromMe,
		Attachments:     "[]", // Handle attachments if any
		CallType:        callType,
	}

	// Create the event
	event := core.MessageEvent{InstanceID: p.getInstanceId(),
		Message: msg,
	}

	// Persist immediately so unread counts & sorting work
	if db.DB != nil {
		p.storeMessagesForConversation(ev.Channel, []models.Message{msg})
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

// handleRTMTypingEvent converts Slack typing events to generic TypingEvent
func (p *SlackProvider) handleRTMTypingEvent(ev *slack.UserTypingEvent) {
	if ev == nil || ev.User == "" || ev.Channel == "" {
		return
	}

	userName := p.resolveUserName(ev.User)

	select {
	case p.eventChan <- core.TypingEvent{InstanceID: p.getInstanceId(),
		ConversationID: ev.Channel,
		UserID:         ev.User,
		UserName:       userName,
		IsTyping:       true,
	}:
	default:
		p.log("SlackProvider: WARNING - Failed to emit TypingEvent for %s in %s\n", ev.User, ev.Channel)
	}
}

// resolveUserInfo returns a best-effort display name and avatar URL for a user ID
func (p *SlackProvider) resolveUserInfo(userID string) (string, string) {
	if userID == "" {
		return "", ""
	}

	// First, try database cache
	cachedName, cachedAvatar := p.getUserNameFromCache(userID)
	if cachedName != "" && cachedName != userID {
		return cachedName, cachedAvatar
	}

	// Check memory cache
	p.userCacheMu.RLock()
	if user, ok := p.userCache[userID]; ok && user != nil {
		name := user.RealName
		if name == "" && user.Profile.DisplayName != "" {
			name = user.Profile.DisplayName
		}
		if name == "" {
			name = user.Name
		}

		avatarURL := ""
		if user.Profile.Image512 != "" {
			avatarURL = user.Profile.Image512
		} else if user.Profile.Image192 != "" {
			avatarURL = user.Profile.Image192
		} else if user.Profile.Image72 != "" {
			avatarURL = user.Profile.Image72
		} else if user.Profile.Image48 != "" {
			avatarURL = user.Profile.Image48
		}

		p.userCacheMu.RUnlock()
		if name != "" && name != userID {
			// Persist to database cache
			p.saveUserNameToCache(userID, name, avatarURL)
			return name, avatarURL
		}
	} else {
		p.userCacheMu.RUnlock()
	}

	// Fallback: fetch from Slack API and update cache
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client != nil {
		if user, err := client.GetUserInfo(userID); err == nil && user != nil {
			name := user.RealName
			if name == "" && user.Profile.DisplayName != "" {
				name = user.Profile.DisplayName
			}
			if name == "" {
				name = user.Name
			}

			avatarURL := ""
			if user.Profile.Image512 != "" {
				avatarURL = user.Profile.Image512
			} else if user.Profile.Image192 != "" {
				avatarURL = user.Profile.Image192
			} else if user.Profile.Image72 != "" {
				avatarURL = user.Profile.Image72
			} else if user.Profile.Image48 != "" {
				avatarURL = user.Profile.Image48
			}

			// Update memory cache
			p.userCacheMu.Lock()
			p.userCache[userID] = user
			p.userCacheMu.Unlock()

			// Persist to database cache
			if name != "" && name != userID {
				p.saveUserNameToCache(userID, name, avatarURL)
				return name, avatarURL
			}
		}
	}

	return "", ""
}

// resolveUserName returns a best-effort display name for a user ID
func (p *SlackProvider) resolveUserName(userID string) string {
	name, _ := p.resolveUserInfo(userID)
	return name
}

// resolveConversationName tries to find a readable name for a channel/DM
func (p *SlackProvider) resolveConversationName(conversationID string, fallback string) string {
	if conversationID == "" {
		return fallback
	}

	// DMs: use sender name
	if strings.HasPrefix(conversationID, "D") {
		if fallback != "" && !strings.HasPrefix(fallback, "U") {
			return fallback
		}

		// Try to resolve user ID from DM channel ID
		actualUserID := p.normalizeDMConversationID(conversationID)
		if actualUserID != conversationID {
			name, _ := p.resolveUserInfo(actualUserID)
			if name != "" && name != actualUserID {
				return name
			}
		}

		if fallback != "" {
			return fallback
		}
		return conversationID
	}

	// Channels/MPIM/Groups: fetch conversation info
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()

	if client != nil {
		if info, err := client.GetConversationInfo(&slack.GetConversationInfoInput{
			ChannelID: conversationID,
		}); err == nil && info != nil && info.Name != "" {
			if strings.HasPrefix(conversationID, "C") {
				return "#" + info.Name
			}
			return info.Name
		}
	}

	if fallback != "" {
		return fallback
	}
	return conversationID
}

// ensureConversationContact makes sure a LinkedAccount/MetaContact exists for this Slack conversation
func (p *SlackProvider) ensureConversationContact(conversationID, displayName string) error {
	if db.DB == nil || conversationID == "" {
		return nil
	}

	// Get instance ID from config
	p.mu.RLock()
	instanceID := ""
	if p.config != nil {
		if id, ok := p.config["_instance_id"].(string); ok {
			instanceID = id
		}
	}
	p.mu.RUnlock()

	if instanceID == "" {
		return fmt.Errorf("instance ID missing, cannot ensure contact")
	}

	// IMPORTANT: For DM channels (start with "D" but not MPIMs), we need to resolve to UserID
	// Slack DM channels have IDs like "DEHFWSQF6", but LinkedAccount should use UserID like "UDY7N2D2L"
	actualUserID := conversationID
	if len(conversationID) > 0 && conversationID[0] == 'D' {
		// This might be a DM channel. Try to get the UserID from the channel info
		p.mu.RLock()
		client := p.client
		p.mu.RUnlock()

		if client != nil {
			// Get conversation info to find the user
			convInfo, err := client.GetConversationInfo(&slack.GetConversationInfoInput{
				ChannelID: conversationID,
			})
			if err == nil && convInfo != nil && convInfo.IsIM && convInfo.User != "" {
				// This is a DM, use the UserID instead of channel ID
				actualUserID = convInfo.User
				p.log("SlackProvider.ensureConversationContact: Resolved DM channel %s to user %s\n", conversationID, actualUserID)
			}
		}
	}

	// Check if linked account already exists (using actualUserID, not conversationID)
	var count int64
	if err := db.DB.Model(&models.LinkedAccount{}).
		Where("provider_instance_id = ? AND user_id = ?", instanceID, actualUserID).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check linked account: %w", err)
	}

	if count > 0 {
		// Optionally refresh display name
		return p.updateLinkedAccountName(instanceID, actualUserID, displayName)
	}

	// Create MetaContact + LinkedAccount via existing helper
	return p.updateLinkedAccountName(instanceID, actualUserID, displayName)
}

// handleRTMReactionAddedEvent handles a reaction being added to a message
func (p *SlackProvider) handleRTMReactionAddedEvent(ev *slack.ReactionAddedEvent) {
	if ev == nil || ev.Item.Channel == "" || ev.Item.Timestamp == "" {
		return
	}

	p.log("SlackProvider.handleRTMReactionAddedEvent: User %s added :%s: to message %s in %s\n",
		ev.User, ev.Reaction, ev.Item.Timestamp, ev.Item.Channel)

	// Update reaction in database
	if db.DB != nil {
		// Find the message
		var message models.Message
		if err := db.DB.Where("protocol_conv_id = ? AND protocol_msg_id = ?",
			ev.Item.Channel, ev.Item.Timestamp).
			Preload("Reactions").
			First(&message).Error; err == nil {

			// Check if reaction already exists
			reactionExists := false
			for _, r := range message.Reactions {
				if r.UserID == ev.User && r.Emoji == ":"+ev.Reaction+":" {
					reactionExists = true
					break
				}
			}

			if !reactionExists {
				// Add the reaction
				reaction := models.Reaction{
					MessageID: message.ID,
					UserID:    ev.User,
					Emoji:     ":" + ev.Reaction + ":",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}
				if err := db.DB.Create(&reaction).Error; err != nil {
					p.log("SlackProvider.handleRTMReactionAddedEvent: Failed to create reaction: %v\n", err)
				} else {
					p.log("SlackProvider.handleRTMReactionAddedEvent: Added reaction to DB\n")

					// Emit event to update frontend
					select {
					case p.eventChan <- core.ReactionEvent{InstanceID: p.getInstanceId(),
						ConversationID: ev.Item.Channel,
						MessageID:      ev.Item.Timestamp,
						UserID:         ev.User,
						Emoji:          ":" + ev.Reaction + ":",
						Added:          true,
						Timestamp:      time.Now().Unix(),
					}:
					default:
						p.log("SlackProvider.handleRTMReactionAddedEvent: Event channel full\n")
					}
				}
			}
		}
	}
}

// handleRTMReactionRemovedEvent handles a reaction being removed from a message
func (p *SlackProvider) handleRTMReactionRemovedEvent(ev *slack.ReactionRemovedEvent) {
	if ev == nil || ev.Item.Channel == "" || ev.Item.Timestamp == "" {
		return
	}

	p.log("SlackProvider.handleRTMReactionRemovedEvent: User %s removed :%s: from message %s in %s\n",
		ev.User, ev.Reaction, ev.Item.Timestamp, ev.Item.Channel)

	// Update reaction in database
	if db.DB != nil {
		// Find the message
		var message models.Message
		if err := db.DB.Where("protocol_conv_id = ? AND protocol_msg_id = ?",
			ev.Item.Channel, ev.Item.Timestamp).
			Preload("Reactions").
			First(&message).Error; err == nil {

			// Find and delete the reaction
			emojiWithColons := ":" + ev.Reaction + ":"
			for _, r := range message.Reactions {
				if r.UserID == ev.User && r.Emoji == emojiWithColons {
					if err := db.DB.Delete(&r).Error; err != nil {
						p.log("SlackProvider.handleRTMReactionRemovedEvent: Failed to delete reaction: %v\n", err)
					} else {
						p.log("SlackProvider.handleRTMReactionRemovedEvent: Removed reaction from DB\n")

						// Emit event to update frontend
						select {
						case p.eventChan <- core.ReactionEvent{InstanceID: p.getInstanceId(),
							ConversationID: ev.Item.Channel,
							MessageID:      ev.Item.Timestamp,
							UserID:         ev.User,
							Emoji:          emojiWithColons,
							Added:          false,
							Timestamp:      time.Now().Unix(),
						}:
						default:
							p.log("SlackProvider.handleRTMReactionRemovedEvent: Event channel full\n")
						}
					}
					break
				}
			}
		}
	}
}

// syncConversationHistory fetches recent history for a conversation to populate context
func (p *SlackProvider) syncConversationHistory(conversationID string) {
	p.log("SlackProvider: syncing history for new conversation %s\n", conversationID)

	if conversationID == "" {
		return
	}

	// Fetch the 100 most recent messages for new conversations
	messages, err := p.GetConversationHistory(conversationID, 100, nil, nil)
	if err != nil {
		p.log("SlackProvider: failed to sync history for %s: %v\n", conversationID, err)
		return
	}

	// Collect all unique sender IDs from the messages
	senderIDs := make(map[string]bool)
	for _, msg := range messages {
		if msg.SenderID != "" {
			senderIDs[msg.SenderID] = true
		}
	}

	// Resolve names for all senders
	if len(senderIDs) > 0 {
		userIDs := make([]string, 0, len(senderIDs))
		for userID := range senderIDs {
			userIDs = append(userIDs, userID)
		}
		p.ResolveUserNames(userIDs)
	}

	// Also make sure the conversation contact exists
	conversationName := p.resolveConversationName(conversationID, "")
	if conversationName != "" {
		if err := p.ensureConversationContact(conversationID, conversationName); err != nil {
			p.log("SlackProvider: failed to ensure conversation contact for %s: %v\n", conversationID, err)
		}
	}
}
