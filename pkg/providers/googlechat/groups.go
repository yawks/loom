package googlechat

import (
	"fmt"
	"net/url"
	"strings"

	"Loom/pkg/core"
	"Loom/pkg/models"
)

// Most Google Chat group management operations are not exposed yet. Leaving a
// space is supported through the official memberships REST API.

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
	spaceName := strings.TrimPrefix(core.StripConvID(convID), "/")
	if !strings.HasPrefix(spaceName, "spaces/") {
		return fmt.Errorf("googlechat: invalid space ID %q", convID)
	}
	if !p.isGroupSpace(spaceName) {
		return fmt.Errorf("googlechat: conversation is not a group")
	}

	membershipName, err := p.selfMembershipName(spaceName)
	if err != nil {
		return err
	}
	if err := p.apiDelete("/" + membershipName); err != nil {
		return fmt.Errorf("googlechat: leave space: %w", err)
	}
	return nil
}

func (p *GoogleChatProvider) selfMembershipName(spaceName string) (string, error) {
	selfID := p.getSelfID()
	if selfID == "" {
		return "", fmt.Errorf("googlechat: current user is unknown")
	}

	pageToken := ""
	for {
		params := url.Values{"pageSize": {"100"}}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}
		var response MemberListResponse
		if err := p.apiGet("/"+spaceName+"/members", params, &response); err != nil {
			return "", fmt.Errorf("googlechat: list space memberships: %w", err)
		}
		for _, membership := range response.Memberships {
			if membership.Member != nil && strings.TrimPrefix(membership.Member.Name, "users/") == selfID {
				if membership.Name == "" {
					return "", fmt.Errorf("googlechat: current membership has no resource name")
				}
				return strings.TrimPrefix(membership.Name, "/"), nil
			}
		}
		if response.NextPageToken == "" {
			break
		}
		pageToken = response.NextPageToken
	}
	return "", fmt.Errorf("googlechat: current user is not a member of %s", spaceName)
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
