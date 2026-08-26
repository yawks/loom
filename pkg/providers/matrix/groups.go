package matrix

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"Loom/pkg/models"
)

func (p *Provider) UpdateGroupName(room, name string) error {
	return p.do(noCancel(), http.MethodPut, p.roomPath(room)+"/state/m.room.name", nil, map[string]string{"name": name}, nil)
}
func (p *Provider) UpdateGroupDescription(room, description string) error {
	return p.do(noCancel(), http.MethodPut, p.roomPath(room)+"/state/m.room.topic", nil, map[string]string{"topic": description}, nil)
}
func (p *Provider) UpdateGroupPhoto(room string, photo []byte) error {
	return fmt.Errorf("matrix: group photo update requires MIME metadata not present in Loom's group contract")
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
	return &models.GroupDetails{ConversationID: p.namespacedRoom(room), Name: s.Name, AvatarURL: s.Avatar, IsMember: true, CanSendMessages: true}, nil
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
