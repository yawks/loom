package whatsapp

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func (w *WhatsAppProvider) eventHandler(evt interface{}) {
	// Log ALL events to help debug
	eventType := fmt.Sprintf("%T", evt)
	fmt.Printf("WhatsApp: [EVENT] Received event type: %s\n", eventType)

	switch v := evt.(type) {
	case *events.Message:
		// Convert WhatsApp message to our Message model
		fmt.Printf("WhatsApp: Received message event from %s in chat %s\n", v.Info.Sender.String(), v.Info.Chat.String())

		// IMPORTANT: Create LID -> JID mapping for typing indicators
		// WhatsApp now sends typing indicators with LIDs instead of JIDs
		// We need to extract and persist the mapping from incoming messages

		// Log all message info fields for debugging
		fmt.Printf("WhatsApp: [MESSAGE] Chat: %s (server: %s), Sender: %s (server: %s), PushName: %s, IsFromMe: %v\n",
			v.Info.Chat.String(), v.Info.Chat.Server,
			v.Info.Sender.String(), v.Info.Sender.Server,
			v.Info.PushName, v.Info.IsFromMe)

		// Strategy 1: Chat is LID + Sender is standard JID (messages FROM others)
		if v.Info.Chat.Server == "lid" && v.Info.Sender.Server == types.DefaultUserServer {
			chatLID := v.Info.Chat.String()
			senderJID := v.Info.Sender.String()

			fmt.Printf("WhatsApp: *** FOUND LID MAPPING (incoming) *** Chat LID: %s -> Sender JID: %s\n", chatLID, senderJID)

			if err := w.saveLIDMapping(chatLID, senderJID); err != nil {
				fmt.Printf("WhatsApp: Warning - Failed to save LID mapping: %v\n", err)
			} else {
				// Also store in LinkedAccount.Extra for persistence
				w.storeContactMapping(chatLID, senderJID)
				w.lidToJIDMu.RLock()
				totalMappings := len(w.lidToJIDMap)
				w.lidToJIDMu.RUnlock()
				fmt.Printf("WhatsApp: Saved LID->JID mapping! Total mappings: %d\n", totalMappings)
			}
		}

		// Strategy 2: Sender is LID + Chat is standard JID (messages TO others - our outgoing messages)
		if v.Info.Sender.Server == "lid" && v.Info.Chat.Server == types.DefaultUserServer {
			senderLID := v.Info.Sender.String()
			chatJID := v.Info.Chat.String()

			fmt.Printf("WhatsApp: *** FOUND LID MAPPING (outgoing) *** Sender LID: %s -> Chat JID: %s\n", senderLID, chatJID)

			if err := w.saveLIDMapping(senderLID, chatJID); err != nil {
				fmt.Printf("WhatsApp: Warning - Failed to save LID mapping: %v\n", err)
			} else {
				// Also store in LinkedAccount.Extra for persistence
				w.storeContactMapping(senderLID, chatJID)
				w.lidToJIDMu.RLock()
				totalMappings := len(w.lidToJIDMap)
				w.lidToJIDMu.RUnlock()
				fmt.Printf("WhatsApp: Saved LID->JID mapping! Total mappings: %d\n", totalMappings)
			}
		}

		// Log if neither condition matched
		if v.Info.Chat.Server != "lid" && v.Info.Sender.Server != "lid" {
			fmt.Printf("WhatsApp: [MESSAGE] No LID detected, both are standard JIDs\n")

			// WORKAROUND: WhatsApp normalizes LIDs in MessageInfo but keeps them in XML
			// The raw XML message contains sender_lid that we can't access through whatsmeow
			// As a workaround, we'll query the database to see if we've received any
			// ChatPresence events with LIDs recently and try to map them to this contact

			// This is a known limitation: we need to rely on the user exchanging messages
			// AFTER receiving a ChatPresence event to create the mapping
			fmt.Printf("WhatsApp: [MESSAGE] Note: If this contact uses LID privacy, the mapping will be created from ChatPresence fallback\n")
		} else if v.Info.Chat.Server == "lid" && v.Info.Sender.Server == "lid" {
			fmt.Printf("WhatsApp: [MESSAGE] WARNING - Both Chat and Sender are LIDs! Cannot create mapping.\n")
		}

		// Check if this is a reaction message
		if v.Message != nil && v.Message.GetReactionMessage() != nil {
			reactionMsg := v.Message.GetReactionMessage()
			key := reactionMsg.GetKey()
			if key != nil {
				targetMsgID := key.GetID()
				targetConvID := key.GetRemoteJID()
				if targetConvID == "" {
					targetConvID = v.Info.Chat.String()
				}
				emoji := reactionMsg.GetText()
				senderID := v.Info.Sender.String()

				// Empty emoji means reaction was removed
				added := emoji != ""

				fmt.Printf("WhatsApp: Received reaction event: message=%s, emoji=%s, added=%v, sender=%s\n", targetMsgID, emoji, added, senderID)

				// Emit reaction event
				select {
				case w.eventChan <- core.ReactionEvent{
					ConversationID: targetConvID,
					MessageID:      targetMsgID,
					UserID:         senderID,
					Emoji:          emoji,
					Added:          added,
					Timestamp:      v.Info.Timestamp.Unix(),
				}:
					fmt.Printf("WhatsApp: ReactionEvent emitted successfully for message %s, emoji %s\n", targetMsgID, emoji)
				default:
					fmt.Printf("WhatsApp: WARNING - Failed to emit ReactionEvent (channel full) for message %s\n", targetMsgID)
				}
				break
			}
		}

		if w.tryHandleProtocolMessage(v, true) {
			fmt.Printf("WhatsApp: Message event was a protocol update (handled separately)\n")
			break
		}

		// Check if this is an edited message (message with same ID as existing message)
		convID := v.Info.Chat.String()
		msgID := v.Info.ID
		fmt.Printf("WhatsApp: Processing message event: ID=%s, Chat=%s, Sender=%s\n", msgID, convID, v.Info.Sender.String())

		// First, convert the message to get its body
		msg := w.convertWhatsAppMessage(v)
		if msg == nil {
			fmt.Printf("WhatsApp: WARNING - convertWhatsAppMessage returned nil for message %s\n", msgID)
			break
		}

		// Check if message with same ID already exists
		var existingMsg *models.Message
		w.mu.RLock()
		if msgs, ok := w.conversationMessages[convID]; ok {
			for _, m := range msgs {
				if m.ProtocolMsgID == msgID {
					existingMsg = &m
					break
				}
			}
		}
		w.mu.RUnlock()

		// If not found in cache, check database
		if existingMsg == nil && db.DB != nil {
			var dbMsg models.Message
			if err := db.DB.Preload("Receipts").Preload("Reactions").Where("protocol_msg_id = ?", msgID).First(&dbMsg).Error; err == nil {
				existingMsg = &dbMsg
				fmt.Printf("WhatsApp: Found existing message %s in database\n", msgID)
				// Also add to cache if we found it in DB
				w.mu.Lock()
				if msgs, ok := w.conversationMessages[convID]; ok {
					// Check if it's not already there (shouldn't be, but just in case)
					found := false
					for _, m := range msgs {
						if m.ProtocolMsgID == msgID {
							found = true
							break
						}
					}
					if !found {
						w.conversationMessages[convID] = append(msgs, dbMsg)
					}
				}
				w.mu.Unlock()
			} else {
				fmt.Printf("WhatsApp: Message %s not found in database: %v\n", msgID, err)
			}
		}

		// If message exists, it might be an edit - check if content changed
		if existingMsg != nil {
			fmt.Printf("WhatsApp: Found existing message %s: old body='%s', new body='%s'\n", msgID, existingMsg.Body, msg.Body)
			if msg.Body != existingMsg.Body && msg.Body != "" {
				// This is an edited message - update it
				fmt.Printf("WhatsApp: Detected edited message %s (content changed from '%s' to '%s')\n", msgID, existingMsg.Body, msg.Body)
				editedAt := time.Now()
				existingMsg.Body = msg.Body
				existingMsg.IsEdited = true
				existingMsg.EditedTimestamp = &editedAt

				// Update in cache
				w.mu.Lock()
				if msgs, ok := w.conversationMessages[convID]; ok {
					for idx := range msgs {
						if msgs[idx].ProtocolMsgID == msgID {
							msgs[idx] = *existingMsg
							w.conversationMessages[convID][idx] = msgs[idx]
							fmt.Printf("WhatsApp: Updated message %s in cache\n", msgID)
							break
						}
					}
				}
				w.mu.Unlock()

				// Update in database
				if db.DB != nil {
					updates := map[string]interface{}{
						"body":             existingMsg.Body,
						"is_edited":        true,
						"edited_timestamp": editedAt,
					}
					if err := db.DB.Model(&models.Message{}).
						Where("protocol_msg_id = ?", msgID).
						Updates(updates).Error; err != nil {
						fmt.Printf("WhatsApp: Failed to update edited message in database: %v\n", err)
					} else {
						fmt.Printf("WhatsApp: Successfully updated edited message %s in database\n", msgID)
					}
				}

				// Emit event
				select {
				case w.eventChan <- core.MessageEvent{Message: *existingMsg}:
					fmt.Printf("WhatsApp: MessageEvent emitted for edited message %s\n", msgID)
				default:
					fmt.Printf("WhatsApp: WARNING - Failed to emit MessageEvent for edited message %s\n", msgID)
				}
				break
			}
			if msg.Body == existingMsg.Body {
				// Same content, might be a duplicate - skip it
				fmt.Printf("WhatsApp: Received duplicate message %s with same content, skipping\n", msgID)
				break
			}
			fmt.Printf("WhatsApp: Message %s exists but content unchanged or empty, treating as new\n", msgID)
		}

		// Normal message processing
		msg = w.convertWhatsAppMessage(v)
		if msg != nil {
			fmt.Printf("WhatsApp: Converted message successfully, ID: %s, Body: %s, Attachments: %s\n", msg.ProtocolMsgID, msg.Body, msg.Attachments)
			// Check if message has media but no attachments
			if msg.Attachments == "" && (v.Message.GetImageMessage() != nil || v.Message.GetVideoMessage() != nil || v.Message.GetAudioMessage() != nil || v.Message.GetDocumentMessage() != nil || v.Message.GetStickerMessage() != nil) {
				fmt.Printf("WhatsApp: WARNING - Message %s has media but no attachments were extracted! Chat: %s, Sender: %s\n", msg.ProtocolMsgID, v.Info.Chat.String(), v.Info.Sender.String())
			}
			w.appendMessageToConversation(msg)

			// Update last sync timestamp when receiving a new message
			w.saveLastSyncTimestamp(msg.Timestamp)

			select {
			case w.eventChan <- core.MessageEvent{Message: *msg}:
				fmt.Printf("WhatsApp: MessageEvent emitted successfully for message %s\n", msg.ProtocolMsgID)
			default:
				fmt.Printf("WhatsApp: WARNING - Failed to emit MessageEvent (channel full) for message %s\n", msg.ProtocolMsgID)
			}
			select {
			case w.eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "message_received"}:
				fmt.Printf("WhatsApp: ContactStatusEvent emitted successfully\n")
			default:
				fmt.Printf("WhatsApp: WARNING - Failed to emit ContactStatusEvent (channel full)\n")
			}
		} else {
			fmt.Printf("WhatsApp: WARNING - convertWhatsAppMessage returned nil for message from %s in chat %s\n", v.Info.Sender.String(), v.Info.Chat.String())
		}
	case *events.ChatPresence:
		// Handle chat presence updates (typing indicators)
		chatJID := v.MessageSource.Chat
		senderJID := v.MessageSource.Sender

		fmt.Printf("WhatsApp: Received ChatPresence event - Chat: %s (server: %s), Sender: %s (server: %s), State: %s\n",
			chatJID.String(),
			chatJID.Server,
			senderJID.String(),
			senderJID.Server,
			v.State)

		// Determine the real conversation ID
		conversationID := chatJID.String()
		userID := senderJID.String()

		// For 1-on-1 chats, the Chat field in ChatPresence can be a LID
		// In that case, the actual conversation ID should be the phone number JID of the other person
		// We need to check if we have this conversation in our database and use its actual JID
		if chatJID.Server == "lid" {
			fmt.Printf("WhatsApp: Chat is a LID, need to resolve to actual conversation ID\n")

			// Strategy 1: Check the memory cache (fastest)
			w.lidToJIDMu.RLock()
			cached, found := w.lidToJIDMap[chatJID.String()]
			w.lidToJIDMu.RUnlock()

			if found {
				conversationID = cached
				fmt.Printf("WhatsApp: Resolved LID %s to conversation ID %s from cache\n", chatJID.String(), conversationID)
			} else {
				// Strategy 2: Check the database (persistent storage)
				if db.DB != nil {
					var mapping models.LIDMapping
					if err := db.DB.Where("lid = ? AND protocol = ?", chatJID.String(), "whatsapp").First(&mapping).Error; err == nil {
						conversationID = mapping.JID
						fmt.Printf("WhatsApp: Resolved LID %s to conversation ID %s from database\n", chatJID.String(), conversationID)

						// Update cache for next time
						w.lidToJIDMu.Lock()
						w.lidToJIDMap[chatJID.String()] = conversationID
						w.lidToJIDMu.Unlock()
					} else {
						fmt.Printf("WhatsApp: LID %s not found in database: %v\n", chatJID.String(), err)
					}
				}

				// Strategy 3: Try other resolution methods if still not found
				if conversationID == chatJID.String() {
					// Try to get contact info from WhatsApp for this LID
					// The LID should be resolvable through the WhatsApp client
					if w.client != nil && w.client.Store != nil {
						// Try to find this contact in the store
						contactInfo, err := w.client.Store.Contacts.GetContact(w.ctx, chatJID)
						if err == nil && contactInfo.Found {
							fmt.Printf("WhatsApp: Found contact info for LID %s: FullName=%s, PushName=%s\n",
								chatJID.String(), contactInfo.FullName, contactInfo.PushName)
						} else {
							fmt.Printf("WhatsApp: Could not find contact info for LID %s (error: %v)\n", chatJID.String(), err)
						}
					}

					// Try using the sender's JID as it's likely the contact's actual JID
					if senderJID.Server == types.DefaultUserServer {
						conversationID = senderJID.String()
						fmt.Printf("WhatsApp: Using sender JID %s as conversation ID for LID %s\n", conversationID, chatJID.String())

						// Save this mapping for future use
						if err := w.saveLIDMapping(chatJID.String(), conversationID); err != nil {
							fmt.Printf("WhatsApp: Warning - Failed to save LID mapping: %v\n", err)
						}
					} else if senderJID.Server == "lid" {
						// Both Chat and Sender are LIDs - this is tricky
						// WhatsApp normalizes sender_lid in the XML to standard JID in MessageInfo
						// but ChatPresence still uses LIDs for both fields

						// Strategy: Search recent messages to find a conversation that might match this LID
						// We'll look for recent messages from any contact and try to match
						fmt.Printf("WhatsApp: Both Chat and Sender are LIDs (%s, %s) - attempting smart resolution\n",
							chatJID.String(), senderJID.String())

						if db.DB != nil {
							// Get the most recent message from ANY conversation to see active chats
							// We're looking for 1-on-1 conversations (not groups)
							var recentMessages []models.Message
							if err := db.DB.Where("is_from_me = ? AND protocol_conv_id LIKE ?", false, "%@s.whatsapp.net").
								Order("timestamp DESC").
								Limit(10).
								Find(&recentMessages).Error; err == nil && len(recentMessages) > 0 {

								fmt.Printf("WhatsApp: Found %d recent incoming messages, checking for potential match\n", len(recentMessages))

								// Look for a conversation that has recent activity
								for _, msg := range recentMessages {
									// This is a candidate - the LID might be for this conversation
									fmt.Printf("WhatsApp: Candidate conversation: %s (last message: %v)\n",
										msg.ProtocolConvID, msg.Timestamp)

									// For now, try the most recent one as it's likely the active conversation
									// where typing is happening
									if msg.ProtocolConvID != "" && !strings.HasSuffix(msg.ProtocolConvID, "@lid") {
										conversationID = msg.ProtocolConvID
										fmt.Printf("WhatsApp: Tentatively mapping LID %s to most recent conversation %s\n",
											chatJID.String(), conversationID)

										// Save this mapping (it might be wrong, but we'll update it if needed)
										if err := w.saveLIDMapping(chatJID.String(), conversationID); err != nil {
											fmt.Printf("WhatsApp: Warning - Failed to save tentative LID mapping: %v\n", err)
										} else {
											fmt.Printf("WhatsApp: Saved tentative LID mapping based on recent activity\n")
										}
										break
									}
								}
							}
						}
					}

					// Last resort: skip this typing event if we still can't resolve it
					if conversationID == chatJID.String() {
						fmt.Printf("WhatsApp: WARNING - Could not resolve LID %s to any JID. Skipping typing event.\n", chatJID.String())
						fmt.Printf("WhatsApp: TIP - Send or receive a message in this conversation to create the LID mapping\n")
						return
					}
				}
			}
		}

		// ChatPresence events contain typing indicator information
		// State can be "composing" (typing) or "paused" (stopped typing)

		// Get the display name of the user who is typing
		// For groups, we need the name of the participant who is typing, not the group name
		var userName string
		conversationJID, err := types.ParseJID(conversationID)
		if err == nil && conversationJID.Server == types.GroupServer {
			// This is a group conversation - get the name of the participant who is typing
			userJID, err := types.ParseJID(userID)
			if err == nil {
				userName = w.lookupSenderNameInGroup(userJID, conversationJID)
				fmt.Printf("WhatsApp: Resolved participant name for group %s: %s (user: %s)\n", conversationID, userName, userID)
			} else {
				fmt.Printf("WhatsApp: Failed to parse userID %s as JID: %v\n", userID, err)
				userName = userID
			}
		} else {
			// This is a 1-on-1 conversation - get the name of the contact
			userName, err = w.GetContactName(conversationID)
			if err != nil {
				fmt.Printf("WhatsApp: Failed to get contact name for %s: %v, using fallback\n", conversationID, err)
				// Fallback to the conversation ID if we can't get the name
				userName = conversationID
			}
			fmt.Printf("WhatsApp: Resolved contact name for %s: %s\n", conversationID, userName)
		}

		if v.State == types.ChatPresenceComposing {
			// User started typing
			fmt.Printf("WhatsApp: User %s (%s) started typing in conversation %s\n", userName, userID, conversationID)

			// If user is typing, they are online - emit a presence event
			// This ensures we show online status even if we haven't subscribed to their presence
			select {
			case w.eventChan <- core.PresenceEvent{
				UserID:   userID,
				IsOnline: true,
				LastSeen: 0,
			}:
				fmt.Printf("WhatsApp: PresenceEvent (online) emitted for typing user %s\n", userID)
			default:
				fmt.Printf("WhatsApp: WARNING - Failed to emit PresenceEvent for typing user\n")
			}

			// Also emit for the conversation ID if it's different (for LID resolution)
			if conversationID != userID {
				select {
				case w.eventChan <- core.PresenceEvent{
					UserID:   conversationID,
					IsOnline: true,
					LastSeen: 0,
				}:
					fmt.Printf("WhatsApp: PresenceEvent (online) emitted for conversation %s\n", conversationID)
				default:
				}
			}

			select {
			case w.eventChan <- core.TypingEvent{
				ConversationID: conversationID,
				UserID:         userID,
				UserName:       userName,
				IsTyping:       true,
			}:
				fmt.Printf("WhatsApp: TypingEvent (started) emitted successfully\n")
			default:
				fmt.Printf("WhatsApp: WARNING - Failed to emit TypingEvent (channel full)\n")
			}
		} else if v.State == types.ChatPresencePaused {
			// User stopped typing
			fmt.Printf("WhatsApp: User %s (%s) stopped typing in conversation %s\n", userName, userID, conversationID)
			select {
			case w.eventChan <- core.TypingEvent{
				ConversationID: conversationID,
				UserID:         userID,
				UserName:       userName,
				IsTyping:       false,
			}:
				fmt.Printf("WhatsApp: TypingEvent (stopped) emitted successfully\n")
			default:
				fmt.Printf("WhatsApp: WARNING - Failed to emit TypingEvent (channel full)\n")
			}
		}
	case *events.Presence:
		// Handle presence updates (online/offline status)
		userID := v.From.String()
		isOnline := !v.Unavailable
		var lastSeen int64
		if !v.LastSeen.IsZero() {
			lastSeen = v.LastSeen.Unix()
		}

		fmt.Printf("WhatsApp: Presence update: %s is %s (LastSeen: %v)\n",
			userID,
			map[bool]string{true: "online", false: "offline"}[isOnline],
			v.LastSeen)

		// Emit PresenceEvent to frontend for the original userID (might be LID)
		select {
		case w.eventChan <- core.PresenceEvent{
			UserID:   userID,
			IsOnline: isOnline,
			LastSeen: lastSeen,
		}:
			fmt.Printf("WhatsApp: PresenceEvent emitted for %s\n", userID)
		default:
			fmt.Printf("WhatsApp: WARNING - Failed to emit PresenceEvent (channel full)\n")
		}

		// If this is a LID, also emit for the resolved JID
		if strings.HasSuffix(userID, "@lid") {
			// Try to resolve LID to JID
			w.lidToJIDMu.RLock()
			resolvedJID, found := w.lidToJIDMap[userID]
			w.lidToJIDMu.RUnlock()

			if found && resolvedJID != userID {
				fmt.Printf("WhatsApp: Resolved LID %s to JID %s for presence\n", userID, resolvedJID)
				// Emit presence event for the resolved JID as well
				select {
				case w.eventChan <- core.PresenceEvent{
					UserID:   resolvedJID,
					IsOnline: isOnline,
					LastSeen: lastSeen,
				}:
					fmt.Printf("WhatsApp: PresenceEvent emitted for resolved JID %s\n", resolvedJID)
				default:
					fmt.Printf("WhatsApp: WARNING - Failed to emit PresenceEvent for resolved JID (channel full)\n")
				}
			} else {
				fmt.Printf("WhatsApp: LID %s not found in mapping, only emitting for LID\n", userID)
			}
		}
	case *events.QR:
		// QR code event - this is handled by the QR channel
		// No need to log here as it's already handled by the QR channel goroutine
	case *events.Connected:
		fmt.Println("WhatsApp: Connected event received - client is now fully connected")
		// Check if we're logged in now
		if w.client != nil && w.client.Store.ID != nil {
			fmt.Printf("WhatsApp: Successfully logged in as %s\n", w.client.Store.ID)

			// IMPORTANT: Mark client as available to receive typing indicators
			// Without this, WhatsApp will not send ChatPresence events (typing notifications)
			// Reference: https://github.com/tulir/whatsmeow/discussions/681
			err := w.client.SendPresence(w.ctx, types.PresenceAvailable)
			if err != nil {
				fmt.Printf("WhatsApp: Warning - Failed to send presence available: %v\n", err)
			} else {
				fmt.Println("WhatsApp: Marked self as available - will now receive typing indicators and presence updates")
			}

			// Subscribe to presence updates for DM contacts
			// This allows us to receive online/offline status updates
                    // DISABLED: go w.subscribeToContactPresence()

			// Build LID mappings from existing conversations
			// This allows typing indicators to work immediately on existing chats
			go w.buildLIDMappingsFromConversations()

			// Note: Device name is set during initial pairing via QR code
			// To change the name to "loom", you need to unpair and re-pair the device
			// The name will appear as "loom" after re-pairing
			fmt.Println("WhatsApp: Ready to receive messages and conversations")
			// Emit sync status event - connection successful, starting sync
			// Don't emit completed here - wait for OfflineSyncCompleted which is the final event
			w.emitSyncStatus(core.SyncStatusFetchingContacts, "Connected, synchronizing...", -1)

			// Fallback: If OfflineSyncCompleted is not received within 30 seconds, emit completed anyway
			go func() {
				time.Sleep(30 * time.Second)
				// Check if we're still connected and have conversations
				if w.client != nil && w.client.Store != nil && w.client.Store.ID != nil {
					contacts, err := w.GetContacts()
					if err == nil && len(contacts) > 0 {
						fmt.Printf("WhatsApp: Fallback - emitting completed sync status after 30s timeout with %d conversations\n", len(contacts))
						w.emitSyncStatus(core.SyncStatusCompleted, fmt.Sprintf("Sync completed - %d conversations available", len(contacts)), 100)
					}
				}
			}()
		}
	case *events.Disconnected:
		fmt.Printf("WhatsApp: Disconnected event received\n")
	case *events.LoggedOut:
		fmt.Println("WhatsApp: Logged out event received")
	case *events.StreamError:
		fmt.Printf("WhatsApp: Stream error: %v\n", v)
	case *events.Receipt:
		// Handle read receipts (message read confirmations)
		// Convert to ReceiptEvent and emit
		if len(v.MessageIDs) == 0 {
			fmt.Printf("WhatsApp: Receipt event has no message IDs, skipping.\n")
			break
		}
		receiptType := core.ReceiptTypeDelivery
		if v.Type == types.ReceiptTypeRead {
			receiptType = core.ReceiptTypeRead
		}
		fmt.Printf("WhatsApp: Processing receipt event for chat %s, type: %s, message IDs: %v\n", v.Chat.String(), receiptType, v.MessageIDs)
		// Emit a ReceiptEvent for each message ID
		for _, msgID := range v.MessageIDs {
			select {
			case w.eventChan <- core.ReceiptEvent{
				ConversationID: v.Chat.String(),
				MessageID:      msgID,
				ReceiptType:    receiptType,
				UserID:         v.Sender.String(),
				Timestamp:      v.Timestamp.Unix(),
			}:
				fmt.Printf("WhatsApp: ReceiptEvent emitted successfully for message %s\n", msgID)
			default:
				fmt.Printf("WhatsApp: WARNING - Failed to emit ReceiptEvent for message %s (channel full)\n", msgID)
			}
		}
	case *events.HistorySync:
		// History sync contains conversations and messages
		fmt.Println("WhatsApp: History sync received - conversations are being synced")
		
		// Cache conversations from the history sync data so we can display them immediately
		if v != nil && v.Data != nil {
			fmt.Printf("WhatsApp: ===== HISTORY SYNC STARTED =====\n")
			w.cacheConversationsFromHistory(v.Data)
			w.cacheMessagesFromHistory(v.Data)
			// Process call log records to enrich call messages with summary information
			fmt.Printf("WhatsApp: ===== PROCESSING CALL LOG RECORDS =====\n")
			w.processCallLogRecords(v.Data)
			fmt.Printf("WhatsApp: ===== CALL LOG RECORDS PROCESSING COMPLETE =====\n")

			// Update last sync timestamp after successful history sync
			now := time.Now()
			w.saveLastSyncTimestamp(now)
		}

		// Emit sync status event - history sync in progress
		w.emitSyncStatus(core.SyncStatusFetchingHistory, "Syncing message history...", -1)
		// Trigger a contact refresh after history sync
		// Use a goroutine to delay the refresh slightly to allow whatsmeow to process the sync
		go func() {
			time.Sleep(2 * time.Second) // Wait a bit for sync to complete
			// Check if provider is still active before sending event
			w.mu.RLock()
			disconnected := w.disconnected
			eventChan := w.eventChan
			ctx := w.ctx
			w.mu.RUnlock()

			if disconnected || eventChan == nil || ctx == nil {
				fmt.Printf("WhatsApp: Skipping contact refresh event - provider disconnected or channel closed\n")
				return
			}

			select {
			case <-ctx.Done():
				// Provider was cancelled/disconnected
				fmt.Printf("WhatsApp: Skipping contact refresh event - context cancelled\n")
				return
			case eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "sync_complete"}:
				// Event sent successfully
			default:
				// Channel full, skip
			}
		}()
	case *events.AppStateSyncComplete:
		// App state sync completed - contacts and conversations are now available
		fmt.Printf("WhatsApp: App state sync completed for collection: %s\n", v.Name)
		// After app state sync, conversations should be available
		// Emit sync status event - app state sync completed, now fetching contacts
		// Don't emit completed here - wait for OfflineSyncCompleted which is the final event
		w.emitSyncStatus(core.SyncStatusFetchingContacts, "Fetching conversations...", -1)
		go func() {
			// Check if provider is still active before sending event
			w.mu.RLock()
			disconnected := w.disconnected
			eventChan := w.eventChan
			ctx := w.ctx
			w.mu.RUnlock()

			if disconnected || eventChan == nil || ctx == nil {
				fmt.Printf("WhatsApp: Skipping contact refresh event - provider disconnected or channel closed\n")
				return
			}

			select {
			case <-ctx.Done():
				// Provider was cancelled/disconnected
				fmt.Printf("WhatsApp: Skipping contact refresh event - context cancelled\n")
				return
			case eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "sync_complete"}:
				// Event sent successfully
			default:
				// Channel full, skip
			}
		}()
	case *events.OfflineSyncCompleted:
