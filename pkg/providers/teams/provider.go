// Package teams adapts the Microsoft Teams web protocol for Loom.
package teams

import (
	"Loom/pkg/core"
	"Loom/pkg/db"
	"Loom/pkg/models"
	"Loom/pkg/providers/messageformat"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-teams/pkg/msteams"
)

const providerID = "teams"

type session struct {
	TenantID     string `json:"tenant_id"`
	UserMRI      string `json:"user_mri"`
	DisplayName  string `json:"display_name,omitempty"`
	RefreshToken string `json:"refresh_token"`
	AuthToken    string `json:"auth_token,omitempty"`
	SkypeToken   string `json:"skype_token,omitempty"`
	ChatSvcBase  string `json:"chat_service_base,omitempty"`
}

type Provider struct {
	unsupportedProvider

	mu             sync.RWMutex
	fileMu         sync.RWMutex
	config         core.ProviderConfig
	instance       string
	session        *session
	client         *msteams.Client
	cancel         context.CancelFunc
	eventChan      chan core.ProviderEvent
	sharedFiles    map[string]msteams.SharedFile
	attachmentURLs map[string]struct{}
	avatarFailures map[string]struct{}
}

var _ core.Provider = (*Provider)(nil)

func NewProvider() *Provider {
	return &Provider{
		config:         make(core.ProviderConfig),
		eventChan:      make(chan core.ProviderEvent, 500),
		sharedFiles:    make(map[string]msteams.SharedFile),
		attachmentURLs: make(map[string]struct{}),
		avatarFailures: make(map[string]struct{}),
	}
}

func (p *Provider) Init(config core.ProviderConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config = config
	p.instance, _ = config.GetString("_instance_id")
	if p.instance == "" {
		p.instance = providerID + "-1"
	}
	return p.loadSessionLocked()
}

func (p *Provider) GetConfig() core.ProviderConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(core.ProviderConfig, len(p.config))
	for key, value := range p.config {
		out[key] = value
	}
	return out
}

func (p *Provider) SetConfig(config core.ProviderConfig) error {
	if config == nil {
		return fmt.Errorf("%s: configuration cannot be nil", providerID)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config = config
	if instance, ok := config.GetString("_instance_id"); ok && instance != "" {
		p.instance = instance
	}
	return nil
}

func (p *Provider) IsAuthenticated() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.session != nil && p.session.TenantID != "" && p.session.UserMRI != "" && p.session.RefreshToken != ""
}

