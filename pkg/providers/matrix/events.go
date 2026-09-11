package matrix

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
)

func noCancel() context.Context { return context.Background() }

func (p *Provider) syncLoop(ctx context.Context) {
	for ctx.Err() == nil {
		p.mu.RLock()
		since := p.nextBatch
		p.mu.RUnlock()
		q := url.Values{"timeout": {"30000"}}
		// An unfiltered initial /sync can return the complete state and timeline of
		// every room on every startup. Keep it bounded; explicit history sync owns
		// the deeper message import.
		q.Set("filter", `{"room":{"timeline":{"limit":20},"state":{"lazy_load_members":true}}}`)
		if since != "" {
			q.Set("since", since)
		}
		var response struct {
			NextBatch string `json:"next_batch"`
			Rooms     struct {
				Join map[string]struct {
					State struct {
						Events []matrixEvent `json:"events"`
					} `json:"state"`
					Timeline struct {
						Events []matrixEvent `json:"events"`
					} `json:"timeline"`
					Ephemeral struct {
						Events []matrixEvent `json:"events"`
					} `json:"ephemeral"`
				} `json:"join"`
			} `json:"rooms"`
		}
		if err := p.do(ctx, http.MethodGet, "/sync", q, nil, &response); err != nil {
			if ctx.Err() != nil {
				return
			}
			time.Sleep(2 * time.Second)
			continue
		}
		initial := since == ""
		p.mu.Lock()
		p.nextBatch = response.NextBatch
		p.mu.Unlock()
		avatarRooms := make(map[string][][]matrixEvent)
		for roomID, room := range response.Rooms.Join {
			avatarRooms[roomID] = [][]matrixEvent{room.State.Events, room.Timeline.Events}
			roomMessages := make([]models.Message, 0, len(room.Timeline.Events))
			for _, event := range room.Timeline.Events {
				p.mu.Lock()
				p.eventRooms[event.EventID] = roomID
				p.mu.Unlock()
				if event.Type == "m.reaction" {
					var c struct {
						Rel struct {
							EventID string `json:"event_id"`
							Key     string `json:"key"`
						} `json:"m.relates_to"`
					}
					if json.Unmarshal(event.Content, &c) == nil {
						p.mu.Lock()
						p.reactionEvents[reactionKey(roomID, c.Rel.EventID, c.Rel.Key, event.Sender)] = event.EventID
						p.mu.Unlock()
						p.emit(core.ReactionEvent{InstanceID: p.getInstanceID(), ConversationID: p.namespacedRoom(roomID), MessageID: c.Rel.EventID, UserID: event.Sender, Emoji: c.Rel.Key, Added: true, Timestamp: event.OriginServerTS / 1000})
					}
					continue
				}
				if message, ok := p.eventToMessage(roomID, event); ok {
					roomMessages = append(roomMessages, message)
					if !initial {
						p.emit(core.MessageEvent{InstanceID: p.getInstanceID(), Message: message})
					}
				}
			}
			p.storeMessages(roomID, roomMessages)
			for _, event := range room.Ephemeral.Events {
				if event.Type == "m.typing" {
					var c struct {
						UserIDs []string `json:"user_ids"`
					}
					if json.Unmarshal(event.Content, &c) == nil {
						for _, user := range c.UserIDs {
							if user != p.CurrentUserID() {
								p.emit(core.TypingEvent{InstanceID: p.getInstanceID(), ConversationID: p.namespacedRoom(roomID), UserID: user, UserName: user, IsTyping: true})
							}
						}
					}
				}
			}
		}
		p.updateRoomAvatars(ctx, avatarRooms)
	}
}

func (p *Provider) SyncHistory(since time.Time) error {
	instanceID := p.getInstanceID()
	p.emit(core.SyncStatusEvent{InstanceID: instanceID, Status: core.SyncStatusFetchingContacts, Message: "Fetching Matrix rooms", Progress: -1})
	contacts, err := p.GetContacts()
	if err != nil {
		return err
	}
	for index, contact := range contacts {
		progress := 90
		if len(contacts) > 0 {
			progress = 10 + (index * 80 / len(contacts))
		}
		p.emit(core.SyncStatusEvent{InstanceID: instanceID, Status: core.SyncStatusFetchingHistory, ConversationID: contact.ConversationID, Message: "Synchronizing Matrix history", Progress: progress})
		selfID := p.CurrentUserID()
		activityAt := db.LatestOwnActivityAt(contact.ConversationID, selfID)
		messages, e := p.GetConversationHistory(contact.ConversationID, 100, nil, &since)
		if e != nil {
			continue
		}
		if len(messages) > 0 {
			read, unread := core.SplitRecoveredMessagesAtOwnActivity(messages, selfID, activityAt)
			if len(read) > 0 {
				p.emit(core.MessageBatchEvent{InstanceID: p.getInstanceID(), ConversationID: contact.ConversationID, Messages: read, ForceRead: true})
			}
			if len(unread) > 0 {
				p.emit(core.MessageBatchEvent{InstanceID: p.getInstanceID(), ConversationID: contact.ConversationID, Messages: unread, ForceUnread: true})
			}
		}
		if readThrough := db.MessagesReadThrough(contact.ConversationID, activityAt, 1000); len(readThrough) > 0 {
			p.emit(core.MessageBatchEvent{InstanceID: p.getInstanceID(), ConversationID: contact.ConversationID, Messages: readThrough, ForceRead: true})
		}
	}
	p.emit(core.SyncStatusEvent{InstanceID: instanceID, Status: core.SyncStatusCompleted, Message: "Matrix synchronization complete", Progress: 100})
	return nil
}
