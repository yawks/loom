package matrix

import (
	"Loom/pkg/db"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"Loom/pkg/core"
	"Loom/pkg/models"
)

func (p *Provider) ListConversationInvitations() ([]models.ConversationInvitation, error) {
	q := url.Values{"timeout": {"0"}, "filter": {`{"room":{"timeline":{"limit":0}}}`}}
	var response struct {
		Rooms struct {
			Invite map[string]struct {
				InviteState struct {
					Events []matrixEvent `json:"events"`
				} `json:"invite_state"`
			} `json:"invite"`
		} `json:"rooms"`
	}
	if err := p.do(context.Background(), http.MethodGet, "/sync", q, nil, &response); err != nil {
		return nil, err
	}
	invitations := make([]models.ConversationInvitation, 0, len(response.Rooms.Invite))
	for roomID, room := range response.Rooms.Invite {
		summary := summarizeRoomEvents(p, room.InviteState.Events)
		invitation := models.ConversationInvitation{ConversationID: p.namespacedRoom(roomID), ProviderInstanceID: p.getInstanceID(), Name: summary.Name, AvatarURL: summary.Avatar}
		memberNames := make(map[string]string)
		for _, event := range room.InviteState.Events {
			if event.Type != "m.room.member" || event.StateKey == nil {
				continue
			}
			var content struct {
				Membership  string `json:"membership"`
				DisplayName string `json:"displayname"`
			}
			if json.Unmarshal(event.Content, &content) != nil {
				continue
			}
			if content.DisplayName != "" {
				memberNames[*event.StateKey] = content.DisplayName
			}
			if *event.StateKey == p.CurrentUserID() && content.Membership == "invite" {
				invitation.InvitedByID = event.Sender
			}
		}
		invitation.InvitedByName = memberNames[invitation.InvitedByID]
		if invitation.Name == "" {
			invitation.Name = roomID
		}
		invitations = append(invitations, invitation)
	}
	sort.Slice(invitations, func(i, j int) bool { return invitations[i].ConversationID < invitations[j].ConversationID })
	return invitations, nil
}

func (p *Provider) AcceptConversationInvitation(conversationID string) error {
	roomID := core.StripConvID(conversationID)
	if err := p.do(noCancel(), http.MethodPost, "/join/"+url.PathEscape(roomID), nil, map[string]any{}, nil); err != nil {
		return fmt.Errorf("matrix: accept room invitation: %w", err)
	}
	// Persist this room directly. Some homeservers do not expose a freshly joined
	// room through /joined_rooms immediately, which made a successful acceptance
	// look like a no-op until the next full synchronization.
	summary, err := p.roomState(roomID)
	if err != nil {
		return fmt.Errorf("matrix: load accepted room: %w", err)
	}
	p.persistRoom(p.accountForRoom(roomID, summary), roomID)
	return nil
}

func (p *Provider) DeclineConversationInvitation(conversationID string) error {
	if err := p.do(noCancel(), http.MethodPost, p.roomPath(conversationID)+"/leave", nil, map[string]any{}, nil); err != nil {
		return fmt.Errorf("matrix: decline room invitation: %w", err)
	}
	return nil
}