func (p *Provider) Connect() error {
	p.mu.Lock()
	if p.session == nil {
		p.mu.Unlock()
		return fmt.Errorf("%s: Microsoft login is required", providerID)
	}
	if p.client != nil && p.client.IsLoggedIn() {
		p.mu.Unlock()
		return nil
	}
	s := *p.session
	client, err := msteams.NewClient(msteams.ClientConfig{
		TenantID:     s.TenantID,
		UserMRI:      s.UserMRI,
		RefreshToken: s.RefreshToken,
		AuthToken:    s.AuthToken,
		SkypeToken:   s.SkypeToken,
		Endpoints:    msteams.Endpoints{ChatSvcBase: s.ChatSvcBase},
		Logger:       zerolog.Nop(),
	})
	if err != nil {
		p.mu.Unlock()
		return fmt.Errorf("%s: create client: %w", providerID, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.client = client
	p.cancel = cancel
	p.mu.Unlock()

	if err := client.Connect(ctx); err != nil {
		cancel()
		_ = client.Close()
		p.mu.Lock()
		if p.client == client {
			p.client = nil
			p.cancel = nil
		}
		p.mu.Unlock()
		return fmt.Errorf("%s: connect: %w", providerID, err)
	}

	p.mu.Lock()
	p.updateSessionFromClientLocked(client)
	err = p.saveSessionLocked()
	p.mu.Unlock()
	if err != nil {
		_ = p.Disconnect()
		return fmt.Errorf("%s: save refreshed session: %w", providerID, err)
	}
	go p.forwardEvents(ctx, client)
	go p.watchConversationList(ctx, client)
	go p.watchPresence(ctx, client)
	return nil
}

func (p *Provider) Disconnect() error {
	p.mu.Lock()
	client, cancel := p.client, p.cancel
	p.client, p.cancel = nil, nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if client != nil {
		return client.Close()
	}
	return nil
}

func (p *Provider) StreamEvents() (<-chan core.ProviderEvent, error) {
	return p.eventChan, nil
}

func (p *Provider) SyncHistory(since time.Time) error {
	client, instance, err := p.connectedClient()
	if err != nil {
		return err
	}
	p.emit(core.SyncStatusEvent{InstanceID: instance, Status: core.SyncStatusFetchingContacts, Message: "Fetching Microsoft Teams conversations", Progress: 0})
	chats, err := client.ListChats(context.Background())
	if err != nil {
		p.emit(core.SyncStatusEvent{InstanceID: instance, Status: core.SyncStatusError, Message: "Unable to fetch Microsoft Teams conversations", Progress: -1})
		return fmt.Errorf("%s: list chats: %w", providerID, err)
	}
	if err := p.removeVirtualConversations(); err != nil {
		return err
	}
	skipped := 0
	for index, chat := range chats {
		if chat.ID == "" || isVirtualTeamsThread(chat.ID) {
			continue
		}
		// Resolve the roster before converting history so stored messages get
		// human sender names on the first pass, not only after the chat is saved.
		account := p.linkedAccount(client, chat)
		p.emit(core.SyncStatusEvent{
			InstanceID: instance, Status: core.SyncStatusFetchingHistory,
			ConversationID: chat.ID, Message: "Syncing Microsoft Teams history",
			Progress: (index * 100) / max(1, len(chats)),
		})
		conversationSince := p.conversationSyncSince(chat.ID, since)
		messages, err := p.GetConversationHistory(chat.ID, 0, nil, &conversationSince)
		if err != nil {
			if isSkippableHistoryError(err) {
				skipped++
				continue
			}
			return err
		}
		account = p.linkedAccount(client, chat)
		if isTechnicalConversationName(account.Username, chat.ID) {
			if historyName := participantNamesFromMessages(messages, client.UserMRI()); historyName != "" {
				account.Username = historyName
			} else if storedName := participantNamesFromStoredMessages(chat.ID, client.UserMRI()); storedName != "" {
				account.Username = storedName
			}
		}
		if err := p.storeConversation(account); err != nil {
			return err
		}
		if err := p.repairStoredSenderNames(client, chat.ID, chat.Members); err != nil {
			return err
		}
		if err := p.storeMessages(messages); err != nil {
			return err
		}
		if len(messages) > 0 {
			p.emit(core.MessageBatchEvent{
				InstanceID:     instance,
				ConversationID: chat.ID,
				Messages:       messages,
				IsHistorical:   conversationSince.IsZero(),
			})
		}
	}
	if err := p.repairStoredHTMLFormatting(); err != nil {
		return err
	}
	completionMessage := "Microsoft Teams sync completed"
	if skipped > 0 {
		completionMessage = fmt.Sprintf("Microsoft Teams sync completed (%d inaccessible conversation(s) skipped)", skipped)
	}
	p.emit(core.SyncStatusEvent{InstanceID: instance, Status: core.SyncStatusCompleted, Message: completionMessage, Progress: 100})
	p.emit(core.ContactStatusEvent{InstanceID: instance, UserID: "refresh", Status: "new_conversations_discovered"})
	return nil
}

// conversationSyncSince prevents the provider-wide watermark from creating
// permanent holes in quieter or newly-discovered chats. A conversation with
// local messages resumes from its own newest message (with overlap); a chat
// absent from the message store receives a full initial backfill.
func (p *Provider) conversationSyncSince(conversationID string, globalSince time.Time) time.Time {
	if db.DB == nil {
		return globalSince
	}
	var newest models.Message
	err := db.DB.Select("timestamp").Where(
		"protocol_conv_id = ? AND deleted_at IS NULL",
		core.BuildConvID(p.instance, core.StripConvID(conversationID)),
	).Order("timestamp DESC").Limit(1).Find(&newest).Error
	if err != nil {
		return globalSince
	}
	if newest.Timestamp.IsZero() {
		return teamsSyncLowerBound(globalSince, nil)
	}
	return teamsSyncLowerBound(globalSince, &newest.Timestamp)
}

func teamsSyncLowerBound(globalSince time.Time, newest *time.Time) time.Time {
	if newest == nil || newest.IsZero() {
		return time.Time{}
	}
	lowerBound := *newest
	if !globalSince.IsZero() && globalSince.Before(lowerBound) {
		lowerBound = globalSince
	}
	return lowerBound.Add(-5 * time.Minute)
}

func isSkippableHistoryError(err error) bool {
	return errors.Is(err, msteams.ErrForbidden) || errors.Is(err, msteams.ErrNotFound)
}

func (p *Provider) GetContacts() ([]models.LinkedAccount, error) {
	client, _, err := p.connectedClient()
	if err != nil {
		return nil, err
	}
	chats, err := client.ListChats(context.Background())
	if err != nil {
		return nil, fmt.Errorf("%s: list chats: %w", providerID, err)
	}
	out := make([]models.LinkedAccount, 0, len(chats))
	for _, chat := range chats {
		if isVirtualTeamsThread(chat.ID) {
			continue
		}
		out = append(out, p.linkedAccount(client, chat))
	}
	mris := make([]string, 0, len(out))
	for _, account := range out {
		if !account.IsGroup && strings.HasPrefix(account.UserID, "8:") {
			mris = append(mris, account.UserID)
		}
	}
	if presences, presenceErr := client.GetPresences(context.Background(), mris); presenceErr == nil {
		presenceByMRI := make(map[string]msteams.Presence, len(presences))
		for _, presence := range presences {
			presenceByMRI[strings.ToLower(presence.MRI)] = presence
		}
		for index := range out {
			if presence, ok := presenceByMRI[strings.ToLower(out[index].UserID)]; ok {
				out[index].Status = teamsPresenceStatus(presence.Availability, presence.Activity)
				extra, _ := json.Marshal(map[string]string{"activity": presence.Activity})
				out[index].Extra = string(extra)
			}
		}
	}
	return out, nil
}

func (p *Provider) SearchContacts(query string) ([]models.LinkedAccount, error) {
	client, _, err := p.connectedClient()
	if err != nil {
		return nil, err
	}
	users, err := client.SearchUsers(context.Background(), query)
	if err != nil {
		return nil, fmt.Errorf("%s: search contacts: %w", providerID, err)
	}
	mris := make([]string, 0, len(users))
	for _, user := range users {
		if user.MRI != "" && !strings.EqualFold(user.MRI, client.UserMRI()) {
			mris = append(mris, user.MRI)
		}
	}
	presenceByMRI := make(map[string]msteams.Presence, len(mris))
	if presences, presenceErr := client.GetPresences(context.Background(), mris); presenceErr == nil {
		for _, presence := range presences {
			presenceByMRI[strings.ToLower(presence.MRI)] = presence
		}
	}
	dmByMRI := make(map[string]string)
	if chats, listErr := client.ListChats(context.Background()); listErr == nil {
		for _, chat := range chats {
			if chat.Type != msteams.ChatType1on1 {
				continue
			}
			members := chat.Members
			if len(members) == 0 {
				if detailed, detailErr := client.GetChat(context.Background(), chat.ID); detailErr == nil {
					members = detailed.Members
				}
			}
			for _, member := range members {
				if member.MRI != "" && !strings.EqualFold(member.MRI, client.UserMRI()) {
					dmByMRI[strings.ToLower(member.MRI)] = chat.ID
				}
			}
		}
	}
	out := make([]models.LinkedAccount, 0, len(users))
	for _, user := range users {
		if user.MRI == "" || strings.EqualFold(user.MRI, client.UserMRI()) {
			continue
		}
		presence := presenceByMRI[strings.ToLower(user.MRI)]
		status := teamsPresenceStatus(presence.Availability, presence.Activity)
		extra, _ := json.Marshal(map[string]string{"email": user.Email, "jobTitle": user.JobTitle, "company": user.Company, "department": user.Department, "activity": presence.Activity})
		account := models.LinkedAccount{Protocol: providerID, ProviderInstanceID: p.instance, UserID: user.MRI, Username: user.DisplayName, AvatarURL: user.AvatarURL, Status: status, Extra: string(extra)}
		account.ConversationID = dmByMRI[strings.ToLower(user.MRI)]
		if account.Username == "" {
			account.Username = user.Email
		}
		if err := p.storeDirectoryContact(account); err != nil {
			return nil, fmt.Errorf("%s: store directory contact: %w", providerID, err)
		}
		out = append(out, account)
	}
	return out, nil
}

func (p *Provider) GetGroupParticipants(conversationID string) ([]models.GroupParticipant, error) {
	client, _, err := p.connectedClient()
	if err != nil {
		return nil, err
	}
	chat, err := client.GetChat(context.Background(), core.StripConvID(conversationID))
	if err != nil {
		return nil, err
	}
	participants := make([]models.GroupParticipant, 0, len(chat.Members))
	for _, member := range chat.Members {
		if member.MRI == "" {
			continue
		}
		participants = append(participants, models.GroupParticipant{UserID: member.MRI, IsAdmin: strings.EqualFold(member.Role, "Admin") || strings.EqualFold(member.Role, "Owner"), IsSelf: strings.EqualFold(member.MRI, client.UserMRI())})
	}
	return participants, nil
}

func (p *Provider) CreateDirectConversation(participantID string) (*models.Conversation, error) {
	client, _, err := p.connectedClient()
	if err != nil {
		return nil, err
	}
	chat, err := client.StartOneOnOne(context.Background(), participantID)
	if err != nil {
		return nil, err
	}
	return &models.Conversation{ProtocolConvID: core.BuildConvID(p.instance, chat.ID), IsGroup: false}, nil
}

func (p *Provider) CurrentUserID() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.client != nil {
		return p.client.UserMRI()
	}
	if p.session != nil {
		return p.session.UserMRI
	}
	return ""
}

func (p *Provider) CreateConversation(conversationType, title string, participantIDs []string) (*models.Conversation, error) {
	if conversationType != "group" {
		return nil, fmt.Errorf("%s: unsupported conversation type %q", providerID, conversationType)
	}
	return p.CreateGroup(title, participantIDs)
}

func (p *Provider) CreateGroup(groupName string, participantIDs []string) (*models.Conversation, error) {
	client, _, err := p.connectedClient()
	if err != nil {
		return nil, err
	}
	chat, err := client.CreateGroupChat(context.Background(), groupName, participantIDs)
	if err != nil {
		return nil, err
	}
	participants := make([]models.GroupParticipant, 0, len(participantIDs)+1)
	seen := make(map[string]bool, len(participantIDs)+1)
	addParticipant := func(userID string, isAdmin bool) {
		key := strings.ToLower(strings.TrimSpace(userID))
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		participants = append(participants, models.GroupParticipant{
			UserID: userID, IsAdmin: isAdmin, IsSelf: strings.EqualFold(userID, client.UserMRI()), JoinedAt: time.Now(),
		})
	}
	for _, member := range chat.Members {
		addParticipant(member.MRI, strings.EqualFold(member.Role, "Admin") || strings.EqualFold(member.Role, "Owner"))
	}
	addParticipant(client.UserMRI(), true)
	for _, participantID := range participantIDs {
		addParticipant(participantID, true)
	}
	displayName := strings.TrimSpace(chat.Topic)
	if displayName == "" {
		displayName = p.createdGroupDisplayName(client, participantIDs)
	}
	return &models.Conversation{
		ProtocolConvID: core.BuildConvID(p.instance, chat.ID), IsGroup: true,
		GroupName: displayName, GroupParticipants: participants,
	}, nil
}

func (p *Provider) createdGroupDisplayName(client *msteams.Client, participantIDs []string) string {
	names := make([]string, 0, len(participantIDs))
	seen := make(map[string]bool, len(participantIDs))
	for _, participantID := range participantIDs {
		if strings.EqualFold(participantID, client.UserMRI()) {
			continue
		}
		name := strings.TrimSpace(client.CachedDisplayName(participantID))
		if name == "" || isTechnicalConversationName(name, participantID) {
			if profile, err := client.GetUser(context.Background(), participantID); err == nil && profile != nil {
				name = strings.TrimSpace(profile.DisplayName)
			}
		}
		if name == "" {
			name = participantID
		}
		key := strings.ToLower(name)
		if !seen[key] {
			seen[key] = true
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

func (p *Provider) UpdateGroupName(conversationID, newName string) error {
	client, _, err := p.connectedClient()
	if err != nil {
		return err
	}
	if err := client.UpdateThreadTopic(context.Background(), core.StripConvID(conversationID), newName); err != nil {
		return fmt.Errorf("%s: rename group: %w", providerID, err)
	}
	return nil
}

func (p *Provider) GetGroupDetails(conversationID string) (*models.GroupDetails, error) {
	client, instance, err := p.connectedClient()
	if err != nil {
		return nil, err
	}
	threadID := core.StripConvID(conversationID)
	chat, err := client.GetChat(context.Background(), threadID)
	if err != nil {
		return nil, err
	}
	canSend := false
	for _, member := range chat.Members {
		if strings.EqualFold(member.MRI, client.UserMRI()) {
			canSend = true
			break
		}
	}
	return &models.GroupDetails{
		ConversationID:  core.BuildConvID(instance, threadID),
		Name:            chat.Topic,
		Description:     chat.Description,
		IsMember:        canSend,
		CanSendMessages: canSend,
	}, nil
}

func (p *Provider) UpdateGroupDescription(conversationID, description string) error {
	client, _, err := p.connectedClient()
	if err != nil {
		return err
	}
	if err := client.UpdateThreadDescription(context.Background(), core.StripConvID(conversationID), description); err != nil {
		return fmt.Errorf("%s: update group description: %w", providerID, err)
	}
	return nil
}

func (p *Provider) UpdateGroupPhoto(string, []byte) error {
	return fmt.Errorf("%s: custom group photos are not supported", providerID)
}

func (p *Provider) AddGroupParticipants(conversationID string, participantIDs []string) error {
	client, _, err := p.connectedClient()
	if err != nil {
		return err
	}
	if err := client.AddThreadMembers(context.Background(), core.StripConvID(conversationID), participantIDs); err != nil {
		return fmt.Errorf("%s: add group participants: %w", providerID, err)
	}
	return nil
}

func (p *Provider) LeaveGroup(conversationID string) error {
	client, _, err := p.connectedClient()
	if err != nil {
		return err
	}
	threadID := core.StripConvID(conversationID)
	if err := client.LeaveGroupChat(context.Background(), threadID); err != nil {
		return fmt.Errorf("%s: leave group: %w", providerID, err)
	}
	return nil
}

func (p *Provider) RemoveGroupParticipants(conversationID string, participantIDs []string) error {
	client, _, err := p.connectedClient()
	if err != nil {
		return err
	}
	threadID := core.StripConvID(conversationID)
	for _, participantID := range participantIDs {
		if err := client.RemoveThreadMember(context.Background(), threadID, participantID); err != nil {
			return fmt.Errorf("%s: remove group participant %s: %w", providerID, participantID, err)
		}
	}
	return nil
}

func (p *Provider) PromoteGroupAdmins(conversationID string, participantIDs []string) error {
	return p.updateGroupMemberRoles(conversationID, participantIDs, "Admin")
}

func (p *Provider) DemoteGroupAdmins(conversationID string, participantIDs []string) error {
	return p.updateGroupMemberRoles(conversationID, participantIDs, "User")
}

func (p *Provider) updateGroupMemberRoles(conversationID string, participantIDs []string, role string) error {
	client, _, err := p.connectedClient()
	if err != nil {
		return err
	}
	threadID := core.StripConvID(conversationID)
	for _, participantID := range participantIDs {
		if err := client.UpdateThreadMemberRole(context.Background(), threadID, participantID, role); err != nil {
			return fmt.Errorf("%s: update group participant %s role: %w", providerID, participantID, err)
		}
	}
	return nil
}

// GetContactProfile exposes the rich Teams directory card when the participant
// is a person MRI. It is intentionally optional and not part of core.Provider.
func (p *Provider) GetContactProfile(userID string) (models.ContactProfile, error) {
	client, _, err := p.connectedClient()
	if err != nil {
		return models.ContactProfile{}, err
	}
	user, err := client.GetUser(context.Background(), userID)
	if err != nil {
		return models.ContactProfile{}, err
	}
	phones := make([]string, 0, len(user.Phones))
	for _, phone := range user.Phones {
		if strings.TrimSpace(phone.Number) != "" {
			phones = append(phones, phone.Number)
		}
	}
	emails := []string{}
	if user.Email != "" {
		emails = append(emails, user.Email)
	}
	avatarURL := p.cachedAvatar(client, userID)
	return models.ContactProfile{
		UserID: userID, DisplayName: user.DisplayName, AvatarURL: avatarURL,
		Protocol: providerID, ProviderInstanceID: p.instance,
		PhoneNumbers: phones, Emails: emails, Company: user.Company,
		JobTitle: user.JobTitle, Department: user.Department, Address: user.Office,
		ProviderFields: map[string]string{},
	}, nil
}

func (p *Provider) GetConversationHistory(conversationID string, limit int, before, since *time.Time) ([]models.Message, error) {
	conversationID = core.StripConvID(conversationID)
	client, _, err := p.connectedClient()
	if err != nil {
		return nil, err
	}
	if limit < 0 {
		return nil, fmt.Errorf("%s: limit must not be negative", providerID)
	}
	maxMessages := limit
	if maxMessages == 0 {
		maxMessages = 2000
	}
	var cursor string
	var beforeValue time.Time
	if before != nil {
		beforeValue = *before
	}
	out := make([]models.Message, 0, min(maxMessages, 200))
	for page := 0; len(out) < maxMessages && page < 500; page++ {
		pageSize := min(100, maxMessages-len(out))
		result, err := client.FetchHistory(context.Background(), conversationID, msteams.HistoryOptions{
			Before: beforeValue, Limit: pageSize, Cursor: cursor,
		})
		if err != nil {
			return nil, fmt.Errorf("%s: fetch history for %s: %w", providerID, conversationID, err)
		}
		senders := make([]string, 0, len(result.Messages))
		for _, remote := range result.Messages {
			if remote.From != "" && remote.From != client.UserMRI() && client.CachedDisplayName(remote.From) == "" {
				senders = append(senders, remote.From)
			}
		}
		p.cacheDisplayNames(client, senders)
		for _, remote := range result.Messages {
			message := p.toModelMessage(client, remote, conversationID)
			if before != nil && !message.Timestamp.Before(*before) {
				continue
			}
			if since != nil && message.Timestamp.Before(*since) {
				continue
			}
			out = append(out, message)
		}
		if !result.HasMore || result.Next == "" || result.Next == cursor {
			break
		}
		if since != nil {
			oldest := oldestTeamsMessageTime(result.Messages)
			if !oldest.IsZero() && oldest.Before(*since) {
				break
			}
		}
		cursor = result.Next
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	p.enrichReplyMetadata(out)
	return out, nil
}

func oldestTeamsMessageTime(messages []msteams.Message) time.Time {
	var oldest time.Time
	for _, message := range messages {
		if message.Created.IsZero() || (!oldest.IsZero() && !message.Created.Before(oldest)) {
			continue
		}
		oldest = message.Created
	}
	return oldest
}

func (p *Provider) SendMessage(conversationID, text string, file *core.Attachment, threadID *string) (*models.Message, error) {
	if threadID != nil {
		return nil, unsupported("threads")
	}
	if strings.TrimSpace(text) == "" && file == nil {
		return nil, fmt.Errorf("%s: message has no text or attachment", providerID)
	}
	rawConvID := core.StripConvID(conversationID)
	nsConvID := core.BuildConvID(p.instance, rawConvID)
	client, _, err := p.connectedClient()
	if err != nil {
		return nil, err
	}
	opts := msteams.SendOptions{ContentType: "html"}
	content := msteams.MatrixToTeamsHTML(messageformat.TeamsHTML(text))
	var modelAttachment *models.Attachment
	if file != nil {
		if len(file.Data) == 0 {
			return nil, fmt.Errorf("%s: attachment is empty", providerID)
		}
		mimeType := teamsUploadMimeType(file.FileName, file.MimeType)
		isInlineMedia := strings.HasPrefix(mimeType, "image/") || strings.HasPrefix(mimeType, "video/") ||
			strings.HasPrefix(mimeType, "audio/")
		if isInlineMedia {
			uploaded, uploadErr := client.UploadAttachment(context.Background(), file.FileName, mimeType, file.Data)
			if uploadErr != nil {
				return nil, fmt.Errorf("%s: upload attachment: %w", providerID, uploadErr)
			}
			p.rememberAttachmentURL(uploaded.URL)
			content = teamsAttachmentHTML(uploaded, content)
			opts.ContentType = "html"
			attachmentType, _ := teamsAttachmentType(uploaded.Name, uploaded.ContentType)
			modelAttachment = &models.Attachment{
				Type: attachmentType, URL: uploaded.URL, FileName: uploaded.Name,
				FileSize: uploaded.Size, MimeType: uploaded.ContentType,
			}
		} else {
			location, locationErr := p.sharePointUploadLocation(client)
			if locationErr != nil {
				return nil, locationErr
			}
			recipients, recipientsErr := p.sharePointRecipients(client, rawConvID)
			if recipientsErr != nil {
				return nil, recipientsErr
			}
			uploaded, uploadErr := client.UploadSharedFile(context.Background(), location, file.FileName, file.Data, recipients)
			if uploadErr != nil {
				return nil, fmt.Errorf("%s: upload document: %w", providerID, uploadErr)
			}
			p.rememberSharedFile(uploaded.FileURL, *uploaded)
			p.rememberSharedFile(uploaded.ShareURL, *uploaded)
			opts.SharedFiles = []msteams.SharedFile{*uploaded}
			modelAttachment = &models.Attachment{
				Type: "document", URL: uploaded.ShareURL, FileName: uploaded.Name,
				FileSize: uploaded.Size, MimeType: mimeType,
			}
		}
	}
	id, err := client.SendMessage(context.Background(), rawConvID, content, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: send message: %w", providerID, err)
	}
	now := time.Now()
	message := &models.Message{
		ProtocolConvID: nsConvID, ProtocolMsgID: id,
		SenderID: client.UserMRI(), SenderName: p.displayName(),
		Body: text, Timestamp: now, IsFromMe: true,
	}
	if modelAttachment != nil {
		if raw, err := json.Marshal([]models.Attachment{*modelAttachment}); err == nil {
			message.Attachments = string(raw)
		}
	}
	if err := p.storeMessages([]models.Message{*message}); err != nil {
		return nil, err
	}
	return message, nil
}

func (p *Provider) SendFile(conversationID string, file *core.Attachment, threadID *string) (*models.Message, error) {
	if file == nil {
		return nil, fmt.Errorf("%s: no attachment provided", providerID)
	}
	return p.SendMessage(conversationID, "", file, threadID)
}

func teamsUploadMimeType(name, contentType string) string {
	contentType = strings.TrimSpace(strings.Split(contentType, ";")[0])
	if contentType == "" || contentType == "application/octet-stream" {
		if inferred := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); inferred != "" {
			return strings.Split(inferred, ";")[0]
		}
	}
	if contentType == "" {
		return "application/octet-stream"
	}
	return contentType
}

func teamsAttachmentHTML(attachment *msteams.Attachment, caption string) string {
	if attachment == nil {
		return caption
	}
	name := html.EscapeString(attachment.Name)
	fileURL := html.EscapeString(attachment.URL)
	var attachmentHTML string
	switch {
	case strings.HasPrefix(attachment.ContentType, "image/"):
		attachmentHTML = fmt.Sprintf(
			`<p><img itemscope="image" style="vertical-align:bottom" src="%s" alt="%s" `+
				`itemtype="http://schema.skype.com/AMSImage" id="%s" itemid="%s" `+
				`href="%s" target-src="%s"></p>`,
			fileURL, name, html.EscapeString(attachment.ID), html.EscapeString(attachment.ID), fileURL, fileURL,
		)
	case strings.HasPrefix(attachment.ContentType, "video/"):
		attachmentHTML = fmt.Sprintf(
			`<video src="%s" itemscope="" itemtype="http://schema.skype.com/AMSVideo">%s</video>`,
			fileURL, name,
		)
	default:
		attachmentHTML = fmt.Sprintf(
			`<URIObject type="File.1" url_thumbnail="" uri="%s" url="%s">`+
				`<a href="%s">%s</a><OriginalName v="%s"/><FileSize v="%d"/></URIObject>`,
			fileURL, fileURL, fileURL, name, name, attachment.Size,
		)
	}
	if strings.TrimSpace(caption) == "" {
		return attachmentHTML
	}
	return caption + "<br>" + attachmentHTML
}

func (p *Provider) SendReply(conversationID, text, quotedMessageID string) (*models.Message, error) {
	if quotedMessageID == "" {
		return nil, fmt.Errorf("%s: quoted message ID is empty", providerID)
	}
	rawConvID := core.StripConvID(conversationID)
	nsConvID := core.BuildConvID(p.instance, rawConvID)
	client, _, err := p.connectedClient()
	if err != nil {
		return nil, err
	}
	content := p.inlineReplyHTML(nsConvID, quotedMessageID) + msteams.MatrixToTeamsHTML(messageformat.TeamsHTML(text))
	id, err := client.SendMessage(context.Background(), rawConvID, content, msteams.SendOptions{ContentType: "html"})
	if err != nil {
		return nil, fmt.Errorf("%s: send reply: %w", providerID, err)
	}
	now := time.Now()
	message := &models.Message{
		ProtocolConvID: nsConvID, ProtocolMsgID: id,
		SenderID: client.UserMRI(), SenderName: p.displayName(), Body: text,
		Timestamp: now, IsFromMe: true, QuotedMessageID: &quotedMessageID,
	}
	replies := []models.Message{*message}
	p.enrichReplyMetadata(replies)
	*message = replies[0]
	if err := p.storeMessages(replies); err != nil {
		return nil, err
	}
	return message, nil
}

func (p *Provider) SendThreadReply(conversationID, text, threadID, quotedMessageID string) (*models.Message, error) {
	return nil, unsupported("threads")
}

func (p *Provider) inlineReplyHTML(conversationID, quotedMessageID string) string {
	senderID := ""
	senderName := ""
	preview := "…"
	if db.DB != nil {
		var quoted models.Message
		if err := db.DB.Where(
			"protocol_msg_id = ? AND protocol_conv_id = ?",
			quotedMessageID, conversationID,
		).First(&quoted).Error; err == nil {
			senderID = quoted.SenderID
			senderName = quoted.SenderName
			if senderName == "" {
				senderName = senderID
			}
			if strings.TrimSpace(quoted.Body) != "" {
				preview = quoted.Body
				if looksLikeTeamsHTML(preview) {
					preview = teamsHTMLToMarkdown(
						msteams.StripAMSAttachments(msteams.StripReplyBlockquote(preview)),
					)
				}
			}
		}
	}
	return fmt.Sprintf(
		`<blockquote itemscope="" itemtype="http://schema.skype.com/Reply" itemid=%q>`+
			`<strong itemprop="mri" itemid=%q>%s</strong>`+
			`<span itemprop="time" itemid=%q></span><p itemprop="preview">%s</p></blockquote>`,
		quotedMessageID,
		senderID,
		html.EscapeString(senderName),
		quotedMessageID,
		html.EscapeString(preview),
	)
}

func (p *Provider) EditMessage(conversationID, messageID, newText string) (*models.Message, error) {
	rawConvID := core.StripConvID(conversationID)
	client, _, err := p.connectedClient()
	if err != nil {
		return nil, err
	}
	if err := client.EditMessage(context.Background(), rawConvID, messageID, msteams.MatrixToTeamsHTML(messageformat.TeamsHTML(newText)), msteams.SendOptions{ContentType: "html"}); err != nil {
		return nil, fmt.Errorf("%s: edit message: %w", providerID, err)
	}
	now := time.Now()
	return &models.Message{ProtocolConvID: core.BuildConvID(p.instance, rawConvID), ProtocolMsgID: messageID, Body: newText, IsEdited: true, EditedTimestamp: &now}, nil
}

func (p *Provider) DeleteMessage(conversationID, messageID string) error {
	client, _, err := p.connectedClient()
	if err != nil {
		return err
	}
	return client.DeleteMessage(context.Background(), core.StripConvID(conversationID), messageID)
}

func (p *Provider) AddReaction(conversationID, messageID, emoji string) error {
	client, _, err := p.connectedClient()
	if err != nil {
		return err
	}
	return client.AddReaction(context.Background(), core.StripConvID(conversationID), messageID, emoji)
}

func (p *Provider) RemoveReaction(conversationID, messageID, emoji string) error {
	client, _, err := p.connectedClient()
	if err != nil {
		return err
	}
	return client.RemoveReaction(context.Background(), core.StripConvID(conversationID), messageID, emoji)
}

func (p *Provider) SendTypingIndicator(conversationID string, isTyping bool) error {
	client, _, err := p.connectedClient()
	if err != nil {
		return err
	}
	threadID := core.StripConvID(conversationID)
	if isTyping {
		return client.SendTyping(context.Background(), threadID)
	}
	return client.SendClearTyping(context.Background(), threadID)
}

func (p *Provider) MarkMessageAsRead(conversationID, messageID string) error {
	client, _, err := p.connectedClient()
	if err != nil {
		return err
	}
	return client.MarkRead(context.Background(), core.StripConvID(conversationID), messageID)
}

func (p *Provider) MarkConversationAsRead(conversationID string) error {
	if db.DB == nil {
		return nil
	}
	var message models.Message
	if err := db.DB.Where("protocol_conv_id = ?", conversationID).Order("timestamp DESC").First(&message).Error; err != nil {
		return err
	}
	return p.MarkMessageAsRead(conversationID, message.ProtocolMsgID)
}

func (p *Provider) GetContactName(contactID string) (string, error) {
	client, _, err := p.connectedClient()
	if err != nil {
		return "", err
	}
	if cached := client.CachedDisplayName(contactID); cached != "" {
		return cached, nil
	}
	user, err := client.GetUser(context.Background(), contactID)
	if err != nil {
		return "", err
	}
	return user.DisplayName, nil
}

func (p *Provider) RefreshContact(contactID string) error {
	_, err := p.GetContactName(contactID)
	return err
}

func (p *Provider) GetCapabilities() core.Capabilities {
	return core.Capabilities{
		SupportsThreads: false, SupportsReactions: true,
		SupportsTypingIndicator: true, SupportsDeleteMessage: true,
		SupportsEditMessage: true, SupportsReadReceipts: true,
		SupportsPinMessage: true, SupportsListMessagePins: true,
		SupportsScheduledMessages: true, SupportsListScheduledMessages: true,
		MessagePinScope:            string(models.MessagePinScopeShared),
		SupportsGroupManagement:    true,
		SupportsLeaveGroup:         true,
		SupportsAddGroupMembers:    true,
		SupportsRemoveGroupMembers: true,
		SupportsRenameGroup:        true,
		SupportsGroupDescription:   true,
		SupportsGroupAdminRoles:    true,
		NativeEmojiReactions:       true,
		SupportsContactDirectory:   true, SupportsDirectConversation: true,
		SupportsGroupConversation: true, SupportsGroupTitle: true,
		RequiresGroupTitle: false, GroupConversationTypes: "group",
	}
}

func (p *Provider) Cleanup() error {
	_ = p.Disconnect()
	path := p.sessionPath()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (p *Provider) connectedClient() (*msteams.Client, string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.client == nil || !p.client.IsLoggedIn() {
		return nil, p.instance, fmt.Errorf("%s: not connected", providerID)
	}
	return p.client, p.instance, nil
}

func (p *Provider) linkedAccount(client *msteams.Client, chat msteams.Chat) models.LinkedAccount {
	name := strings.TrimSpace(chat.Topic)
	isGroup := chat.Type != msteams.ChatType1on1
	if name == "" || isTechnicalConversationName(name, chat.ID) {
		name = p.participantDisplayName(client, chat)
	}
	if name == "" {
		if isGroup {
			name = "Group conversation"
		} else {
			name = chat.ID
		}
	}
	userID := chat.ID
	if !isGroup {
		members := chat.Members
		if len(members) == 0 {
			if detailed, err := client.GetChat(context.Background(), chat.ID); err == nil {
				members = detailed.Members
			}
		}
		for _, member := range members {
			if member.MRI != "" && !strings.EqualFold(member.MRI, client.UserMRI()) {
				userID = member.MRI
				break
			}
		}
		if userID == chat.ID {
			userID = teamsDMParticipantMRI(chat.ID, client.UserMRI())
		}
		if userID != "" && userID != chat.ID && isTechnicalConversationName(name, chat.ID) {
			name = client.CachedDisplayName(userID)
			if name == "" {
				if profile, err := client.GetUser(context.Background(), userID); err == nil {
					name = profile.DisplayName
				}
			}
		}
	}
	return models.LinkedAccount{
		Protocol: providerID, ProviderInstanceID: p.instance,
		UserID: userID, Username: name, AvatarURL: p.conversationAvatar(client, chat), IsGroup: isGroup,
		ConversationID: chat.ID,
	}
}

func teamsDMParticipantMRI(threadID, selfMRI string) string {
	if !strings.HasPrefix(threadID, "19:") || !strings.HasSuffix(threadID, "@unq.gbl.spaces") {
		return threadID
	}
	body := strings.TrimSuffix(strings.TrimPrefix(threadID, "19:"), "@unq.gbl.spaces")
	parts := strings.Split(body, "_")
	if len(parts) != 2 {
		return threadID
	}
	self := strings.TrimPrefix(strings.ToLower(selfMRI), "8:orgid:")
	for _, part := range parts {
		if strings.ToLower(part) != self {
			return "8:orgid:" + part
		}
	}
	return threadID
}

func isVirtualTeamsThread(threadID string) bool {
	switch strings.ToLower(strings.TrimSpace(threadID)) {
	case "48:calllogs", "48:mentions", "48:notifications", "48:notes", "48:drafts":
		return true
	default:
		return false
	}
}

func isTechnicalConversationName(name, threadID string) bool {
	name = strings.TrimSpace(name)
	return name == "" || strings.EqualFold(name, strings.TrimSpace(threadID)) ||
		strings.HasPrefix(strings.ToLower(name), "19:")
}

func participantNamesFromMessages(messages []models.Message, selfMRI string) string {
	names := make([]string, 0)
	seen := make(map[string]struct{})
	for _, message := range messages {
		name := strings.TrimSpace(message.SenderName)
		if name == "" || strings.EqualFold(message.SenderID, selfMRI) || isTechnicalConversationName(name, message.SenderID) {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func participantNamesFromStoredMessages(conversationID, selfMRI string) string {
	if db.DB == nil || conversationID == "" {
		return ""
	}
	var rows []struct {
		SenderID   string
		SenderName string
	}
	if err := db.DB.Model(&models.Message{}).
		Select("sender_id, sender_name").
		Where("protocol_conv_id = ? AND sender_name <> ''", conversationID).
		Group("sender_id, sender_name").
		Scan(&rows).Error; err != nil {
		return ""
	}
	messages := make([]models.Message, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, models.Message{SenderID: row.SenderID, SenderName: row.SenderName})
	}
	return participantNamesFromMessages(messages, selfMRI)
}

func (p *Provider) participantDisplayName(client *msteams.Client, chat msteams.Chat) string {
	members := chat.Members
	if len(members) == 0 {
		if detailed, err := client.GetChat(context.Background(), chat.ID); err == nil {
			members = detailed.Members
		}
	}
	mris := make([]string, 0, len(members))
	for _, member := range members {
		if member.MRI != "" && !strings.EqualFold(member.MRI, client.UserMRI()) {
			mris = append(mris, member.MRI)
			if member.DisplayName != "" {
				client.CacheDisplayName(member.MRI, member.DisplayName)
			}
		}
	}
	if len(mris) == 0 {
		return ""
	}

	p.cacheDisplayNames(client, mris)

	names := make([]string, 0, len(mris))
	seen := make(map[string]struct{}, len(mris))
	for _, mri := range mris {
		name := strings.TrimSpace(client.CachedDisplayName(mri))
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func (p *Provider) cacheDisplayNames(client *msteams.Client, mris []string) {
	if len(mris) == 0 {
		return
	}
	unique := make([]string, 0, len(mris))
	seen := make(map[string]struct{}, len(mris))
	for _, mri := range mris {
		key := mriLookupKey(mri)
		if key == "" || client.CachedDisplayName(mri) != "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, mri)
	}
	if len(unique) == 0 {
		return
	}
	profiles, err := client.FetchShortProfiles(context.Background(), unique)
	if err != nil {
		return
	}
	byMRI := make(map[string]*msteams.User, len(profiles))
	for index := range profiles {
		profile := &profiles[index]
		if profile.DisplayName == "" {
			continue
		}
		byMRI[mriLookupKey(profile.MRI)] = profile
		if profile.ObjectID != "" {
			byMRI[mriLookupKey(profile.ObjectID)] = profile
		}
		client.CacheUserProfile(profile)
	}
	for index, mri := range unique {
		profile := byMRI[mriLookupKey(mri)]
		// Some tenant variants omit the MRI and preserve request order.
		if profile == nil && len(profiles) == len(unique) && profiles[index].DisplayName != "" {
			profile = &profiles[index]
		}
		if profile != nil {
			client.CacheDisplayName(mri, profile.DisplayName)
			continue
		}
		// Guests and federated users are sometimes omitted from the batch
		// response but can still be resolved through their detailed people card.
		if detailed, err := client.GetUser(context.Background(), mri); err == nil && detailed.DisplayName != "" {
			client.CacheUserProfile(detailed)
			client.CacheDisplayName(mri, detailed.DisplayName)
		}
	}
}

func mriLookupKey(mri string) string {
	value := strings.ToLower(strings.TrimSpace(mri))
	value = strings.TrimPrefix(value, "8:orgid:")
	return strings.ReplaceAll(value, "-", "")
}

func (p *Provider) toModelMessage(client *msteams.Client, remote msteams.Message, conversationID string) models.Message {
	conversationID = core.BuildConvID(p.instance, core.StripConvID(conversationID))
	if isTeamsCallMessage(remote) {
		return p.toCallModelMessage(client, remote, conversationID)
	}
	body := remote.Content
	embeddedAttachments := msteams.ExtractAMSAttachments(remote.Content)
	if remote.ContentType == "html" ||
		strings.Contains(strings.ToLower(remote.MessageType), "richtext") ||
		looksLikeTeamsHTML(remote.Content) {
		if remote.ParentID == "" {
			remote.ParentID = msteams.ExtractReplyParent(remote.Content)
		}
		cleanContent := msteams.StripAMSAttachments(msteams.StripReplyBlockquote(remote.Content))
		body = teamsHTMLToMarkdown(cleanContent)
	}
	timestamp := remote.Created
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	senderName := strings.TrimSpace(remote.DisplayName)
	if senderName != "" && !isTechnicalConversationName(senderName, remote.From) {
		client.CacheDisplayName(remote.From, senderName)
	} else {
		senderName = client.CachedDisplayName(remote.From)
	}
	message := models.Message{
		ProtocolConvID: conversationID, ProtocolMsgID: remote.ID,
		SenderID: remote.From, SenderName: senderName,
		SenderAvatarURL: p.cachedAvatar(client, remote.From),
		Body:            body, Timestamp: timestamp, IsFromMe: remote.From == client.UserMRI(),
	}
	if remote.ParentID != "" {
		parent := remote.ParentID
		message.QuotedMessageID = &parent
	}
	attachments := make([]models.Attachment, 0, len(remote.Attachments)+len(remote.SharedFiles))
	seenAttachmentURLs := make(map[string]struct{})
	for _, attachment := range remote.Attachments {
		p.rememberAttachmentURL(attachment.URL)
		seenAttachmentURLs[attachment.URL] = struct{}{}
		attachmentType, contentType := teamsAttachmentType(attachment.Name, attachment.ContentType)
		attachments = append(attachments, models.Attachment{
			Type: attachmentType, URL: attachment.URL, FileName: attachment.Name,
			FileSize: attachment.Size, MimeType: contentType,
		})
	}
	for _, file := range remote.SharedFiles {
		fileURL := firstNonEmpty(file.ShareURL, file.FileURL)
		p.rememberSharedFile(fileURL, file)
		seenAttachmentURLs[fileURL] = struct{}{}
		attachmentType, contentType := teamsAttachmentType(file.Name, "")
		attachments = append(attachments, models.Attachment{
			Type: attachmentType, URL: fileURL, FileName: file.Name,
			FileSize: file.Size, MimeType: contentType,
		})
	}
	for _, embedded := range embeddedAttachments {
		if embedded.URL == "" {
			continue
		}
		if _, exists := seenAttachmentURLs[embedded.URL]; exists {
			continue
		}
		p.rememberAttachmentURL(embedded.URL)
		attachmentType, contentType := teamsAttachmentType(embedded.AltText, "")
		if embedded.IsImage {
			attachmentType = "image"
		}
		if embedded.IsVideo {
			attachmentType = "video"
		}
		attachments = append(attachments, models.Attachment{
			Type: attachmentType, URL: embedded.URL, FileName: embedded.AltText,
			MimeType: contentType, Duration: uint32(embedded.Duration.Seconds()),
		})
	}
	if len(attachments) > 0 {
		if raw, err := json.Marshal(attachments); err == nil {
			message.Attachments = string(raw)
		}
	}
	for _, reaction := range remote.Reactions {
		message.Reactions = append(message.Reactions, models.Reaction{
			UserID: reaction.UserID, Emoji: msteams.DecodeReactionKey(reaction.Type),
			CreatedAt: reaction.Time, UpdatedAt: reaction.Time,
		})
	}
	return message
}

var teamsHTMLContentPattern = regexp.MustCompile(`(?i)<(?:p|div|br|ul|ol|li|table|blockquote|span|strong|em|i|b|a)\b`)

func looksLikeTeamsHTML(content string) bool {
	return teamsHTMLContentPattern.MatchString(content)
}

func teamsAttachmentType(name, contentType string) (string, string) {
	contentType = strings.TrimSpace(strings.Split(contentType, ";")[0])
	if contentType == "" {
		contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	}
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return "image", contentType
	case strings.HasPrefix(contentType, "video/"):
		return "video", contentType
	case strings.HasPrefix(contentType, "audio/"):
		return "audio", contentType
	default:
		return "document", contentType
	}
}

var (
	callEventTypePattern   = regexp.MustCompile(`(?is)<callEventType>\s*([^<]+)\s*</callEventType>`)
	callDisplayNamePattern = regexp.MustCompile(`(?is)<displayName>\s*([^<]+)\s*</displayName>`)
	callDurationPattern    = regexp.MustCompile(`(?is)<duration>\s*(\d+)\s*</duration>`)
	teamsMeetingURLPattern = regexp.MustCompile(`https://teams\.microsoft\.com/(?:meet/|l/meetup-join/)[^\s<"'\\]+`)
)

func isTeamsCallMessage(message msteams.Message) bool {
	if message.CallLog != nil {
		return true
	}
	switch message.MessageType {
	case "Event/Call", "RichText/Media_Call", "ThreadActivity/CallStarted",
		"ThreadActivity/CallEnded", "ThreadActivity/CallRecordingFinished":
		return true
	default:
		return false
	}
}

func (p *Provider) toCallModelMessage(client *msteams.Client, remote msteams.Message, conversationID string) models.Message {
	message := models.Message{
		ProtocolConvID: conversationID, ProtocolMsgID: remote.ID,
		SenderID: remote.From, SenderName: client.CachedDisplayName(remote.From),
		Timestamp: remote.Created, IsFromMe: remote.From == client.UserMRI(),
		CallType: "scheduled_start", Body: "Call started",
	}
	if message.Timestamp.IsZero() {
		message.Timestamp = time.Now()
	}
	eventType := strings.ToLower(remote.MessageType)
	if match := callEventTypePattern.FindStringSubmatch(remote.Content); len(match) > 1 {
		eventType = strings.ToLower(strings.TrimSpace(match[1]))
	}
	if strings.Contains(eventType, "ended") || strings.Contains(remote.Content, "<ended") {
		message.CallType = "call_ended"
		// This is a conversation-wide meeting event. It does not prove that the
		// authenticated user joined; personal CallLog records carry that signal.
		message.CallOutcome = "ENDED"
		message.Body = "Call ended"
	}
	if remote.CallLog != nil {
		message.CallOutcome = strings.ToUpper(remote.CallLog.State)
		if remote.CallLog.State == "missed" {
			message.CallType = "missed_voice"
			message.CallOutcome = "MISSED"
			message.Body = "Missed call"
		}
		if !remote.CallLog.EndTime.IsZero() && !remote.CallLog.ConnectTime.IsZero() {
			seconds := int32(remote.CallLog.EndTime.Sub(remote.CallLog.ConnectTime).Seconds())
			if seconds > 0 {
				message.CallDurationSecs = &seconds
			}
		}
	}
	maxDuration := int64(0)
	for _, match := range callDurationPattern.FindAllStringSubmatch(remote.Content, -1) {
		if len(match) > 1 {
			if seconds, err := strconv.ParseInt(match[1], 10, 32); err == nil && seconds > maxDuration {
				maxDuration = seconds
			}
		}
	}
	if maxDuration > 0 {
		seconds := int32(maxDuration)
		message.CallDurationSecs = &seconds
	}
	participants := make([]string, 0)
	seen := make(map[string]struct{})
	for _, match := range callDisplayNamePattern.FindAllStringSubmatch(remote.Content, -1) {
		if len(match) < 2 {
			continue
		}
		name := strings.TrimSpace(html.UnescapeString(match[1]))
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		participants = append(participants, name)
	}
	if len(participants) > 0 {
		raw, _ := json.Marshal(participants)
		message.CallParticipants = string(raw)
	}
	decodedContent := html.UnescapeString(remote.Content)
	if match := teamsMeetingURLPattern.FindString(decodedContent); match != "" {
		message.CallUrl = match
		message.CallLinkAction = "join"
	} else if rawConversationID := core.StripConvID(conversationID); strings.HasPrefix(rawConversationID, "19:") {
		// Ad-hoc calls generally expose only an internal flightproxy URL, which
		// cannot be opened anonymously. Navigating to the chat lets Teams show
		// the active-call banner and its native Join action.
		message.CallUrl = "https://teams.microsoft.com/l/chat/" +
			url.PathEscape(rawConversationID) + "/conversations"
		message.CallLinkAction = "open"
	}
	return message
}

func (p *Provider) forwardEvents(ctx context.Context, client *msteams.Client) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-client.Events():
			if !ok {
				return
			}
			p.handleRemoteEvent(client, event)
		}
	}
}

func (p *Provider) handleRemoteEvent(client *msteams.Client, event msteams.Event) {
	switch event.Type {
	case msteams.EventTypeNewMessage, msteams.EventTypeEditMessage, msteams.EventTypeDeleteMessage, msteams.EventTypeCall:
		if event.Message == nil {
			return
		}
		if discovered, err := p.ensureConversationStored(client, event.ThreadID); err == nil && discovered {
			p.emit(core.ContactStatusEvent{
				InstanceID: p.instance, UserID: "refresh",
				Status: "new_conversations_discovered",
			})
		}
		message := p.toModelMessage(client, *event.Message, event.ThreadID)
		messages := []models.Message{message}
		p.enrichReplyMetadata(messages)
		message = messages[0]
		if event.Type == msteams.EventTypeEditMessage {
			message.IsEdited = true
			edited := event.Timestamp
			message.EditedTimestamp = &edited
		}
		if event.Type == msteams.EventTypeDeleteMessage {
			message.IsDeleted = true
			deleted := event.Timestamp
			message.DeletedTimestamp = &deleted
		}
		_ = p.storeMessages([]models.Message{message})
		p.emit(core.MessageEvent{InstanceID: p.instance, Message: message})
	case msteams.EventTypeReaction:
		if event.Message == nil {
			return
		}
		conversationID := core.BuildConvID(p.instance, event.ThreadID)
		current := make([]models.Reaction, 0, len(event.Message.Reactions))
		for _, reaction := range event.Message.Reactions {
			current = append(current, models.Reaction{
				UserID: reaction.UserID, Emoji: msteams.DecodeReactionKey(reaction.Type),
				CreatedAt: reaction.Time, UpdatedAt: reaction.Time,
			})
		}
		previous, err := p.replaceStoredReactions(conversationID, event.Message.ID, current)
		if err != nil {
			return
		}
		previousKeys := make(map[string]models.Reaction, len(previous))
		currentKeys := make(map[string]models.Reaction, len(current))
		for _, reaction := range previous {
			previousKeys[reaction.UserID+"\x00"+reaction.Emoji] = reaction
		}
		for _, reaction := range current {
			key := reaction.UserID + "\x00" + reaction.Emoji
			currentKeys[key] = reaction
			if _, exists := previousKeys[key]; !exists {
				p.emit(core.ReactionEvent{
					InstanceID: p.instance, ConversationID: conversationID,
					MessageID: event.Message.ID, UserID: reaction.UserID,
					Emoji: reaction.Emoji, Added: true, Timestamp: reaction.CreatedAt.Unix(),
				})
			}
		}
		for key, reaction := range previousKeys {
			if _, exists := currentKeys[key]; !exists {
				p.emit(core.ReactionEvent{
					InstanceID: p.instance, ConversationID: conversationID,
					MessageID: event.Message.ID, UserID: reaction.UserID,
					Emoji: reaction.Emoji, Added: false, Timestamp: event.Timestamp.Unix(),
				})
			}
		}
	case msteams.EventTypeTyping:
		if event.TypingFrom == "" || event.TypingFrom == client.UserMRI() {
			return
		}
		p.emit(core.TypingEvent{
			InstanceID: p.instance, ConversationID: core.BuildConvID(p.instance, event.ThreadID),
			UserID: event.TypingFrom, UserName: client.CachedDisplayName(event.TypingFrom),
			IsTyping: !event.TypingStop,
		})
	case msteams.EventTypeChatUpdate:
		p.emit(core.ContactStatusEvent{InstanceID: p.instance, UserID: "refresh", Status: "new_conversations_discovered"})
	}
}

func (p *Provider) watchConversationList(ctx context.Context, client *msteams.Client) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			discovered, err := p.discoverNewConversations(ctx, client)
			if err == nil && discovered > 0 {
				p.emit(core.ContactStatusEvent{
					InstanceID: p.instance, UserID: "refresh",
					Status: "new_conversations_discovered",
				})
			}
		}
	}
}

func (p *Provider) watchPresence(ctx context.Context, client *msteams.Client) {
	p.pollPresence(ctx, client)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollPresence(ctx, client)
		}
	}
}

