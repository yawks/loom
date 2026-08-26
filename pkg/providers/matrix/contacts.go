package matrix

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"Loom/pkg/db"
	"Loom/pkg/models"
)

type roomSummary struct {
	Name     string
	Avatar   string
	Members  []string
	IsDirect bool
}

func (p *Provider) joinedRooms() ([]string, error) {
	var out struct {
		JoinedRooms []string `json:"joined_rooms"`
	}
	if err := p.do(noCancel(), http.MethodGet, "/joined_rooms", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.JoinedRooms, nil
}
func (p *Provider) roomState(room string) (roomSummary, error) {
	var events []matrixEvent
	if err := p.do(noCancel(), http.MethodGet, p.roomPath(room)+"/state", nil, nil, &events); err != nil {
		return roomSummary{}, err
	}
	s := roomSummary{}
	p.mu.RLock()
	self := p.userID
	p.mu.RUnlock()
	for _, e := range events {
		switch e.Type {
		case "m.room.name":
			var c struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(e.Content, &c)
			s.Name = c.Name
		case "m.room.avatar":
			var c struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal(e.Content, &c)
			s.Avatar = p.mediaURL(c.URL)
		case "m.room.member":
			if e.StateKey != nil {
				var c struct {
					Membership  string `json:"membership"`
					DisplayName string `json:"displayname"`
					AvatarURL   string `json:"avatar_url"`
				}
				_ = json.Unmarshal(e.Content, &c)
				if c.Membership == "join" {
					s.Members = append(s.Members, *e.StateKey)
					if *e.StateKey != self && s.Name == "" {
						s.Name = c.DisplayName
						s.Avatar = p.mediaURL(c.AvatarURL)
					}
				}
			}
		}
	}
	s.IsDirect = len(s.Members) == 2
	if s.Name == "" {
		s.Name = room
	}
	return s, nil
}
func (p *Provider) GetContacts() ([]models.LinkedAccount, error) {
	rooms, err := p.joinedRooms()
	if err != nil {
		return nil, err
	}
	out := make([]models.LinkedAccount, 0, len(rooms))
	for _, room := range rooms {
		s, e := p.roomState(room)
		if e != nil {
			continue
		}
		userID := room
		if s.IsDirect {
			for _, member := range s.Members {
				if member != p.CurrentUserID() {
					userID = member
					break
				}
			}
		}
		account := models.LinkedAccount{Protocol: "matrix", ProviderInstanceID: p.getInstanceID(), UserID: userID, Username: s.Name, AvatarURL: s.Avatar, IsGroup: !s.IsDirect, ConversationID: p.namespacedRoom(room)}
		out = append(out, account)
		p.persistRoom(account, room)
	}
	return out, nil
}

func (p *Provider) persistRoom(account models.LinkedAccount, roomID string) {
	if db.DB == nil {
		return
	}
	var linked models.LinkedAccount
	db.DB.Where("provider_instance_id = ? AND user_id = ?", p.getInstanceID(), account.UserID).First(&linked)
	if linked.ID == 0 {
		meta := models.MetaContact{DisplayName: account.Username, AvatarURL: account.AvatarURL}
		if db.DB.Create(&meta).Error != nil {
			return
		}
		linked = account
		linked.MetaContactID = meta.ID
		if db.DB.Create(&linked).Error != nil {
			return
		}
		db.ContactStore.UpsertMetaContact(meta)
		db.ContactStore.UpsertLinkedAccount(linked)
	} else if linked.Username != account.Username || linked.AvatarURL != account.AvatarURL || linked.IsGroup != account.IsGroup {
		linked.Username, linked.AvatarURL, linked.IsGroup = account.Username, account.AvatarURL, account.IsGroup
		db.DB.Save(&linked)
		db.ContactStore.UpsertLinkedAccount(linked)
		var meta models.MetaContact
		if db.DB.First(&meta, linked.MetaContactID).Error == nil && (meta.DisplayName != account.Username || meta.AvatarURL != account.AvatarURL) {
			meta.DisplayName, meta.AvatarURL = account.Username, account.AvatarURL
			db.DB.Save(&meta)
			db.ContactStore.UpsertMetaContact(meta)
		}
	}
	namespaced := p.namespacedRoom(roomID)
	var conversation models.Conversation
	db.DB.Where("protocol_conv_id = ?", namespaced).First(&conversation)
	groupName := ""
	if account.IsGroup {
		groupName = account.Username
	}
	if conversation.ID == 0 {
		conversation = models.Conversation{LinkedAccountID: linked.ID, ProtocolConvID: namespaced, IsGroup: account.IsGroup, GroupName: groupName}
		if db.DB.Create(&conversation).Error != nil {
			return
		}
	} else {
		conversation.LinkedAccountID, conversation.IsGroup, conversation.GroupName = linked.ID, account.IsGroup, groupName
		db.DB.Save(&conversation)
	}
	db.ContactStore.SetConversation(linked.ID, namespaced)
}
func (p *Provider) GetContactName(id string) (string, error) {
	var out struct {
		DisplayName string `json:"displayname"`
	}
	if err := p.do(noCancel(), http.MethodGet, "/profile/"+url.PathEscape(id)+"/displayname", nil, nil, &out); err != nil {
		return id, err
	}
	if out.DisplayName == "" {
		return id, nil
	}
	return out.DisplayName, nil
}
func (p *Provider) RefreshContact(string) error { return nil }
func (p *Provider) SearchContacts(query string) ([]models.LinkedAccount, error) {
	var out struct {
		Results []struct {
			UserID      string `json:"user_id"`
			DisplayName string `json:"display_name"`
			AvatarURL   string `json:"avatar_url"`
		} `json:"results"`
	}
	if err := p.do(noCancel(), http.MethodPost, "/user_directory/search", nil, map[string]any{"search_term": query, "limit": 50}, &out); err != nil {
		return nil, err
	}
	items := make([]models.LinkedAccount, 0, len(out.Results))
	for _, r := range out.Results {
		items = append(items, models.LinkedAccount{Protocol: "matrix", ProviderInstanceID: p.getInstanceID(), UserID: r.UserID, Username: r.DisplayName, AvatarURL: p.mediaURL(r.AvatarURL)})
	}
	return items, nil
}
func (p *Provider) CreateDirectConversation(userID string) (*models.Conversation, error) {
	return p.CreateConversation("direct", "", []string{userID})
}
func (p *Provider) CreateConversation(kind, title string, participants []string) (*models.Conversation, error) {
	direct := kind == "direct"
	payload := map[string]any{"invite": participants, "is_direct": direct, "preset": "private_chat"}
	if strings.TrimSpace(title) != "" {
		payload["name"] = strings.TrimSpace(title)
	}
	var out struct {
		RoomID string `json:"room_id"`
	}
	if err := p.do(noCancel(), http.MethodPost, "/createRoom", nil, payload, &out); err != nil {
		return nil, err
	}
	return &models.Conversation{ProtocolConvID: p.namespacedRoom(out.RoomID), IsGroup: !direct, ConversationType: kind, GroupName: title}, nil
}
func (p *Provider) CreateGroup(name string, participants []string) (*models.Conversation, error) {
	if len(participants) == 0 {
		return nil, fmt.Errorf("matrix: at least one participant is required")
	}
	return p.CreateConversation("group", name, participants)
}