// Offline sync completed - all data is now synced
// This is the FINAL sync event - emit completed status here
fmt.Println("WhatsApp: Offline sync completed - conversations should now be available")

// Wait a bit for the store to be fully populated, then fetch contacts and emit final completed
go func() {
time.Sleep(2 * time.Second) // Wait for store to be fully populated

contacts, err := w.GetContacts()
if err != nil {
fmt.Printf("WhatsApp: Failed to fetch contacts after offline sync: %v\n", err)
fmt.Printf("WhatsApp: Emitting error sync status event\n")
w.emitSyncStatus(core.SyncStatusError, fmt.Sprintf("Failed to refresh conversations: %v", err), -1)
} else {
fmt.Printf("WhatsApp: Fetched %d conversations after offline sync\n", len(contacts))
// This is the final completed event - sync is fully done
fmt.Printf("WhatsApp: Emitting completed sync status event with %d conversations\n", len(contacts))
w.emitSyncStatus(core.SyncStatusCompleted, fmt.Sprintf("Sync completed - %d conversations available", len(contacts)), 100)
fmt.Printf("WhatsApp: Completed sync status event emitted\n")
}
// Emit a contact refresh event
select {
case w.eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "sync_complete"}:
default:
}
}()
case *events.CallOffer:
		// Handle incoming call offer
		fmt.Printf("WhatsApp: Received CallOffer event - CallCreator: %s (server: %s), CallID: %s\n",
			v.CallCreator.String(), v.CallCreator.Server, v.CallID)

		// CallID is NOT the conversation ID - it's a unique call identifier
		// The conversation ID should be the CallCreator (the person calling us)
		callCreatorJID := v.CallCreator
		callID := v.CallID // Keep CallID for message ID generation

		// Resolve CallCreator LID to JID if needed (this is the actual conversation ID)
		// Use the unified resolveContactID function
		callCreatorJIDStr := callCreatorJID.String()
		resolvedID, err := w.resolveContactID(callCreatorJIDStr)
		if err != nil {
			fmt.Printf("WhatsApp: WARNING - Could not resolve CallCreator LID %s to JID: %v\n", callCreatorJIDStr, err)
			fmt.Printf("WhatsApp: TIP - Send or receive a message in this conversation to create the LID mapping\n")
			// Don't skip - try to create the message anyway, it will be resolved later
			resolvedID = callCreatorJIDStr
		} else {
			// Store the mapping if it was a LID
			if callCreatorJIDStr != resolvedID {
				w.storeContactMapping(callCreatorJIDStr, resolvedID)
			}
		}

		// Parse resolved ID back to JID
		var parseErr error
		callCreatorJID, parseErr = types.ParseJID(resolvedID)
		if parseErr != nil {
			fmt.Printf("WhatsApp: Failed to parse resolved CallCreator JID %s: %v\n", resolvedID, parseErr)
			return
		}

		// The conversation ID is the CallCreator (resolved to JID if it was a LID)
		convID := resolvedID

		fmt.Printf("WhatsApp: Using conversation ID %s (resolved from CallCreator %s) for call %s\n", convID, v.CallCreator.String(), callID)

		// Determine if it's a group call
		isGroup := strings.Contains(convID, "@g.us")

		// Determine call type based on call offer
		// CallOffer is an incoming call (ringing), not a missed call yet
		// The actual call type will be determined later from CallTerminate
		callType := "incoming_call"
		if isGroup {
			callType = "incoming_group_call"
		}

		// Create a call message
		now := time.Now()
		callMsgID := fmt.Sprintf("call_%s_%d", callID, now.Unix())

		// Use resolved CallCreator JID for sender ID
		senderID := callCreatorJID.String()
		if callCreatorJID.Server == "lid" {
			// If still a LID, use original
			senderID = v.CallCreator.String()
		}

		callMessage := &models.Message{
			ProtocolConvID: convID,
			ProtocolMsgID:  callMsgID,
			SenderID:       senderID,
			SenderName:     "", // Will be filled from contact info if available
			Body:           "", // Call messages don't have body text
			Timestamp:      now,
			IsFromMe:       false, // Incoming call
			CallType:       callType,
			CallIsVideo:    false, // Will be updated from call logs if available
		}

		// Try to get sender name from contact info using resolved JID
		if w.client != nil && w.client.Store != nil && w.client.Store.Contacts != nil && callCreatorJID.Server != "lid" {
			contact, err := w.client.Store.Contacts.GetContact(w.ctx, callCreatorJID)
			if err == nil && contact.Found {
				if contact.FullName != "" {
					callMessage.SenderName = contact.FullName
				} else if contact.PushName != "" {
					callMessage.SenderName = contact.PushName
				} else if contact.FirstName != "" {
					callMessage.SenderName = contact.FirstName
				}
			}
		}

		// If we still don't have a name, try using GetContactName helper
		if callMessage.SenderName == "" && senderID != "" {
			if name, err := w.GetContactName(senderID); err == nil && name != "" {
				callMessage.SenderName = name
			}
		}

		// Store the message
		fmt.Printf("WhatsApp: [CALL MSG] CallOffer - Storing call message: ProtocolMsgID=%s, ProtocolConvID=%s, CallType=%s\n",
			callMessage.ProtocolMsgID, callMessage.ProtocolConvID, callMessage.CallType)
		w.appendMessageToConversation(callMessage)

		// Emit as a message event so it appears in the conversation
		select {
		case w.eventChan <- core.MessageEvent{Message: *callMessage}:
			fmt.Printf("WhatsApp: [CALL MSG] CallOffer message event emitted successfully for call %s in conversation %s\n", callID, convID)
		default:
			fmt.Printf("WhatsApp: [CALL MSG] WARNING - Failed to emit CallOffer message event (channel full) for call %s\n", callID)
		}

		// Also emit a contact refresh to update the conversation list
		select {
		case w.eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "call_received"}:
			fmt.Printf("WhatsApp: ContactStatusEvent emitted for call\n")
		default:
		}
	case *events.CallTerminate:
		// Handle call termination - this might be the only event we receive for missed calls
		// CallTerminate is sent when a call ends, including missed calls
		fmt.Printf("WhatsApp: Received CallTerminate event - CallCreator: %s (server: %s), CallID: %s\n",
			v.CallCreator.String(), v.CallCreator.Server, v.CallID)

		// CallID is NOT the conversation ID - it's a unique call identifier
		// The conversation ID should be the CallCreator (the person calling us)
		callCreatorJID := v.CallCreator
		callID := v.CallID // Keep CallID for message ID generation

		// Resolve CallCreator LID to JID if needed (this is the actual conversation ID)
		// Use the unified resolveContactID function
		callCreatorJIDStr := callCreatorJID.String()
		resolvedID, err := w.resolveContactID(callCreatorJIDStr)
		if err != nil {
			fmt.Printf("WhatsApp: WARNING - Could not resolve CallCreator LID %s to JID: %v\n", callCreatorJIDStr, err)
			fmt.Printf("WhatsApp: TIP - Send or receive a message in this conversation to create the LID mapping\n")
			// Don't skip - try to create the message anyway, it will be resolved later
			resolvedID = callCreatorJIDStr
		} else {
			// Store the mapping if it was a LID
			if callCreatorJIDStr != resolvedID {
				w.storeContactMapping(callCreatorJIDStr, resolvedID)
			}
		}

		// Parse resolved ID back to JID
		var parseErr error
		callCreatorJID, parseErr = types.ParseJID(resolvedID)
		if parseErr != nil {
			fmt.Printf("WhatsApp: Failed to parse resolved CallCreator JID %s: %v\n", resolvedID, parseErr)
			return
		}

		// The conversation ID is the CallCreator (resolved to JID if it was a LID)
		convID := resolvedID

		fmt.Printf("WhatsApp: Using conversation ID %s (resolved from CallCreator %s) for call %s\n", convID, v.CallCreator.String(), callID)

		// Determine if it's a group call
		isGroup := strings.Contains(convID, "@g.us")

		// Try to find existing call message created by CallOffer
		var existingCallMessage *models.Message
		if db.DB != nil {
			var dbMsg models.Message
			// Look for call message with this CallID
			if err := db.DB.Where("protocol_msg_id LIKE ? AND protocol_conv_id = ?", fmt.Sprintf("call_%s%%", callID), convID).First(&dbMsg).Error; err == nil {
				existingCallMessage = &dbMsg
				fmt.Printf("WhatsApp: Found existing call message for call %s, will update it\n", callID)
			}
		}

		// If no existing message, check in-memory cache
		if existingCallMessage == nil {
			w.mu.RLock()
			if msgs, ok := w.conversationMessages[convID]; ok {
				for i := range msgs {
					if strings.HasPrefix(msgs[i].ProtocolMsgID, fmt.Sprintf("call_%s", callID)) {
						existingCallMessage = &msgs[i]
						fmt.Printf("WhatsApp: Found existing call message in cache for call %s\n", callID)
						break
					}
				}
			}
			w.mu.RUnlock()
		}

		callTimestamp := time.Now()
		if !v.Timestamp.IsZero() {
			callTimestamp = v.Timestamp
		}

		// Determine final call type based on termination reason
		// For now, we'll mark it as missed since CallTerminate usually means the call ended without being answered
		// The actual outcome will be determined from call logs if available
		var callType string
		var callOutcome string
		if isGroup {
			callType = "missed_group_voice"
		} else {
			callType = "missed_voice"
		}
		callOutcome = "MISSED" // Default, will be updated from call logs if available

		if existingCallMessage != nil {
			// Update existing message with termination info
			existingCallMessage.CallType = callType
			existingCallMessage.CallOutcome = callOutcome
			existingCallMessage.Timestamp = callTimestamp // Update timestamp to termination time

			// Update in database
			if db.DB != nil {
				if err := db.DB.Save(existingCallMessage).Error; err != nil {
					fmt.Printf("WhatsApp: Failed to update call message in database: %v\n", err)
				} else {
					fmt.Printf("WhatsApp: Updated call message in database for call %s\n", callID)
				}
			}

			// Update in cache
			w.mu.Lock()
			if msgs, ok := w.conversationMessages[convID]; ok {
				for i := range msgs {
					if msgs[i].ProtocolMsgID == existingCallMessage.ProtocolMsgID {
						msgs[i] = *existingCallMessage
						break
					}
				}
			}
			w.mu.Unlock()

			// Emit updated message event
			select {
			case w.eventChan <- core.MessageEvent{Message: *existingCallMessage}:
				fmt.Printf("WhatsApp: CallTerminate updated message event emitted successfully for call %s in conversation %s\n", callID, convID)
			default:
				fmt.Printf("WhatsApp: WARNING - Failed to emit CallTerminate update message event (channel full) for call %s\n", callID)
			}
		} else {
			// No existing message found, create a new one (fallback case)
			fmt.Printf("WhatsApp: No existing call message found for call %s, creating new termination message\n", callID)
			callMsgID := fmt.Sprintf("call_%s_%d", callID, callTimestamp.Unix())

			senderID := callCreatorJID.String()
			if callCreatorJID.Server == "lid" {
				senderID = v.CallCreator.String()
			}

			callMessage := &models.Message{
				ProtocolConvID: convID,
				ProtocolMsgID:  callMsgID,
				SenderID:       senderID,
				SenderName:     "",
				Body:           "",
				Timestamp:      callTimestamp,
				IsFromMe:       false,
				CallType:       callType,
				CallIsVideo:    false,
				CallOutcome:    callOutcome,
			}

			// Try to get sender name
			if w.client != nil && w.client.Store != nil && w.client.Store.Contacts != nil && callCreatorJID.Server != "lid" {
				contact, err := w.client.Store.Contacts.GetContact(w.ctx, callCreatorJID)
				if err == nil && contact.Found {
					if contact.FullName != "" {
						callMessage.SenderName = contact.FullName
					} else if contact.PushName != "" {
						callMessage.SenderName = contact.PushName
					} else if contact.FirstName != "" {
						callMessage.SenderName = contact.FirstName
					}
				}
			}

			if callMessage.SenderName == "" && senderID != "" {
				if name, err := w.GetContactName(senderID); err == nil && name != "" {
					callMessage.SenderName = name
				}
			}

			// Store the message
			fmt.Printf("WhatsApp: [CALL MSG] CallTerminate - Storing call message: ProtocolMsgID=%s, ProtocolConvID=%s, CallType=%s\n",
				callMessage.ProtocolMsgID, callMessage.ProtocolConvID, callMessage.CallType)
			w.appendMessageToConversation(callMessage)

			// Emit as a message event
			select {
			case w.eventChan <- core.MessageEvent{Message: *callMessage}:
				fmt.Printf("WhatsApp: [CALL MSG] CallTerminate new message event emitted successfully for call %s in conversation %s\n", callID, convID)
			default:
				fmt.Printf("WhatsApp: [CALL MSG] WARNING - Failed to emit CallTerminate message event (channel full) for call %s\n", callID)
			}
		}

		// Emit contact refresh
		select {
		case w.eventChan <- core.ContactStatusEvent{UserID: "refresh", Status: "call_received"}:
			fmt.Printf("WhatsApp: ContactStatusEvent emitted for call terminate\n")
		default:
		}
	default:
		// Log other events for debugging
		fmt.Printf("WhatsApp: Unhandled event type: %T\n", evt)
	}
}

