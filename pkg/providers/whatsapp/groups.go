package whatsapp

import (
	"Loom/pkg/models"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

func (w *WhatsAppProvider) cacheGroupParticipants(groupJID types.JID) {
	if w.client == nil {
		return
	}

	// Get group info to obtain participants with phone numbers
	// This is a potentially blocking call, so we don't hold any locks
	groupInfo, err := w.client.GetGroupInfo(w.ctx, groupJID)
	if err != nil || groupInfo == nil {
		return
	}

	// Create mapping of participant JID to phone number
	participants := make(map[types.JID]string)
	for _, participant := range groupInfo.Participants {
		// Check if participant has a LID and a phone number
		if participant.JID.Server == "lid" && !participant.PhoneNumber.IsEmpty() {
			// Store mapping: participant LID -> phone number string
			participants[participant.JID] = participant.PhoneNumber.String()
		}
	}

	// Only take lock for the final write operation
	w.mu.Lock()
	if w.groupParticipants == nil {
		w.groupParticipants = make(map[string]map[types.JID]string)
	}
	w.groupParticipants[groupJID.String()] = participants
	w.mu.Unlock()
}

func (w *WhatsAppProvider) CreateGroup(groupName string, participantIDs []string) (*models.Conversation, error) {
	w.mu.RLock()
	client := w.client
	ctx := w.ctx
	w.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	// Parse participant IDs to JIDs
	participants := make([]types.JID, 0, len(participantIDs))
	for _, id := range participantIDs {
		// Clean up ID if needed (remove prefixes etc)
		cleanID := strings.TrimPrefix(id, "whatsapp-")

		// Parse JID
		jid, err := types.ParseJID(cleanID)
		if err != nil {
			// Try adding suffix if missing (assuming phone number)
			if !strings.Contains(cleanID, "@") {
				jid, err = types.ParseJID(cleanID + "@s.whatsapp.net")
			}

			if err != nil {
				return nil, fmt.Errorf("invalid participant ID %s: %w", id, err)
			}
		}
		participants = append(participants, jid)
	}

	// Create group
	resp, err := client.CreateGroup(ctx, whatsmeow.ReqCreateGroup{
		Name:         groupName,
		Participants: participants,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create group: %w", err)
	}

	// Format conversation ID
	conversationID := resp.JID.String()

	// Create conversation model
	conversation := &models.Conversation{
		ProtocolConvID:    conversationID,
		GroupName:         groupName,
		IsGroup:           true,
		GroupParticipants: make([]models.GroupParticipant, 0, len(participants)+1),
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	// Add self as participant (admin)
	if w.client.Store.ID != nil {
		conversation.GroupParticipants = append(conversation.GroupParticipants, models.GroupParticipant{
			UserID:   w.client.Store.ID.ToNonAD().String(),
			IsAdmin:  true,
			JoinedAt: time.Now(),
		})
	}

	// Add other participants
	for _, jid := range participants {
		conversation.GroupParticipants = append(conversation.GroupParticipants, models.GroupParticipant{
			UserID:   jid.String(),
			IsAdmin:  false,
			JoinedAt: time.Now(),
		})
	}

	return conversation, nil
}

func (w *WhatsAppProvider) UpdateGroupName(conversationID string, newName string) error {
	// TODO: Implement group name update
	markUnused(conversationID, newName)
	return fmt.Errorf("group name update not yet implemented")
}

func (w *WhatsAppProvider) AddGroupParticipants(conversationID string, participantIDs []string) error {
	// TODO: Implement adding participants
	markUnused(conversationID, participantIDs)
	return fmt.Errorf("adding participants not yet implemented")
}

func (w *WhatsAppProvider) RemoveGroupParticipants(conversationID string, participantIDs []string) error {
	// TODO: Implement removing participants
	markUnused(conversationID, participantIDs)
	return fmt.Errorf("removing participants not yet implemented")
}

func (w *WhatsAppProvider) LeaveGroup(conversationID string) error {
	// TODO: Implement leaving group
	markUnused(conversationID)
	return fmt.Errorf("leaving group not yet implemented")
}

func (w *WhatsAppProvider) PromoteGroupAdmins(conversationID string, participantIDs []string) error {
	// TODO: Implement promoting admins
	markUnused(conversationID, participantIDs)
	return fmt.Errorf("promoting admins not yet implemented")
}

func (w *WhatsAppProvider) DemoteGroupAdmins(conversationID string, participantIDs []string) error {
	// TODO: Implement demoting admins
	markUnused(conversationID, participantIDs)
	return fmt.Errorf("demoting admins not yet implemented")
}

func (w *WhatsAppProvider) GetGroupParticipants(conversationID string) ([]models.GroupParticipant, error) {
	w.mu.RLock()
	client := w.client
	ctx := w.ctx
	w.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	// Parse conversation ID (JID)
	groupJID, err := types.ParseJID(conversationID)
	if err != nil {
		return nil, fmt.Errorf("invalid conversation ID: %w", err)
	}

	// Verify it's a group
	if groupJID.Server != types.GroupServer {
		return nil, fmt.Errorf("conversation is not a group: %s", conversationID)
	}

	// Get group info to obtain participants
	groupInfo, err := client.GetGroupInfo(ctx, groupJID)
	if err != nil {
		return nil, fmt.Errorf("failed to get group info: %w", err)
	}

	if groupInfo == nil {
		return nil, fmt.Errorf("group info is nil")
	}

	// Convert participants to models.GroupParticipant
	participants := make([]models.GroupParticipant, 0, len(groupInfo.Participants))

	// Also build a map of LID -> phone number for later conversion
	lidToPhoneMap := make(map[types.JID]string)

	for _, participant := range groupInfo.Participants {
		// Determine if participant is admin
		// In whatsmeow, GroupParticipant has an IsSuperAdmin field
		isAdmin := participant.IsSuperAdmin

		// Use current time as JoinedAt if not available (whatsmeow doesn't provide join time)
		joinedAt := time.Now()

		// Use PhoneNumber if available, otherwise fallback to JID
		userID := participant.JID.String()
		if !participant.PhoneNumber.IsEmpty() {
			// Use phone number (may include @s.whatsapp.net suffix)
			phoneStr := participant.PhoneNumber.String()
			// Remove @s.whatsapp.net suffix if present
			if strings.Contains(phoneStr, "@s.whatsapp.net") {
				userID = phoneStr
			} else {
				// If no suffix, add it for consistency
				userID = phoneStr + "@s.whatsapp.net"
			}
		}

		// Store mapping from LID to phone number
		if participant.JID.Server == "lid" && !participant.PhoneNumber.IsEmpty() {
			lidToPhoneMap[participant.JID] = userID
			// Also store in LinkedAccount.Extra for persistence
			w.storeContactMapping(participant.JID.String(), userID)
		}

		participants = append(participants, models.GroupParticipant{
			UserID:   userID,
			IsAdmin:  isAdmin,
			JoinedAt: joinedAt,
		})
	}

	// Cache the LID to phone number mapping
	w.mu.Lock()
	w.groupParticipants[groupJID.String()] = lidToPhoneMap
	w.mu.Unlock()

	return participants, nil
}

func (w *WhatsAppProvider) CreateGroupInviteLink(conversationID string) (string, error) {
	// TODO: Implement invite link creation
	markUnused(conversationID)
	return "", fmt.Errorf("invite links not yet implemented")
}

func (w *WhatsAppProvider) RevokeGroupInviteLink(conversationID string) error {
	// TODO: Implement invite link revocation
	markUnused(conversationID)
	return fmt.Errorf("invite links not yet implemented")
}

func (w *WhatsAppProvider) JoinGroupByInviteLink(inviteLink string) (*models.Conversation, error) {
	// TODO: Implement joining via invite link
	markUnused(inviteLink)
	return nil, fmt.Errorf("invite links not yet implemented")
}

func (w *WhatsAppProvider) JoinGroupByInviteMessage(inviteMessageID string) (*models.Conversation, error) {
	// TODO: Implement joining via invite message
	markUnused(inviteMessageID)
	return nil, fmt.Errorf("invite messages not yet implemented")
}
