package whatsapp

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	"context"
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
			phoneStr := participant.PhoneNumber.String()
			participants[participant.JID] = phoneStr

			// Persist the mapping to the database for future lookups
			// This ensures we can resolve LIDs even after restart
			if err := w.saveLIDMapping(participant.JID.String(), phoneStr); err != nil {
				fmt.Printf("WhatsApp: Warning - Failed to save LID mapping for %s: %v\n", participant.JID.String(), err)
			} else {
				fmt.Printf("WhatsApp: Saved LID mapping: %s -> %s (from group %s)\n", participant.JID.String(), phoneStr, groupJID.String())
			}

			// Also store in LinkedAccount.Extra for additional persistence
			w.storeContactMapping(participant.JID.String(), phoneStr)
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

	// Format conversation ID (namespaced so it is globally unique across instances)
	conversationID := core.BuildConvID(w.getInstanceId(), resp.JID.String())

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
	client, ctx, groupJID, err := w.groupClient(conversationID)
	if err != nil {
		return err
	}
	if err := client.SetGroupName(ctx, groupJID, strings.TrimSpace(newName)); err != nil {
		return fmt.Errorf("update group name: %w", err)
	}
	return nil
}

func (w *WhatsAppProvider) AddGroupParticipants(conversationID string, participantIDs []string) error {
	return w.updateGroupParticipants(conversationID, participantIDs, whatsmeow.ParticipantChangeAdd)
}

func (w *WhatsAppProvider) RemoveGroupParticipants(conversationID string, participantIDs []string) error {
	return w.updateGroupParticipants(conversationID, participantIDs, whatsmeow.ParticipantChangeRemove)
}

func (w *WhatsAppProvider) updateGroupParticipants(conversationID string, participantIDs []string, action whatsmeow.ParticipantChange) error {
	w.mu.RLock()
	client, ctx := w.client, w.ctx
	w.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("client not initialized")
	}
	groupJID, err := types.ParseJID(core.StripConvID(conversationID))
	if err != nil || groupJID.Server != types.GroupServer {
		return fmt.Errorf("invalid WhatsApp group ID %q", conversationID)
	}
	participants := make([]types.JID, 0, len(participantIDs))
	for _, participantID := range participantIDs {
		participantID = strings.TrimSpace(strings.TrimPrefix(participantID, "whatsapp-"))
		if participantID == "" {
			continue
		}
		if !strings.Contains(participantID, "@") {
			participantID += "@s.whatsapp.net"
		}
		jid, parseErr := types.ParseJID(participantID)
		if parseErr != nil {
			return fmt.Errorf("invalid participant ID %q: %w", participantID, parseErr)
		}
		participants = append(participants, jid)
	}
	if len(participants) == 0 {
		return fmt.Errorf("at least one participant is required")
	}
	results, err := client.UpdateGroupParticipants(ctx, groupJID, participants, action)
	if err != nil {
		return fmt.Errorf("%s group participants: %w", action, err)
	}
	for _, result := range results {
		if result.Error != 0 {
			return fmt.Errorf("%s participant %s: WhatsApp error %d", action, result.JID, result.Error)
		}
	}
	go w.cacheGroupParticipants(groupJID)
	return nil
}

func (w *WhatsAppProvider) LeaveGroup(conversationID string) error {
	w.mu.RLock()
	client := w.client
	ctx := w.ctx
	w.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("client not initialized")
	}

	groupJID, err := types.ParseJID(core.StripConvID(conversationID))
	if err != nil {
		return fmt.Errorf("invalid conversation ID: %w", err)
	}
	if groupJID.Server != types.GroupServer {
		return fmt.Errorf("conversation is not a group: %s", conversationID)
	}

	if err := client.LeaveGroup(ctx, groupJID); err != nil {
		return fmt.Errorf("leave group: %w", err)
	}
	return nil
}

func (w *WhatsAppProvider) PromoteGroupAdmins(conversationID string, participantIDs []string) error {
	return w.updateGroupParticipants(conversationID, participantIDs, whatsmeow.ParticipantChangePromote)
}

