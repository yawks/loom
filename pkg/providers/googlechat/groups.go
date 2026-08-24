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
	memberships := make([]Membership, 0, len(participantIDs))
	for _, participantID := range participantIDs {
		participantID = strings.TrimPrefix(strings.TrimSpace(participantID), "users/")
		if participantID == "" {
			continue
		}
		memberships = append(memberships, Membership{Member: &ChatUser{Name: "users/" + participantID, Type: "HUMAN"}})
	}
	if len(memberships) == 0 {
		return nil, fmt.Errorf("googlechat: at least one participant is required")
	}
	request := struct {
		Space       Space        `json:"space"`
		Memberships []Membership `json:"memberships"`
	}{Space: Space{SpaceType: "SPACE", DisplayName: strings.TrimSpace(groupName)}, Memberships: memberships}
	var space Space
	if err := p.apiPost("/spaces:setup", request, &space); err != nil {
		return nil, fmt.Errorf("googlechat: create group: %w", err)
	}
	return &models.Conversation{ProtocolConvID: core.BuildConvID(p.getInstanceID(), space.Name), IsGroup: true, ConversationType: "group", GroupName: space.DisplayName}, nil
}

func (p *GoogleChatProvider) UpdateGroupName(convID, newName string) error {
	spaceName := strings.TrimPrefix(core.StripConvID(convID), "/")
	if !strings.HasPrefix(spaceName, "spaces/") {
		return fmt.Errorf("googlechat: invalid space ID %q", convID)
	}
	var updated Space
	if err := p.apiPatch("/"+spaceName, "displayName", Space{DisplayName: strings.TrimSpace(newName)}, &updated); err != nil {
		return fmt.Errorf("googlechat: update group name: %w", err)
	}
	return nil
}

func (p *GoogleChatProvider) GetGroupDetails(convID string) (*models.GroupDetails, error) {
	spaceName := strings.TrimPrefix(core.StripConvID(convID), "/")
	if !strings.HasPrefix(spaceName, "spaces/") {
		return nil, fmt.Errorf("googlechat: invalid space ID %q", convID)
	}
	var space Space
	if err := p.apiGet("/"+spaceName, nil, &space); err != nil {
		return nil, fmt.Errorf("googlechat: get group details: %w", err)
	}
	canSendMessages := false
	isMember := false
	if selfID := p.getSelfID(); selfID != "" {
		if membership, membershipErr := p.membershipForUser(spaceName, selfID); membershipErr == nil {
			isMember = membership.State == "JOINED" || membership.State == ""
			canSendMessages = isMember
		}
	}
	return &models.GroupDetails{ConversationID: core.BuildConvID(p.getInstanceID(), spaceName), Name: space.DisplayName, IsMember: isMember, CanSendMessages: canSendMessages}, nil
}

func (p *GoogleChatProvider) UpdateGroupDescription(string, string) error {
	return fmt.Errorf("googlechat: group descriptions are not supported")
}

func (p *GoogleChatProvider) UpdateGroupPhoto(string, []byte) error {
	return fmt.Errorf("googlechat: custom group photos are not supported")
}

func (p *GoogleChatProvider) AddGroupParticipants(convID string, participantIDs []string) error {
	spaceName, err := googleChatSpaceName(convID)
	if err != nil {
		return err
	}
	for _, participantID := range participantIDs {
		participantID = strings.TrimPrefix(strings.TrimSpace(participantID), "users/")
		if participantID == "" {
			continue
		}
		var membership Membership
		payload := Membership{Member: &ChatUser{Name: "users/" + participantID, Type: "HUMAN"}}
		if err := p.apiPost("/"+spaceName+"/members", payload, &membership); err != nil {
			return fmt.Errorf("googlechat: add participant %s: %w", participantID, err)
		}
	}
	return nil
}