func (p *Provider) pollPresence(ctx context.Context, client *msteams.Client) {
	chats, err := client.ListChats(ctx)
	if err != nil {
		return
	}
	chatIDsByMRI := make(map[string][]string)
	mris := make([]string, 0)
	for _, chat := range chats {
		if chat.Type != msteams.ChatType1on1 {
			continue
		}
		for _, member := range chat.Members {
			if member.MRI == "" || strings.EqualFold(member.MRI, client.UserMRI()) {
				continue
			}
			if _, exists := chatIDsByMRI[member.MRI]; !exists {
				mris = append(mris, member.MRI)
			}
			chatIDsByMRI[member.MRI] = append(chatIDsByMRI[member.MRI], chat.ID)
		}
	}
	changed := false
	const batchSize = 100
	for start := 0; start < len(mris); start += batchSize {
		end := min(start+batchSize, len(mris))
		presences, err := client.GetPresences(ctx, mris[start:end])
		if err != nil {
			return
		}
		for _, presence := range presences {
			status := teamsPresenceStatus(presence.Availability, presence.Activity)
			if len(chatIDsByMRI[presence.MRI]) > 0 {
				updated, err := p.updateConversationPresence(presence.MRI, status, presence.Activity)
				if err == nil && updated {
					changed = true
				}
			}
		}
	}
	if changed {
		p.emit(core.ContactStatusEvent{
			InstanceID: p.instance, UserID: "refresh", Status: "message_received",
		})
	}
}