func (w *WhatsAppProvider) processCallLogRecords(history *waHistorySync.HistorySync) {
	fmt.Printf("WhatsApp: [CALL LOGS] processCallLogRecords called\n")
	if history == nil {
		fmt.Printf("WhatsApp: [CALL LOGS] ERROR - history is nil, skipping\n")
		return
	}

	callLogRecords := history.GetCallLogRecords()
	fmt.Printf("WhatsApp: [CALL LOGS] Found %d call log records in history sync\n", len(callLogRecords))
	if len(callLogRecords) == 0 {
		fmt.Printf("WhatsApp: [CALL LOGS] No call log records found in history sync, returning\n")
		return
	}

	fmt.Printf("WhatsApp: [CALL LOGS] Processing %d call log records from history sync\n", len(callLogRecords))

	for i, record := range callLogRecords {
		fmt.Printf("WhatsApp: [CALL LOGS] Processing record %d/%d\n", i+1, len(callLogRecords))
		if record == nil {
			fmt.Printf("WhatsApp: [CALL LOGS] Record %d is nil, skipping\n", i+1)
			continue
		}

		// Get conversation ID from the record (use GroupJID if available, otherwise CallCreatorJID)
		groupJID := record.GetGroupJID()
		callCreatorJIDStr := record.GetCallCreatorJID()
		callID := record.GetCallID()
		fmt.Printf("WhatsApp: [CALL LOGS] Record %d - CallID: %s, GroupJID: %s, CallCreatorJID: %s\n", i+1, callID, groupJID, callCreatorJIDStr)

		var convID string
		var originalLID string // Store original LID if we resolve it
		if groupJID != "" {
			// Group call - use GroupJID directly
			convID = groupJID
			fmt.Printf("WhatsApp: Using GroupJID %s as conversation ID\n", convID)
		} else if callCreatorJIDStr != "" {
			// Individual call - use CallCreatorJID and resolve LID to JID if needed
			// Use the unified resolveContactID function
			resolvedID, err := w.resolveContactID(callCreatorJIDStr)
			if err != nil {
				fmt.Printf("WhatsApp: WARNING - Could not resolve CallCreator LID %s to JID for call log: %v\n", callCreatorJIDStr, err)
				fmt.Printf("WhatsApp: Skipping this call log - it will be processed when LID mapping is available\n")
				// Skip this call log if we can't resolve it - we need a valid conversation ID
				continue
			} else {
				convID = resolvedID
				// Store original LID if it was different
				if callCreatorJIDStr != resolvedID {
					originalLID = callCreatorJIDStr
					w.storeContactMapping(callCreatorJIDStr, resolvedID)
					fmt.Printf("WhatsApp: Resolved CallCreatorJID %s to conversation ID %s\n", callCreatorJIDStr, convID)
				} else {
					fmt.Printf("WhatsApp: Using CallCreatorJID %s as conversation ID\n", convID)
				}
			}
		} else {
			fmt.Printf("WhatsApp: Call log record without GroupJID or CallCreatorJID, skipping\n")
			continue
		}

		// Get call ID - we'll use this to find the corresponding message
		if callID == "" {
			fmt.Printf("WhatsApp: [CALL LOGS] Record %d has no call ID, skipping\n", i+1)
			continue
		}

		// Extract call information from CallLogRecord
		duration := record.GetDuration()
		isVideo := record.GetIsVideo()
		callResult := record.GetCallResult()
		participants := record.GetParticipants()
		callType := record.GetCallType()

		var durationSecs *int32
		if duration > 0 {
			// Duration is in milliseconds, convert to seconds
			secs := int32(duration / 1000)
			durationSecs = &secs
		}

		durationStr := "N/A"
		if durationSecs != nil {
			durationStr = fmt.Sprintf("%ds", *durationSecs)
		}
		// Determine if it's a group call
		isGroupCall := strings.Contains(convID, "@g.us")

		fmt.Printf("WhatsApp: Processing call log for call %s in conversation %s: duration=%s, isVideo=%v, result=%v, type=%v, participants=%d, isGroup=%v\n",
			callID, convID, durationStr, isVideo, callResult, callType, len(participants), isGroupCall)

		// Find call messages in this conversation that match the call timestamp
		// We'll search for call messages around the start time
		startTime := record.GetStartTime()
		if startTime == 0 {
			fmt.Printf("WhatsApp: Call log record without start time, skipping\n")
			continue
		}

		startTimestamp := time.Unix(startTime/1000, 0)
		// Search window: 5 minutes before and after
		timeWindow := 5 * time.Minute
		startSearch := startTimestamp.Add(-timeWindow)
		endSearch := startTimestamp.Add(timeWindow)
		fmt.Printf("WhatsApp: [CALL LOGS] Record %d - Call start time: %s, search window: %s to %s\n",
			i+1, startTimestamp.Format("2006-01-02 15:04:05"), startSearch.Format("2006-01-02 15:04:05"), endSearch.Format("2006-01-02 15:04:05"))

		// Find and update call messages in the database
		// Search with resolved convID, and also with original LID if we resolved one
		if db.DB != nil {
			var dbMsgs []models.Message
			fmt.Printf("WhatsApp: Searching for call messages in conversation %s between %s and %s\n", convID, startSearch.Format("2006-01-02 15:04:05"), endSearch.Format("2006-01-02 15:04:05"))

			// Build query - search with resolved convID, and also with original LID if available
			query := db.DB.Model(&models.Message{})
			if originalLID != "" {
				query = query.Where("(protocol_conv_id = ? OR protocol_conv_id = ?)", convID, originalLID)
			} else {
				query = query.Where("protocol_conv_id = ?", convID)
			}
			query = query.Where("call_type != '' AND timestamp >= ? AND timestamp <= ?", startSearch, endSearch)

			err := query.Find(&dbMsgs).Error
			if err != nil {
				fmt.Printf("WhatsApp: Failed to find call messages for call %s: %v\n", callID, err)
				continue
			}
			fmt.Printf("WhatsApp: Found %d existing call messages for call %s in conversation %s\n", len(dbMsgs), callID, convID)

			// Update ProtocolConvID to resolved ID if any messages were found with LID
			for i := range dbMsgs {
				if dbMsgs[i].ProtocolConvID != convID {
					fmt.Printf("WhatsApp: Updating ProtocolConvID from %s to %s for call message %s\n", dbMsgs[i].ProtocolConvID, convID, dbMsgs[i].ProtocolMsgID)
					dbMsgs[i].ProtocolConvID = convID
					if err := db.DB.Save(&dbMsgs[i]).Error; err != nil {
						fmt.Printf("WhatsApp: Failed to update ProtocolConvID for call message: %v\n", err)
					}
				}
			}

			if len(dbMsgs) == 0 {
				// No existing call messages found - create one from the call log record
				fmt.Printf("WhatsApp: No call messages found for call %s in conversation %s, creating new message from call log\n", callID, convID)

				// Determine call type based on call log record
				callTypeEnum := record.GetCallType()
				callTypeStr := callTypeEnum.String()

				var callType string
				if callTypeStr != "" && callTypeStr != "UNKNOWN" {
					switch callTypeStr {
					case "REGULAR":
						if isVideo {
							callType = "missed_video"
						} else {
							callType = "missed_voice"
						}
					case "SCHEDULED_CALL":
						callType = "scheduled_start"
					case "VOICE_CHAT":
						callType = "missed_group_voice"
					default:
						if isVideo {
							callType = "missed_video"
						} else {
							callType = "missed_voice"
						}
					}
				} else {
					// Default based on video flag
					if isVideo {
						callType = "missed_video"
					} else {
						callType = "missed_voice"
					}
				}

				// Get sender ID - for group calls use convID, for individual calls use the resolved JID
				senderID := convID
				isGroupCall := strings.Contains(convID, "@g.us")
				// For individual calls, senderID is already the resolved JID (convID)

				// Create call message from call log record
				callMsgID := fmt.Sprintf("call_%s_%d", callID, startTimestamp.Unix())
				callMessage := &models.Message{
					ProtocolConvID:   convID,
					ProtocolMsgID:    callMsgID,
					SenderID:         senderID,
					SenderName:       "",
					Body:             "",
					Timestamp:        startTimestamp,
					IsFromMe:         false,
					CallType:         callType,
					CallIsVideo:      isVideo,
					CallOutcome:      callResult.String(),
					CallDurationSecs: durationSecs,
				}

				// Store participants as JSON array
				if len(participants) > 0 {
					participantJIDs := make([]string, 0, len(participants))
					for _, p := range participants {
						if p != nil {
							if jid := p.GetUserJID(); jid != "" {
								participantJIDs = append(participantJIDs, jid)
							}
						}
					}
					if len(participantJIDs) > 0 {
						participantsJSON, err := json.Marshal(participantJIDs)
						if err == nil {
							callMessage.CallParticipants = string(participantsJSON)
						}
					}
				}

				// Try to get sender name for individual calls
				if !isGroupCall && w.client != nil && w.client.Store != nil && w.client.Store.Contacts != nil {
					senderJID, err := types.ParseJID(senderID)
					if err == nil {
						contact, err := w.client.Store.Contacts.GetContact(w.ctx, senderJID)
						if err == nil && contact.Found {
							if contact.FullName != "" {
								callMessage.SenderName = contact.FullName
							} else if contact.PushName != "" {
								callMessage.SenderName = contact.PushName
							} else if contact.FirstName != "" {
								callMessage.SenderName = contact.FirstName
							}
						}
					}
				}

				// Store the message in cache and database
				// appendMessageToConversation will save to database, but we also save explicitly to ensure it's persisted
				fmt.Printf("WhatsApp: [CALL LOGS] Storing call message in cache for conversation %s\n", convID)
				w.appendMessageToConversation(callMessage)

				// Also save explicitly to database to ensure it's persisted with correct ProtocolConvID
				if db.DB != nil {
					// Ensure ProtocolConvID is set correctly
					callMessage.ProtocolConvID = convID
					fmt.Printf("WhatsApp: [CALL LOGS] Saving call message to database: ProtocolMsgID=%s, ProtocolConvID=%s, CallType=%s\n",
						callMsgID, callMessage.ProtocolConvID, callType)

					// Use upsert to avoid duplicates
					var existingMsg models.Message
					err := db.DB.Where("protocol_msg_id = ?", callMsgID).First(&existingMsg).Error
					if err != nil {
						// Message doesn't exist, create it
						fmt.Printf("WhatsApp: [CALL LOGS] Creating new call message in database\n")
						if err := db.DB.Create(callMessage).Error; err != nil {
							fmt.Printf("WhatsApp: [CALL LOGS] ERROR - Failed to create call message from call log %s: %v\n", callID, err)
						} else {
							durationStr := "N/A"
							if durationSecs != nil {
								durationStr = fmt.Sprintf("%ds", *durationSecs)
							}
							fmt.Printf("WhatsApp: [CALL LOGS] SUCCESS - Created call message %s from call log in conversation %s (duration=%s, outcome=%s, type=%s, senderID=%s, timestamp=%s, ProtocolConvID=%s)\n",
								callMsgID, convID, durationStr, callMessage.CallOutcome, callType, senderID, startTimestamp.Format("2006-01-02 15:04:05"), callMessage.ProtocolConvID)
						}
					} else {
						// Message exists, update it
						fmt.Printf("WhatsApp: [CALL LOGS] Updating existing call message in database\n")
						existingMsg.ProtocolConvID = convID
						existingMsg.CallType = callType
						existingMsg.CallIsVideo = isVideo
						existingMsg.CallOutcome = callResult.String()
						existingMsg.CallDurationSecs = durationSecs
						if callMessage.CallParticipants != "" {
							existingMsg.CallParticipants = callMessage.CallParticipants
						}
						if err := db.DB.Save(&existingMsg).Error; err != nil {
							fmt.Printf("WhatsApp: [CALL LOGS] ERROR - Failed to update call message from call log %s: %v\n", callID, err)
						} else {
							fmt.Printf("WhatsApp: [CALL LOGS] SUCCESS - Updated call message %s from call log in conversation %s\n", callMsgID, convID)
						}
					}
				} else {
					fmt.Printf("WhatsApp: [CALL LOGS] WARNING - Database not available, cannot save call message from call log\n")
				}

				continue
			}

			// Update all matching call messages with summary information
			for i := range dbMsgs {
				dbMsg := &dbMsgs[i]

				// Update message with call summary information
				if durationSecs != nil {
					dbMsg.CallDurationSecs = durationSecs
				}
				dbMsg.CallIsVideo = isVideo

				// Store call outcome as string
				dbMsg.CallOutcome = callResult.String()

				// Store participants as JSON array, resolving LIDs to phone numbers
				if len(participants) > 0 {
					participantJIDs := make([]string, 0, len(participants))
					for _, p := range participants {
						if p != nil {
							// Get user JID from participant info
							if jid := p.GetUserJID(); jid != "" {
								// Resolve LID to phone number if needed
								resolvedJID, err := w.resolveContactID(jid)
								if err == nil {
									participantJIDs = append(participantJIDs, resolvedJID)
									// Store mapping if it was a LID
									if jid != resolvedJID {
										w.storeContactMapping(jid, resolvedJID)
									}
								} else {
									// If resolution failed, use original JID
									participantJIDs = append(participantJIDs, jid)
								}
							}
						}
					}
					if len(participantJIDs) > 0 {
						participantsJSON, err := json.Marshal(participantJIDs)
						if err == nil {
							dbMsg.CallParticipants = string(participantsJSON)
						}
					}
				}

				// Update call type if we have more specific information
				callTypeStr := callType.String()
				if callTypeStr != "" {
					// Map call type from protobuf enum to our string format
					switch callTypeStr {
					case "REGULAR":
						if isVideo {
							dbMsg.CallType = "missed_video"
						} else {
							dbMsg.CallType = "missed_voice"
						}
					case "SCHEDULED_CALL":
						dbMsg.CallType = "scheduled_start"
					case "VOICE_CHAT":
						dbMsg.CallType = "missed_group_voice"
					}
				}

				// Save updated message
				if err := db.DB.Save(dbMsg).Error; err != nil {
					fmt.Printf("WhatsApp: Failed to update call message %s with summary: %v\n", dbMsg.ProtocolMsgID, err)
				} else {
					durationStr := "N/A"
					if durationSecs != nil {
						durationStr = fmt.Sprintf("%ds", *durationSecs)
					}
					fmt.Printf("WhatsApp: Successfully updated call message %s with summary (duration=%s, outcome=%s)\n",
						dbMsg.ProtocolMsgID, durationStr, dbMsg.CallOutcome)
				}
			}
		}
	}
}