func (p *GoogleChatProvider) RemoveGroupParticipants(convID string, participantIDs []string) error {
	spaceName, err := googleChatSpaceName(convID)
	if err != nil {
		return err
	}
	for _, participantID := range participantIDs {
		membership, err := p.membershipForUser(spaceName, participantID)
		if err != nil {
			return err
		}
		if err := p.apiDelete("/" + strings.TrimPrefix(membership.Name, "/")); err != nil {
			return fmt.Errorf("googlechat: remove participant %s: %w", participantID, err)
		}
	}
	return nil
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
	return p.updateGroupRoles(convID, participantIDs, "ROLE_ASSISTANT_MANAGER")
}

func (p *GoogleChatProvider) DemoteGroupAdmins(convID string, participantIDs []string) error {
	return p.updateGroupRoles(convID, participantIDs, "ROLE_MEMBER")
}

func googleChatSpaceName(convID string) (string, error) {
	spaceName := strings.TrimPrefix(core.StripConvID(convID), "/")
	if !strings.HasPrefix(spaceName, "spaces/") {
		return "", fmt.Errorf("googlechat: invalid space ID %q", convID)
	}
	return spaceName, nil
}

func (p *GoogleChatProvider) membershipForUser(spaceName, userID string) (*Membership, error) {
	userID = strings.TrimPrefix(userID, "users/")
	pageToken := ""
	for {
		params := url.Values{"pageSize": {"100"}}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}
		var response MemberListResponse
		if err := p.apiGet("/"+spaceName+"/members", params, &response); err != nil {
			return nil, fmt.Errorf("googlechat: list memberships: %w", err)
		}
		for i := range response.Memberships {
			membership := &response.Memberships[i]
			if membership.Member != nil && strings.TrimPrefix(membership.Member.Name, "users/") == userID {
				return membership, nil
			}
		}
		if response.NextPageToken == "" {
			break
		}
		pageToken = response.NextPageToken
	}
	return nil, fmt.Errorf("googlechat: user %s is not a member of %s", userID, spaceName)
}

func (p *GoogleChatProvider) updateGroupRoles(convID string, participantIDs []string, role string) error {
	spaceName, err := googleChatSpaceName(convID)
	if err != nil {
		return err
	}
	for _, participantID := range participantIDs {
		membership, err := p.membershipForUser(spaceName, participantID)
		if err != nil {
			return err
		}
		membership.Role = role
		var updated Membership
		if err := p.apiPatch("/"+strings.TrimPrefix(membership.Name, "/"), "role", membership, &updated); err != nil {
			return fmt.Errorf("googlechat: update participant role %s: %w", participantID, err)
		}
	}
	return nil
}

func (p *GoogleChatProvider) GetGroupParticipants(convID string) ([]models.GroupParticipant, error) {
	spaceName := strings.TrimPrefix(core.StripConvID(convID), "/")
	if !strings.HasPrefix(spaceName, "spaces/") {
		return nil, fmt.Errorf("googlechat: invalid space ID %q", convID)
	}
	selfID := p.getSelfID()
	participants := make([]models.GroupParticipant, 0)
	pageToken := ""
	for {
		params := url.Values{"pageSize": {"100"}}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}
		var response MemberListResponse
		if err := p.apiGet("/"+spaceName+"/members", params, &response); err != nil {
			return nil, fmt.Errorf("googlechat: list group participants: %w", err)
		}
		for _, membership := range response.Memberships {
			if membership.Member == nil || membership.Member.Name == "" {
				continue
			}
			userID := strings.TrimPrefix(membership.Member.Name, "users/")
			isAdmin := membership.Role == "ROLE_MANAGER" || membership.Role == "ROLE_ASSISTANT_MANAGER"
			participants = append(participants, models.GroupParticipant{UserID: userID, IsSelf: userID == selfID, IsAdmin: isAdmin})
		}
		if response.NextPageToken == "" {
			break
		}
		pageToken = response.NextPageToken
	}
	return participants, nil
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