func (w *WhatsAppProvider) DemoteGroupAdmins(conversationID string, participantIDs []string) error {
	return w.updateGroupParticipants(conversationID, participantIDs, whatsmeow.ParticipantChangeDemote)
}

func (w *WhatsAppProvider) groupClient(conversationID string) (*whatsmeow.Client, context.Context, types.JID, error) {
	w.mu.RLock()
	client, ctx := w.client, w.ctx
	w.mu.RUnlock()
	if client == nil {
		return nil, nil, types.JID{}, fmt.Errorf("client not initialized")
	}
	groupJID, err := types.ParseJID(core.StripConvID(conversationID))
	if err != nil || groupJID.Server != types.GroupServer {
		return nil, nil, types.JID{}, fmt.Errorf("invalid WhatsApp group ID %q", conversationID)
	}
	return client, ctx, groupJID, nil
}

func (w *WhatsAppProvider) GetGroupDetails(conversationID string) (*models.GroupDetails, error) {
	client, ctx, groupJID, err := w.groupClient(conversationID)
	if err != nil {
		return nil, err
	}
	info, err := client.GetGroupInfo(ctx, groupJID)
	if err != nil {
		return nil, fmt.Errorf("get group details: %w", err)
	}
	canSendMessages := !info.IsAnnounce
	if info.IsAnnounce && client.Store.ID != nil {
		self := client.Store.ID.ToNonAD()
		for _, participant := range info.Participants {
			if participant.JID.ToNonAD() == self || participant.PhoneNumber.ToNonAD() == self || participant.LID.ToNonAD() == self {
				canSendMessages = participant.IsAdmin || participant.IsSuperAdmin
				break
			}
		}
	}
	return &models.GroupDetails{ConversationID: core.BuildConvID(w.getInstanceId(), groupJID.String()), Name: info.Name, Description: info.Topic, AvatarURL: w.getProfilePictureURL(groupJID), CanSendMessages: canSendMessages}, nil
}

func (w *WhatsAppProvider) UpdateGroupDescription(conversationID, description string) error {
	client, ctx, groupJID, err := w.groupClient(conversationID)
	if err != nil {
		return err
	}
	if err := client.SetGroupDescription(ctx, groupJID, description); err != nil {
		return fmt.Errorf("update group description: %w", err)
	}
	return nil
}

func (w *WhatsAppProvider) UpdateGroupPhoto(conversationID string, photo []byte) error {
	client, ctx, groupJID, err := w.groupClient(conversationID)
	if err != nil {
		return err
	}
	if len(photo) == 0 {
		return fmt.Errorf("group photo is empty")
	}
	if _, err := client.SetGroupPhoto(ctx, groupJID, photo); err != nil {
		return fmt.Errorf("update group photo: %w", err)
	}
	w.avatarFailuresMu.Lock()
	delete(w.avatarFailures, groupJID.String())
	w.avatarFailuresMu.Unlock()
	return nil
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
	groupJID, err := types.ParseJID(core.StripConvID(conversationID))
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
		isAdmin := participant.IsAdmin || participant.IsSuperAdmin
		isSelf := false
		if client.Store.ID != nil {
			self := client.Store.ID.ToNonAD()
			isSelf = participant.JID.ToNonAD() == self || participant.PhoneNumber.ToNonAD() == self || participant.LID.ToNonAD() == self
		}

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

			// Persist the mapping to the database for future lookups
			if err := w.saveLIDMapping(participant.JID.String(), userID); err != nil {
				fmt.Printf("WhatsApp: Warning - Failed to save LID mapping for %s: %v\n", participant.JID.String(), err)
			} else {
				fmt.Printf("WhatsApp: Saved LID mapping: %s -> %s (from GetGroupParticipants)\n", participant.JID.String(), userID)
			}

			// Also store in LinkedAccount.Extra for persistence
			w.storeContactMapping(participant.JID.String(), userID)
		}

		participants = append(participants, models.GroupParticipant{
			UserID:   userID,
			IsAdmin:  isAdmin,
			IsSelf:   isSelf,
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