func (w *WhatsAppProvider) cacheConversationsFromHistory(history *waHistorySync.HistorySync) {
	if history == nil {
		return
	}

	conversations := history.GetConversations()
	if len(conversations) == 0 {
		fmt.Printf("WhatsApp: History sync contained 0 conversations\n")
		return
	}
	fmt.Printf("WhatsApp: Processing %d conversations from history sync\n", len(conversations))

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.conversations == nil {
		w.conversations = make(map[string]models.LinkedAccount)
	}

	added := 0
	skippedNil := 0
	skippedEmptyID := 0
	skippedInvalidJID := 0

	for i, conv := range conversations {
		if conv == nil {
			skippedNil++
			fmt.Printf("WhatsApp: Conversation[%d] is nil\n", i)
			continue
		}

		jidString := conv.GetID()
		if jidString == "" {
			skippedEmptyID++
			fmt.Printf("WhatsApp: Conversation[%d] has empty ID\n", i)
			continue
		}

				// Note: conv.GetMessages() is always empty during HistorySync
		// Messages are processed separately in the HistorySync event
		// So we should NOT filter based on message count here

		jid, err := types.ParseJID(jidString)
		if err != nil {
			skippedInvalidJID++
			fmt.Printf("WhatsApp: Conversation[%d] has invalid JID %s: %v\n", i, jidString, err)
			continue
		}

		// Get display name - try multiple sources in order of preference
		displayName := ""
		
		// 1. Try whatsmeow's Contact store first (most reliable for names)
		if w.client != nil && w.client.Store != nil && w.client.Store.Contacts != nil {
			if contact, err := w.client.Store.Contacts.GetContact(w.ctx, jid); err == nil && contact.Found {
				// Prefer actual names over phone numbers
				if contact.FullName != "" && !isPhoneNumber(contact.FullName) {
					displayName = contact.FullName
				} else if contact.PushName != "" && !isPhoneNumber(contact.PushName) {
					displayName = contact.PushName
				} else if contact.FirstName != "" && !isPhoneNumber(contact.FirstName) {
					displayName = contact.FirstName
				} else if contact.BusinessName != "" && !isPhoneNumber(contact.BusinessName) {
					displayName = contact.BusinessName
				}
			}
		}
		
		// 2. If Contact store didn't have a name, use conversation name fields
		if displayName == "" {
			convName := conv.GetName()
			if convName != "" && !isPhoneNumber(convName) {
				displayName = convName
			}
		}
		
		// 3. Try DisplayName field as another fallback
		if displayName == "" {
			convDisplayName := conv.GetDisplayName()
			if convDisplayName != "" && !isPhoneNumber(convDisplayName) {
				displayName = convDisplayName
			}
		}
		
		// 4. For phone number JIDs, format the number nicely
		if displayName == "" && jid.Server == types.DefaultUserServer {
			displayName = formatPhoneNumber(jid.User)
		}
		
		// 5. Last resort: use JID user part without formatting (for LIDs)
		if displayName == "" {
			displayName = jid.User
		}

		lastActivity := time.Now()
		if ts := conv.GetConversationTimestamp(); ts > 0 {
			lastActivity = time.Unix(int64(ts), 0)
		}

		// Don't fetch profile pictures synchronously - it blocks and causes rate limiting
		// Avatars will be loaded lazily when needed or in background
		linked := models.LinkedAccount{
			Protocol:  "whatsapp",
			UserID:    jid.String(),
			Username:  displayName,
			AvatarURL: "", // Will be loaded asynchronously if needed
			Status:    "active",
			CreatedAt: lastActivity,
			UpdatedAt: lastActivity,
		}

		w.conversations[linked.UserID] = linked
		fmt.Printf("WhatsApp: Cached conversation[%d]: %s (%s)\n", i, displayName, jid.String())
		
		// Save to database for persistence - use FirstOrCreate to avoid duplicates
		if db.DB != nil {
			var existing models.LinkedAccount
			// Try to find existing conversation
			result := db.DB.Where("user_id = ? AND protocol = ?", linked.UserID, "whatsapp").First(&existing)
			if result.Error == nil {
				// Update existing with new name if it's better (not a phone number)
				if linked.Username != "" && !strings.HasPrefix(linked.Username, "+") {
					existing.Username = linked.Username
					existing.UpdatedAt = linked.UpdatedAt
					if err := db.DB.Save(&existing).Error; err != nil {
						fmt.Printf("WhatsApp: Error updating conversation %s: %v\n", linked.UserID, err)
					}
				}
			} else {
				// Create new
				if err := db.DB.Create(&linked).Error; err != nil {
					fmt.Printf("WhatsApp: Error creating conversation %s: %v\n", linked.UserID, err)
				}
			}
		}

		if jid.Server == types.GroupServer {
			w.knownGroups[linked.UserID] = displayName
			fmt.Printf("WhatsApp: Also cached as group: %s\n", displayName)
		}

		added++
	}

	fmt.Printf("WhatsApp: Cache summary - Added: %d, Skipped (nil): %d, Skipped (empty ID): %d, Skipped (invalid JID): %d\n",
		added, skippedNil, skippedEmptyID, skippedInvalidJID)

	fmt.Printf("WhatsApp: Cached %d conversations from history sync (total cached: %d)\n", added, len(w.conversations))
	if added == 0 {
		fmt.Printf("WhatsApp: WARNING - No conversations were cached despite %d in history sync!\n", len(conversations))
	}
}

