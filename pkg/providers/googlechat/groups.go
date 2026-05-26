package googlechat

import (
	"fmt"

	"Loom/pkg/models"
)

// Google Chat does not expose group management via the internal API used here.
// All methods return "not supported".

func (p *GoogleChatProvider) CreateGroup(groupName string, participantIDs []string) (*models.Conversation, error) {
	return nil, fmt.Errorf("googlechat: CreateGroup not supported")
}

func (p *GoogleChatProvider) UpdateGroupName(convID, newName string) error {
	return fmt.Errorf("googlechat: UpdateGroupName not supported")
}

func (p *GoogleChatProvider) AddGroupParticipants(convID string, participantIDs []string) error {
	return fmt.Errorf("googlechat: AddGroupParticipants not supported")
}

func (p *GoogleChatProvider) RemoveGroupParticipants(convID string, participantIDs []string) error {
	return fmt.Errorf("googlechat: RemoveGroupParticipants not supported")
}

func (p *GoogleChatProvider) LeaveGroup(convID string) error {
	return fmt.Errorf("googlechat: LeaveGroup not supported")
}

func (p *GoogleChatProvider) PromoteGroupAdmins(convID string, participantIDs []string) error {
	return fmt.Errorf("googlechat: PromoteGroupAdmins not supported")
}

func (p *GoogleChatProvider) DemoteGroupAdmins(convID string, participantIDs []string) error {
	return fmt.Errorf("googlechat: DemoteGroupAdmins not supported")
}

func (p *GoogleChatProvider) GetGroupParticipants(convID string) ([]models.GroupParticipant, error) {
	return nil, fmt.Errorf("googlechat: GetGroupParticipants not supported")
}

func (p *GoogleChatProvider) CreateGroupInviteLink(convID string) (string, error) {
	return "", fmt.Errorf("googlechat: CreateGroupInviteLink not supported")
}

func (p *GoogleChatProvider) RevokeGroupInviteLink(convID string) error {
	return fmt.Errorf("googlechat: RevokeGroupInviteLink not supported")
}

func (p *GoogleChatProvider) JoinGroupByInviteLink(inviteLink string) (*models.Conversation, error) {
	return nil, fmt.Errorf("googlechat: JoinGroupByInviteLink not supported")
}

func (p *GoogleChatProvider) JoinGroupByInviteMessage(inviteMsgID string) (*models.Conversation, error) {
	return nil, fmt.Errorf("googlechat: JoinGroupByInviteMessage not supported")
}

