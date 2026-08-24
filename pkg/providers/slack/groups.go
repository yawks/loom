package slack

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	"fmt"
	"strings"
	"unicode"

	"github.com/slack-go/slack"
	"golang.org/x/text/unicode/norm"
)

const slackChannelNameMaxLength = 80

func normalizeSlackChannelName(name string) string {
	var normalized strings.Builder
	separatorPending := false
	for _, r := range norm.NFD.String(strings.ToLower(strings.TrimSpace(name))) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if separatorPending && normalized.Len() > 0 && normalized.Len() < slackChannelNameMaxLength {
				normalized.WriteByte('-')
			}
			separatorPending = false
			if normalized.Len() < slackChannelNameMaxLength {
				normalized.WriteRune(r)
			}
			continue
		}
		if r == '_' {
			if normalized.Len() > 0 && normalized.Len() < slackChannelNameMaxLength {
				normalized.WriteRune(r)
			}
			separatorPending = false
			continue
		}
		separatorPending = normalized.Len() > 0
	}
	return strings.TrimRight(normalized.String(), "-_")
}

// CreateConversation supports Slack's distinct multi-person DM and channel
// concepts while keeping the generic provider API provider-neutral.
func (p *SlackProvider) CreateConversation(conversationType, title string, participantIDs []string) (*models.Conversation, error) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}
	if conversationType == "group_message" {
		channel, _, _, err := client.OpenConversation(&slack.OpenConversationParameters{Users: participantIDs})
		if err != nil {
			return nil, err
		}
		return &models.Conversation{ProtocolConvID: core.BuildConvID(p.getInstanceId(), channel.ID), GroupName: channel.Name, IsGroup: true}, nil
	}
	channelName := normalizeSlackChannelName(title)
	if channelName == "" {
		return nil, fmt.Errorf("slack channel name must contain at least one letter or number")
	}
	channel, err := client.CreateConversation(slack.CreateConversationParams{
		ChannelName: channelName,
		IsPrivate:   conversationType == "private_channel",
	})
	if err != nil {
		return nil, err
	}
	if len(participantIDs) > 0 {
		if _, err = client.InviteUsersToConversation(channel.ID, participantIDs...); err != nil {
			return nil, fmt.Errorf("channel created but participants could not be invited: %w", err)
		}
	}
	return &models.Conversation{ProtocolConvID: core.BuildConvID(p.getInstanceId(), channel.ID), GroupName: channel.Name, IsGroup: true}, nil
}

// CreateGroup creates a new channel (group).
func (p *SlackProvider) CreateGroup(groupName string, participantIDs []string) (*models.Conversation, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}

	channelName := normalizeSlackChannelName(groupName)
	if channelName == "" {
		return nil, fmt.Errorf("slack channel name must contain at least one letter or number")
	}
	channel, err := p.client.CreateConversation(slack.CreateConversationParams{
		ChannelName: channelName,
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

	channelName := normalizeSlackChannelName(newName)
	if channelName == "" {
		return fmt.Errorf("slack channel name must contain at least one letter or number")
	}
	_, err := p.client.RenameConversation(core.StripConvID(conversationID), channelName)
	return err
}

func (p *SlackProvider) GetGroupDetails(conversationID string) (*models.GroupDetails, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.client == nil {
		return nil, fmt.Errorf("slack client not initialized")
	}
	rawID := core.StripConvID(conversationID)
	channel, err := p.client.GetConversationInfo(&slack.GetConversationInfoInput{ChannelID: rawID, IncludeNumMembers: true})
	if err != nil {
		return nil, err
	}
	description := channel.Purpose.Value
	if description == "" {
		description = channel.Topic.Value
	}
	return &models.GroupDetails{ConversationID: core.BuildConvID(p.getInstanceId(), rawID), Name: channel.Name, Description: description, IsMember: channel.IsMember, CanSendMessages: channel.IsMember && !channel.IsArchived}, nil
}

func (p *SlackProvider) UpdateGroupDescription(conversationID, description string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.client == nil {
		return fmt.Errorf("slack client not initialized")
	}
	channel, err := p.client.SetPurposeOfConversation(core.StripConvID(conversationID), description)
	if err != nil {
		return err
	}
	if channel == nil {
		return fmt.Errorf("Slack returned no channel after updating its description")
	}
	if channel.Purpose.Value != description {
		return fmt.Errorf("Slack did not apply the channel description")
	}
	return nil
}

func (p *SlackProvider) UpdateGroupPhoto(string, []byte) error {
	return fmt.Errorf("Slack does not support custom channel photos")
}

// AddGroupParticipants adds users to a channel.
func (p *SlackProvider) AddGroupParticipants(conversationID string, participantIDs []string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	_, err := p.client.InviteUsersToConversation(core.StripConvID(conversationID), participantIDs...)
	return err
}

// RemoveGroupParticipants kicks users from a channel.
func (p *SlackProvider) RemoveGroupParticipants(conversationID string, participantIDs []string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return fmt.Errorf("slack client not initialized")
	}

	conversationID = core.StripConvID(conversationID)
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

	rawID := core.StripConvID(conversationID)
	_, err := p.client.LeaveConversation(rawID)
	if err == nil {
		return nil
	}
	// Slack does not allow the final member to leave a channel. Archiving is
	// the equivalent terminal action and removes it from active conversations.
	if err.Error() == "last_member" {
		if archiveErr := p.client.ArchiveConversation(rawID); archiveErr != nil {
			return fmt.Errorf("leave group: %v; archive final-member channel: %w", err, archiveErr)
		}
		return nil
	}
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
	conversationID = core.StripConvID(conversationID)

	// Check if this is a User ID (1-to-1 DM stored as normalized user ID)
	// Slack user IDs start with "U" and are stored directly after DM channel normalization
	if len(conversationID) > 0 && conversationID[0] == 'U' {
		var participants []models.GroupParticipant

		// Get current user ID
		authTest, err := p.client.AuthTest()
		if err == nil && authTest != nil {
			participants = append(participants, models.GroupParticipant{
				UserID:  authTest.UserID,
				IsAdmin: false,
			})
		}

		// The conversation ID itself IS the other user's ID
		participants = append(participants, models.GroupParticipant{
			UserID:  conversationID,
			IsAdmin: false,
		})

		return participants, nil
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

	// For channels/groups, collect every page returned by conversations.members.
	// Slack's endpoint is cursor-paginated, so a single request can silently omit
	// most members of larger channels.
	var userIDs []string
	cursor := ""
	for {
		page, nextCursor, err := p.client.GetUsersInConversation(&slack.GetUsersInConversationParameters{
			ChannelID: conversationID,
			Cursor:    cursor,
			Limit:     200,
		})
		if err != nil {
			return nil, fmt.Errorf("get users in Slack conversation %s: %w", conversationID, err)
		}
		userIDs = append(userIDs, page...)
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	var participants []models.GroupParticipant
	for _, uid := range userIDs {
		participants = append(participants, models.GroupParticipant{
			UserID:  uid,
			IsAdmin: false,
			IsSelf:  uid == p.selfUserID,
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