func (w *WhatsAppProvider) loadConversationsFromDatabaseLocked() {
	if db.DB == nil {
		return
	}

	var linkedAccounts []models.LinkedAccount
	if err := db.DB.Where("protocol = ?", "whatsapp").Find(&linkedAccounts).Error; err == nil {
		// w.mu is already locked, so we can directly access w.conversations
		if w.conversations == nil {
			w.conversations = make(map[string]models.LinkedAccount)
		}
		for _, acc := range linkedAccounts {
			// Verify avatar file exists before using it
			if acc.AvatarURL != "" {
				if _, err := os.Stat(acc.AvatarURL); err != nil {
					// File doesn't exist, clear the avatar URL
					acc.AvatarURL = ""
				}
			}

			// Only add if not already in cache
			if existing, exists := w.conversations[acc.UserID]; !exists {
				w.conversations[acc.UserID] = acc
			} else {
				// Update if existing entry is missing name or avatar
				if existing.Username == "" || existing.Username == existing.UserID {
					existing.Username = acc.Username
				}
				// Only update avatar if the one from DB exists and is valid
				if existing.AvatarURL == "" && acc.AvatarURL != "" {
					existing.AvatarURL = acc.AvatarURL
				} else if existing.AvatarURL != "" && acc.AvatarURL != "" {
					// Both have avatars, prefer the one from DB if file exists
					if _, err := os.Stat(acc.AvatarURL); err == nil {
						existing.AvatarURL = acc.AvatarURL
					}
				}
				w.conversations[acc.UserID] = existing
			}
		}
		fmt.Printf("WhatsApp: Loaded %d conversations from database\n", len(linkedAccounts))
	}
}