func (p *Provider) UpdateGroupName(room, name string) error {
	return p.do(noCancel(), http.MethodPut, p.roomPath(room)+"/state/m.room.name", nil, map[string]string{"name": name}, nil)
}
func (p *Provider) UpdateGroupDescription(room, description string) error {
	return p.do(noCancel(), http.MethodPut, p.roomPath(room)+"/state/m.room.topic", nil, map[string]string{"topic": description}, nil)
}
func (p *Provider) UpdateGroupPhoto(room string, photo []byte) error {
	mimeType := http.DetectContentType(photo)
	if !strings.HasPrefix(mimeType, "image/") || len(photo) > 4<<20 {
		return fmt.Errorf("matrix: invalid avatar image (maximum 4 MiB)")
	}
	uri, err := p.upload(&core.Attachment{Data: photo, MimeType: mimeType, FileName: "avatar"})
	if err != nil {
		return err
	}
	if err := p.do(noCancel(), http.MethodPut, p.roomPath(room)+"/state/m.room.avatar", nil, map[string]string{"url": uri}, nil); err != nil {
		return err
	}
	if db.DB != nil {
		return db.UpdateConversationAvatars(p.getInstanceID(), map[string]string{p.namespacedRoom(room): "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(photo)})
	}
	return nil
}
func (p *Provider) AddGroupParticipants(room string, ids []string) error {
	for _, id := range ids {
		if err := p.do(noCancel(), http.MethodPost, p.roomPath(room)+"/invite", nil, map[string]string{"user_id": id}, nil); err != nil {
			return err
		}
	}
	return nil
}
func (p *Provider) RemoveGroupParticipants(room string, ids []string) error {
	for _, id := range ids {
		if err := p.do(noCancel(), http.MethodPost, p.roomPath(room)+"/kick", nil, map[string]string{"user_id": id}, nil); err != nil {
			return err
		}
	}
	return nil
}
func (p *Provider) LeaveGroup(room string) error {
	return p.do(noCancel(), http.MethodPost, p.roomPath(room)+"/leave", nil, map[string]string{}, nil)
}
func (p *Provider) GetGroupParticipants(room string) ([]models.GroupParticipant, error) {
	var events []matrixEvent
	if err := p.do(noCancel(), http.MethodGet, p.roomPath(room)+"/members", nil, nil, &struct {
		Chunk *[]matrixEvent `json:"chunk"`
	}{Chunk: &events}); err != nil {
		return nil, err
	}
	out := []models.GroupParticipant{}
	for _, e := range events {
		if e.StateKey == nil {
			continue
		}
		var c struct {
			Membership string `json:"membership"`
		}
		_ = json.Unmarshal(e.Content, &c)
		if c.Membership == "join" {
			out = append(out, models.GroupParticipant{UserID: *e.StateKey, IsSelf: *e.StateKey == p.CurrentUserID()})
		}
	}
	return out, nil
}
func (p *Provider) PromoteGroupAdmins(string, []string) error {
	return fmt.Errorf("matrix: power-level administration is not exposed by this provider")
}
func (p *Provider) DemoteGroupAdmins(string, []string) error {
	return fmt.Errorf("matrix: power-level administration is not exposed by this provider")
}
func (p *Provider) GetGroupDetails(room string) (*models.GroupDetails, error) {
	s, err := p.roomState(room)
	if err != nil {
		return nil, err
	}
	member, name, topic, avatar := roomMetadataPermissions(s.Events, p.CurrentUserID())
	return &models.GroupDetails{ConversationID: p.namespacedRoom(room), Name: s.Name, Description: s.Description, AvatarURL: s.Avatar, IsMember: member, CanSendMessages: member, CanEditName: &name, CanEditDescription: &topic, CanEditPhoto: &avatar}, nil
}
func (p *Provider) CreateGroupInviteLink(string) (string, error) {
	return "", fmt.Errorf("matrix: invite links are not supported")
}
func (p *Provider) RevokeGroupInviteLink(string) error {
	return fmt.Errorf("matrix: invite links are not supported")
}
func (p *Provider) JoinGroupByInviteLink(string) (*models.Conversation, error) {
	return nil, fmt.Errorf("matrix: invite links are not supported")
}
func (p *Provider) JoinGroupByInviteMessage(string) (*models.Conversation, error) {
	return nil, fmt.Errorf("matrix: invite messages are not supported")
}

var _ = url.PathEscape

// Matrix permissions are normalized here; the UI never interprets power levels.
func roomMetadataPermissions(events []matrixEvent, self string) (member, name, topic, avatar bool) {
	var powers map[string]json.RawMessage
	creator, version := "", 1
	creators := []string{}
	for _, e := range events {
		if e.StateKey == nil {
			continue
		}
		switch e.Type {
		case "m.room.member":
			if *e.StateKey == self {
				var c struct{ Membership string }
				if json.Unmarshal(e.Content, &c) == nil {
					member = c.Membership == "join"
				}
			}
		case "m.room.create":
			var c struct {
				Creator            string
				RoomVersion        string   `json:"room_version"`
				AdditionalCreators []string `json:"additional_creators"`
			}
			if json.Unmarshal(e.Content, &c) != nil {
				return false, false, false, false
			}
			creator = c.Creator
			if creator == "" {
				creator = e.Sender
			}
			if c.RoomVersion != "" {
				version, _ = strconv.Atoi(c.RoomVersion)
			}
			creators = c.AdditionalCreators
		case "m.room.power_levels":
			if json.Unmarshal(e.Content, &powers) != nil {
				return member, false, false, false
			}
		}
	}
	level := func(raw json.RawMessage, fallback int64) int64 {
		if len(raw) == 0 {
			return fallback
		}
		n, err := strconv.ParseInt(strings.Trim(string(raw), "\""), 10, 64)
		if err != nil {
			return fallback
		}
		return n
	}
	var users, required map[string]json.RawMessage
	_ = json.Unmarshal(powers["users"], &users)
	_ = json.Unmarshal(powers["events"], &required)
	own := level(users[self], level(powers["users_default"], 0))
	isCreator := self != "" && self == creator
	if version >= 12 {
		for _, id := range creators {
			isCreator = isCreator || self == id
		}
	}
	if powers == nil && isCreator {
		own = 100
	}
	can := func(event string) bool {
		return member && ((version >= 12 && isCreator) || own >= level(required[event], level(powers["state_default"], 50)))
	}
	return member, can("m.room.name"), can("m.room.topic"), can("m.room.avatar")
}