func teamsPresenceStatus(availability, activity string) string {
	switch strings.ToLower(strings.TrimSpace(activity)) {
	case "inameeting":
		return "meeting"
	case "inacall":
		return "busy"
	case "presenting":
		return "dnd"
	}
	switch strings.ToLower(strings.TrimSpace(availability)) {
	case "available", "availableidle":
		return "online"
	case "away", "berightback":
		return "away"
	case "busy", "busyidle", "inacall", "inameeting":
		return "busy"
	case "donotdisturb", "presenting":
		return "dnd"
	default:
		return "offline"
	}
}

func (p *Provider) emit(event core.ProviderEvent) {
	select {
	case p.eventChan <- event:
	default:
	}
}

func (p *Provider) displayName() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.session == nil {
		return ""
	}
	return p.session.DisplayName
}

func (p *Provider) updateSessionFromClientLocked(client *msteams.Client) {
	if p.session == nil {
		return
	}
	p.session.RefreshToken = client.SnapshotRefresh()
	auth, skype := client.SnapshotTokens()
	if auth != nil {
		p.session.AuthToken = auth.Value
	}
	if skype != nil {
		p.session.SkypeToken = skype.Value
	}
	p.session.ChatSvcBase = client.ChatSvcBase()
}

func (p *Provider) sessionPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Loom", p.instance, "teams-session.json")
}

func (p *Provider) loadSessionLocked() error {
	path := p.sessionPath()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: read session: %w", providerID, err)
	}
	var stored session
	if err := json.Unmarshal(raw, &stored); err != nil {
		return fmt.Errorf("%s: decode session: %w", providerID, err)
	}
	p.session = &stored
	return nil
}

func (p *Provider) saveSessionLocked() error {
	if p.session == nil {
		return fmt.Errorf("%s: no session to save", providerID)
	}
	path := p.sessionPath()
	if path == "" {
		return fmt.Errorf("%s: user configuration directory is unavailable", providerID)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(p.session)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0600)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
