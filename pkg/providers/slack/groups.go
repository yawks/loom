package slack

import (
	"Loom/pkg/models"
	"fmt"

	"github.com/slack-go/slack"
)

// CreateGroup creates a new channel (group).
func (p *SlackProvider) CreateGroup(groupName string, participantIDs []string) (*models.Conversation, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	channel, err := p.client.CreateConversation(slack.CreateConversationParams{
		ChannelName: groupName,
		IsPrivate:   false,
	})
	if err != nil {
		return nil, err
	}

	if len(participantIDs) > 0 {
		_, err = p.client.InviteUsersToConversation(channel.ID, participantIDs...)
		if err != nil {
			// Created but failed to invite
		}
	}

	return &models.Conversation{
		ProtocolConvID: channel.ID,
		GroupName:      channel.Name,
		IsGroup:        true,
	}, nil
}

// UpdateGroupName updates the channel name.
func (p *SlackProvider) UpdateGroupName(conversationID string, newName string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	_, err := p.client.RenameConversation(conversationID, newName)
	return err
}

// AddGroupParticipants adds users to a channel.
func (p *SlackProvider) AddGroupParticipants(conversationID string, participantIDs []string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	_, err := p.client.InviteUsersToConversation(conversationID, participantIDs...)
	return err
}

// RemoveGroupParticipants kicks users from a channel.
func (p *SlackProvider) RemoveGroupParticipants(conversationID string, participantIDs []string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	for _, user := range participantIDs {
		err := p.client.KickUserFromConversation(conversationID, user)
		if err != nil {
			return err
		}
	}
	return nil
}

// LeaveGroup leaves a channel.
func (p *SlackProvider) LeaveGroup(conversationID string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	_, err := p.client.LeaveConversation(conversationID)
	return err
}

// PromoteGroupAdmins - Not supported on Slack
func (p *SlackProvider) PromoteGroupAdmins(conversationID string, participantIDs []string) error {
	return fmt.Errorf("not supported on Slack")
}

// DemoteGroupAdmins - Not supported on Slack
func (p *SlackProvider) DemoteGroupAdmins(conversationID string, participantIDs []string) error {
	return fmt.Errorf("not supported on Slack")
}

// GetGroupParticipants returns the list of participants in a group.
// For DMs (conversationID starting with "D"), it extracts participants from conversation info.
// For channels/groups, it uses GetUsersInConversation.
func (p *SlackProvider) GetGroupParticipants(conversationID string) ([]models.GroupParticipant, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	// Check if this is a DM (DM channel IDs start with "D")
	if len(conversationID) > 0 && conversationID[0] == 'D' {
		// For DMs, use conversations.info to get the channel info
		// This includes the user IDs in the conversation
		channelInfo, err := p.client.GetConversationInfo(&slack.GetConversationInfoInput{
			ChannelID:         conversationID,
			IncludeLocale:     false,
			IncludeNumMembers: false,
		})
		if err != nil {
			// If we can't get conversation info, return empty list
			// Participants will be extracted from messages in ConversationDetailsView
			p.log("SlackProvider.GetGroupParticipants: Failed to get conversation info for DM %s: %v\n", conversationID, err)
			return []models.GroupParticipant{}, nil
		}

		// For DMs, channelInfo.User contains the other user's ID
		// We also need to get the current user's ID
		var participants []models.GroupParticipant

		// Get current user ID
		authTest, err := p.client.AuthTest()
		if err == nil && authTest != nil {
			participants = append(participants, models.GroupParticipant{
				UserID:  authTest.UserID,
				IsAdmin: false,
			})
		}

		// Add the other user if available
		if channelInfo.User != "" {
			participants = append(participants, models.GroupParticipant{
				UserID:  channelInfo.User,
				IsAdmin: false,
			})
		}

		return participants, nil
	}

	// For channels/groups, use GetUsersInConversation
	userIDs, _, err := p.client.GetUsersInConversation(&slack.GetUsersInConversationParameters{
		ChannelID: conversationID,
	})
	if err != nil {
		return nil, err
	}

	var participants []models.GroupParticipant
	for _, uid := range userIDs {
		participants = append(participants, models.GroupParticipant{
			UserID:  uid,
			IsAdmin: false,
		})
	}
	return participants, nil
}

// --- Invite Links ---

func (p *SlackProvider) CreateGroupInviteLink(conversationID string) (string, error) {
	return "", fmt.Errorf("not supported on this provider")
}

func (p *SlackProvider) RevokeGroupInviteLink(conversationID string) error {
	return fmt.Errorf("not supported on this provider")
}

func (p *SlackProvider) JoinGroupByInviteLink(inviteLink string) (*models.Conversation, error) {
	return nil, fmt.Errorf("not supported on this provider")
}

func (p *SlackProvider) JoinGroupByInviteMessage(inviteMessageID string) (*models.Conversation, error) {
	return nil, fmt.Errorf("not supported on this provider")
}
