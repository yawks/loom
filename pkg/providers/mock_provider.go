// Package providers contains implementations of the Provider interface.
package providers

import (
	"Loom/pkg/core"
	"Loom/pkg/models"
	cryptoRand "crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
)

// MockProvider is a fake implementation of the Provider interface for development.
type MockProvider struct {
	contacts      []models.LinkedAccount
	conversations map[string]models.Conversation // map[protocolConvId]Conversation
	messages      map[string][]models.Message    // map[protocolConvId][]Message
	reactions     map[string][]models.Reaction   // map[protocolMsgId][]Reaction
	eventChan     chan core.ProviderEvent
	stopChan      chan struct{}
	config        core.ProviderConfig
	mu            sync.RWMutex
	disconnected  bool // Track if already disconnected
}

var loremIpsum = []string{
	"Lorem ipsum dolor sit amet, consectetur adipiscing elit.",
	"Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua.",
	"Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat.",
	"Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur.",
	"Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum.",
}

func secureRandInt(upperBound int) int {
	if upperBound <= 0 {
		return 0
	}
	n, err := cryptoRand.Int(cryptoRand.Reader, big.NewInt(int64(upperBound)))
	if err != nil {
		return 0
	}
	return int(n.Int64())
}

// NewMockProvider creates a new instance of the MockProvider.
func NewMockProvider() *MockProvider {
	return &MockProvider{
		conversations: make(map[string]models.Conversation),
		messages:      make(map[string][]models.Message),
		reactions:     make(map[string][]models.Reaction),
		eventChan:     make(chan core.ProviderEvent, 100),
		stopChan:      make(chan struct{}),
		config:        make(core.ProviderConfig),
	}
}

// Init initializes the mock provider with fake data.
func (m *MockProvider) Init(config core.ProviderConfig) error {
	fmt.Println("MockProvider: Initializing...")
	if config != nil {
		m.config = config
	} else {
		m.config = make(core.ProviderConfig)
	}
	m.generateFakeData()
	go m.simulateRealtimeEvents()
	fmt.Println("MockProvider: Initialized.")
	return nil
}

// GetConfig returns the current configuration of the mock provider.
func (m *MockProvider) GetConfig() core.ProviderConfig {
	return m.config
}

// SetConfig updates the configuration of the mock provider.
func (m *MockProvider) SetConfig(config core.ProviderConfig) error {
	m.config = config
	return nil
}

// GetQRCode returns a QR code string (mock provider doesn't need QR code).
func (m *MockProvider) GetQRCode() (string, error) {
	return "", nil
}

// IsAuthenticated returns true since MockProvider doesn't require authentication.
func (m *MockProvider) IsAuthenticated() bool {
	return true
}

// Connect simulates a connection to the mock provider.
func (m *MockProvider) Connect() error {
	fmt.Println("MockProvider: 'Connecting'...")
	time.Sleep(500 * time.Millisecond) // Simulate a network connection
	fmt.Println("MockProvider: 'Connected'.")
	return nil
}

// Disconnect closes the connection and stops background operations.
func (m *MockProvider) Disconnect() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.disconnected {
		// Already disconnected, skip
		return nil
	}

	fmt.Println("MockProvider: 'Disconnecting'...")

	// Close channels safely
	select {
	case <-m.stopChan:
		// Already closed
	default:
		close(m.stopChan)
	}

	select {
	case <-m.eventChan:
		// Already closed
	default:
		close(m.eventChan)
	}

	m.disconnected = true
	fmt.Println("MockProvider: 'Disconnected'.")
	return nil
}

// SyncHistory simulates syncing message history since a given time.
func (m *MockProvider) SyncHistory(since time.Time) error {
	fmt.Printf("MockProvider: Syncing history since %v...\n", since)
	// TODO: Integrate logic here for `whatsmeow` or `slack-go` to fetch history.
	return nil
}

// StreamEvents returns a channel for receiving real-time events.
func (m *MockProvider) StreamEvents() (<-chan core.ProviderEvent, error) {
	return m.eventChan, nil
}

// SendMessage sends a text message (optionally with a file attachment) to a conversation.
func (m *MockProvider) SendMessage(conversationID string, text string, file *core.Attachment, threadID *string) (*models.Message, error) {
	fmt.Printf("MockProvider: Sending message '%s' to conv %s\n", text, conversationID)

	body := text
	if file != nil {
		body = fmt.Sprintf("%s [File: %s]", text, file.FileName)
	}

	newMessage := models.Message{
		ProtocolMsgID:  fmt.Sprintf("mock-msg-%d", secureRandInt(100000)),
		ProtocolConvID: conversationID,
		SenderID:       "me",
		Body:           body,
		Timestamp:      time.Now(),
		IsFromMe:       true,
		ThreadID:       threadID,
	}

	if file != nil {
		newMessage.Attachments = fmt.Sprintf(`[{"fileName":"%s","mimeType":"%s","fileSize":%d}]`, file.FileName, file.MimeType, file.FileSize)
	}

	if _, ok := m.messages[conversationID]; ok {
		m.messages[conversationID] = append(m.messages[conversationID], newMessage)
	}

	// 1. Send the user's message to the frontend immediately.
	m.eventChan <- core.MessageEvent{Message: newMessage}

	// 2. Simulate an auto-reply after a short delay.
	go func() {
		time.Sleep(time.Duration(secureRandInt(1500)+500) * time.Millisecond) // Wait 0.5-2 seconds

		conv, ok := m.conversations[conversationID]
		if !ok {
			return // Should not happen
		}

		var senderID string
		// If it's a group, pick a random member to reply. Otherwise, it's the contact.
		if conv.IsGroup {
			// Find linked accounts that are not "me"
			possibleSenders := []string{}
			for _, contact := range m.contacts {
				// This is a simplified group logic. A real app would have a participant list.
				if strings.HasPrefix(contact.UserID, "user-") {
					possibleSenders = append(possibleSenders, contact.UserID)
				}
			}
			if len(possibleSenders) > 0 {
				senderID = possibleSenders[secureRandInt(len(possibleSenders))]
			}
		} else {
			// In a direct message, the other person replies.
			// This logic is simplified; it assumes the conversationID is the user ID.
			senderID = conversationID
		}

		if senderID != "" {
			replyMessage := models.Message{
				ProtocolMsgID:  fmt.Sprintf("mock-reply-%d", secureRandInt(100000)),
				ProtocolConvID: conversationID,
				SenderID:       senderID,
				Body:           loremIpsum[secureRandInt(len(loremIpsum))],
				Timestamp:      time.Now(),
				IsFromMe:       false,
			}
			m.messages[conversationID] = append(m.messages[conversationID], replyMessage)
			m.eventChan <- core.MessageEvent{Message: replyMessage}
		}

	}()

	return &newMessage, nil
}

// SendFile sends a file to a conversation without text.
func (m *MockProvider) SendFile(conversationID string, file *core.Attachment, threadID *string) (*models.Message, error) {
	fmt.Printf("MockProvider: Sending file '%s' to conv %s\n", file.FileName, conversationID)

	newMessage := models.Message{
		ProtocolMsgID:  fmt.Sprintf("mock-file-%d", secureRandInt(100000)),
		ProtocolConvID: conversationID,
		SenderID:       "me",
		Body:           fmt.Sprintf("[File: %s]", file.FileName),
		Timestamp:      time.Now(),
		IsFromMe:       true,
		ThreadID:       threadID,
		Attachments:    fmt.Sprintf(`[{"fileName":"%s","mimeType":"%s","fileSize":%d}]`, file.FileName, file.MimeType, file.FileSize),
	}

	if _, ok := m.messages[conversationID]; ok {
		m.messages[conversationID] = append(m.messages[conversationID], newMessage)
	}

	// Send the file message to the frontend immediately.
	m.eventChan <- core.MessageEvent{Message: newMessage}

	return &newMessage, nil
}

// GetThreads loads all messages in a discussion thread from a parent message ID.
func (m *MockProvider) GetThreads(parentMessageID string) ([]models.Message, error) {
	fmt.Printf("MockProvider: Getting threads for message %s\n", parentMessageID)
	// Find all messages that have this message as their ThreadID
	var threadMessages []models.Message
	for _, messages := range m.messages {
		for _, msg := range messages {
			if msg.ThreadID != nil && *msg.ThreadID == parentMessageID {
				threadMessages = append(threadMessages, msg)
			}
		}
	}
	return threadMessages, nil
}

// GetContacts returns the list of contacts with their current status.
func (m *MockProvider) GetContacts() ([]models.LinkedAccount, error) {
	// Return contacts with their current status
	// In a real implementation, this would fetch the actual status from the provider
	contacts := make([]models.LinkedAccount, len(m.contacts))
	copy(contacts, m.contacts)

	// Simulate different statuses
	statuses := []string{"online", "offline", "away", "busy"}
	now := time.Now()
	for i := range contacts {
		if contacts[i].Status == "" {
			contacts[i].Status = statuses[secureRandInt(len(statuses))]
		}
		if contacts[i].Status == "offline" && contacts[i].LastSeen == nil {
			lastSeen := now.Add(-time.Duration(secureRandInt(3600)) * time.Minute)
			contacts[i].LastSeen = &lastSeen
		}
	}

	return contacts, nil
}

// GetConversationHistory retrieves the message history for a specific conversation.
func (m *MockProvider) GetConversationHistory(conversationID string, limit int) ([]models.Message, error) {
	fmt.Printf("MockProvider: Getting conversation history for %s (limit: %d)\n", conversationID, limit)

	messages, ok := m.messages[conversationID]
	if !ok {
		return []models.Message{}, nil
	}

	// Return messages ordered by timestamp (oldest first)
	result := make([]models.Message, len(messages))
	copy(result, messages)

	// Apply limit if specified
	if limit > 0 && limit < len(result) {
		result = result[:limit]
	}

	return result, nil
}

// GetMessages returns the messages for a given conversation.
// Note: This is a helper for the mock provider and not part of the Provider interface.
func (m *MockProvider) GetMessages(conversationID string) []models.Message {
	if messages, ok := m.messages[conversationID]; ok {
		return messages
	}
	return []models.Message{}
}

// --- Mock Utility Functions ---

func (m *MockProvider) generateFakeData() {
	m.contacts = []models.LinkedAccount{
		{UserID: "user-alice", Username: "Alice", Protocol: "mock"},
		{UserID: "user-bob", Username: "Bob", Protocol: "mock"},
		{UserID: "user-charlie", Username: "Charlie", Protocol: "mock"},
		{UserID: "user-jean", Username: "Jean", Protocol: "mock"},
		{UserID: "user-maria", Username: "Maria", Protocol: "mock"},
	}

	// Conversation with Alice
	convAliceID := "user-alice"
	m.conversations[convAliceID] = models.Conversation{ProtocolConvID: convAliceID, IsGroup: false}
	msg1ID := "msg-1"
	msg2ID := "msg-2"
	m.messages[convAliceID] = []models.Message{
		{ProtocolMsgID: msg1ID, SenderID: "user-alice", SenderName: "Alice", Body: "Hi there!", Timestamp: time.Now().Add(-10 * time.Minute)},
		{ProtocolMsgID: msg2ID, SenderID: "me", SenderName: "You", Body: "Hey Alice, how are you?", Timestamp: time.Now().Add(-9 * time.Minute), IsFromMe: true},
		{ProtocolMsgID: "msg-thread-1", SenderID: "user-alice", SenderName: "Alice", Body: "I'm doing great, thanks!", Timestamp: time.Now().Add(-8 * time.Minute), ThreadID: &msg2ID},
		{ProtocolMsgID: "msg-thread-2", SenderID: "me", SenderName: "You", Body: "That's awesome!", Timestamp: time.Now().Add(-7 * time.Minute), IsFromMe: true, ThreadID: &msg2ID},
	}

	// Group Conversation with threads
	convGroupID := "group-work-chat"
	m.conversations[convGroupID] = models.Conversation{ProtocolConvID: convGroupID, IsGroup: true, GroupName: "Work Chat"}
	grpMsg1ID := "grp-msg-1"
	m.messages[convGroupID] = []models.Message{
		{ProtocolMsgID: grpMsg1ID, SenderID: "user-bob", SenderName: "Bob", Body: "Does anyone have the report?", Timestamp: time.Now().Add(-30 * time.Minute)},
		{ProtocolMsgID: "grp-msg-2", SenderID: "user-charlie", SenderName: "Charlie", Body: "I think Alice has it.", Timestamp: time.Now().Add(-29 * time.Minute)},
		{ProtocolMsgID: "grp-msg-3", SenderID: "me", SenderName: "You", Body: "I'll ask her.", Timestamp: time.Now().Add(-28 * time.Minute), IsFromMe: true},
		{ProtocolMsgID: "grp-thread-1", SenderID: "user-alice", SenderName: "Alice", Body: "Yes, I have it. Let me send it.", Timestamp: time.Now().Add(-27 * time.Minute), ThreadID: &grpMsg1ID},
		{ProtocolMsgID: "grp-thread-2", SenderID: "user-bob", SenderName: "Bob", Body: "Thanks Alice!", Timestamp: time.Now().Add(-26 * time.Minute), ThreadID: &grpMsg1ID},
	}

	// Conversation with Bob (with threads)
	convBobID := "user-bob"
	m.conversations[convBobID] = models.Conversation{ProtocolConvID: convBobID, IsGroup: false}
	bobMsg1ID := "bob-msg-1"
	m.messages[convBobID] = []models.Message{
		{ProtocolMsgID: bobMsg1ID, SenderID: "user-bob", SenderName: "Bob", Body: "Hey, can we discuss the project?", Timestamp: time.Now().Add(-15 * time.Minute)},
		{ProtocolMsgID: "bob-msg-2", SenderID: "me", SenderName: "You", Body: "Sure, what do you want to discuss?", Timestamp: time.Now().Add(-14 * time.Minute), IsFromMe: true},
		{ProtocolMsgID: "bob-msg-3", SenderID: "me", SenderName: "You", Body: "hello?", Timestamp: time.Now().Add(-13 * time.Minute), IsFromMe: true},
		{ProtocolMsgID: "bob-thread-1", SenderID: "user-bob", SenderName: "Bob", Body: "I have some questions about the architecture.", Timestamp: time.Now().Add(-13 * time.Minute), ThreadID: &bobMsg1ID},
		{ProtocolMsgID: "bob-thread-2", SenderID: "me", SenderName: "You", Body: "Let's schedule a meeting.", Timestamp: time.Now().Add(-12 * time.Minute), IsFromMe: true, ThreadID: &bobMsg1ID},
	}
}

// AddReaction adds a reaction (emoji) to a message.
func (m *MockProvider) AddReaction(conversationID string, messageID string, emoji string) error {
	fmt.Printf("MockProvider: Adding reaction %s to message %s in conv %s\n", emoji, messageID, conversationID)

	// Find the message and add reaction
	for convID, messages := range m.messages {
		if convID != conversationID {
			continue
		}
		for i := range messages {
			if messages[i].ProtocolMsgID == messageID {
				reaction := models.Reaction{
					UserID:    "me",
					Emoji:     emoji,
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}
				m.reactions[messageID] = append(m.reactions[messageID], reaction)

				// Emit reaction event
				m.eventChan <- core.ReactionEvent{
					ConversationID: conversationID,
					MessageID:      messageID,
					UserID:         "me",
					Emoji:          emoji,
					Added:          true,
					Timestamp:      time.Now().Unix(),
				}
				return nil
			}
		}
	}

	return fmt.Errorf("message not found: %s", messageID)
}

// RemoveReaction removes a reaction (emoji) from a message.
func (m *MockProvider) RemoveReaction(conversationID string, messageID string, emoji string) error {
	fmt.Printf("MockProvider: Removing reaction %s from message %s in conv %s\n", emoji, messageID, conversationID)

	// Remove reaction from storage
	reactions, ok := m.reactions[messageID]
	if !ok {
		return fmt.Errorf("no reactions found for message: %s", messageID)
	}

	for i, reaction := range reactions {
		if reaction.Emoji == emoji && reaction.UserID == "me" {
			// Remove reaction
			m.reactions[messageID] = append(reactions[:i], reactions[i+1:]...)

			// Emit reaction event
			m.eventChan <- core.ReactionEvent{
				ConversationID: conversationID,
				MessageID:      messageID,
				UserID:         "me",
				Emoji:          emoji,
				Added:          false,
				Timestamp:      time.Now().Unix(),
			}
			return nil
		}
	}

	return fmt.Errorf("reaction not found: %s", emoji)
}

// SendTypingIndicator sends a typing indicator to a conversation.
func (m *MockProvider) SendTypingIndicator(conversationID string, isTyping bool) error {
	fmt.Printf("MockProvider: Sending typing indicator (isTyping: %v) to conv %s\n", isTyping, conversationID)

	// Emit typing event
	m.eventChan <- core.TypingEvent{
		ConversationID: conversationID,
		UserID:         "me",
		IsTyping:       isTyping,
	}

	return nil
}

// --- Group Management ---

// CreateGroup creates a new group conversation.
func (m *MockProvider) CreateGroup(groupName string, participantIDs []string) (*models.Conversation, error) {
	fmt.Printf("MockProvider: Creating group '%s' with %d participants\n", groupName, len(participantIDs))

	groupID := fmt.Sprintf("group-%s-%d", strings.ToLower(strings.ReplaceAll(groupName, " ", "-")), secureRandInt(10000))
	conv := models.Conversation{
		ProtocolConvID: groupID,
		IsGroup:        true,
		GroupName:      groupName,
		IsPinned:       false,
		IsMuted:        false,
	}

	m.conversations[groupID] = conv
	m.messages[groupID] = []models.Message{}

	// Emit group change event
	m.eventChan <- core.GroupChangeEvent{
		ConversationID: groupID,
		ChangeType:     core.GroupChangeCreated,
		GroupName:      groupName,
		Timestamp:      time.Now().Unix(),
	}

	return &conv, nil
}

// UpdateGroupName updates the name of a group.
func (m *MockProvider) UpdateGroupName(conversationID string, newName string) error {
	fmt.Printf("MockProvider: Updating group name to '%s' for conv %s\n", newName, conversationID)

	conv, ok := m.conversations[conversationID]
	if !ok {
		return fmt.Errorf("conversation not found: %s", conversationID)
	}
	if !conv.IsGroup {
		return fmt.Errorf("conversation is not a group: %s", conversationID)
	}

	conv.GroupName = newName
	m.conversations[conversationID] = conv

	// Emit group change event
	m.eventChan <- core.GroupChangeEvent{
		ConversationID: conversationID,
		ChangeType:     core.GroupChangeUpdated,
		GroupName:      newName,
		Timestamp:      time.Now().Unix(),
	}

	return nil
}

// AddGroupParticipants adds participants to a group.
func (m *MockProvider) AddGroupParticipants(conversationID string, participantIDs []string) error {
	fmt.Printf("MockProvider: Adding %d participants to group %s\n", len(participantIDs), conversationID)

	conv, ok := m.conversations[conversationID]
	if !ok {
		return fmt.Errorf("conversation not found: %s", conversationID)
	}
	if !conv.IsGroup {
		return fmt.Errorf("conversation is not a group: %s", conversationID)
	}

	// Emit group change events for each participant
	for _, participantID := range participantIDs {
		m.eventChan <- core.GroupChangeEvent{
			ConversationID: conversationID,
			ChangeType:     core.GroupChangeParticipantAdded,
			ParticipantID:  participantID,
			Timestamp:      time.Now().Unix(),
		}
	}

	return nil
}

// RemoveGroupParticipants removes participants from a group.
func (m *MockProvider) RemoveGroupParticipants(conversationID string, participantIDs []string) error {
	fmt.Printf("MockProvider: Removing %d participants from group %s\n", len(participantIDs), conversationID)

	conv, ok := m.conversations[conversationID]
	if !ok {
		return fmt.Errorf("conversation not found: %s", conversationID)
	}
	if !conv.IsGroup {
		return fmt.Errorf("conversation is not a group: %s", conversationID)
	}

	// Emit group change events for each participant
	for _, participantID := range participantIDs {
		m.eventChan <- core.GroupChangeEvent{
			ConversationID: conversationID,
			ChangeType:     core.GroupChangeParticipantRemoved,
			ParticipantID:  participantID,
			Timestamp:      time.Now().Unix(),
		}
	}

	return nil
}

// LeaveGroup leaves a group conversation.
func (m *MockProvider) LeaveGroup(conversationID string) error {
	fmt.Printf("MockProvider: Leaving group %s\n", conversationID)

	conv, ok := m.conversations[conversationID]
	if !ok {
		return fmt.Errorf("conversation not found: %s", conversationID)
	}
	if !conv.IsGroup {
		return fmt.Errorf("conversation is not a group: %s", conversationID)
	}

	// Emit group change event
	m.eventChan <- core.GroupChangeEvent{
		ConversationID: conversationID,
		ChangeType:     core.GroupChangeParticipantLeft,
		ParticipantID:  "me",
		Timestamp:      time.Now().Unix(),
	}

	return nil
}

// PromoteGroupAdmins promotes participants to admin in a group.
func (m *MockProvider) PromoteGroupAdmins(conversationID string, participantIDs []string) error {
	fmt.Printf("MockProvider: Promoting %d participants to admin in group %s\n", len(participantIDs), conversationID)

	conv, ok := m.conversations[conversationID]
	if !ok {
		return fmt.Errorf("conversation not found: %s", conversationID)
	}
	if !conv.IsGroup {
		return fmt.Errorf("conversation is not a group: %s", conversationID)
	}

	// Emit group change events for each participant
	for _, participantID := range participantIDs {
		m.eventChan <- core.GroupChangeEvent{
			ConversationID: conversationID,
			ChangeType:     core.GroupChangeParticipantPromoted,
			ParticipantID:  participantID,
			Timestamp:      time.Now().Unix(),
		}
	}

	return nil
}

// DemoteGroupAdmins demotes admins to regular participants in a group.
func (m *MockProvider) DemoteGroupAdmins(conversationID string, participantIDs []string) error {
	fmt.Printf("MockProvider: Demoting %d admins in group %s\n", len(participantIDs), conversationID)

	conv, ok := m.conversations[conversationID]
	if !ok {
		return fmt.Errorf("conversation not found: %s", conversationID)
	}
	if !conv.IsGroup {
		return fmt.Errorf("conversation is not a group: %s", conversationID)
	}

	// Emit group change events for each participant
	for _, participantID := range participantIDs {
		m.eventChan <- core.GroupChangeEvent{
			ConversationID: conversationID,
			ChangeType:     core.GroupChangeParticipantDemoted,
			ParticipantID:  participantID,
			Timestamp:      time.Now().Unix(),
		}
	}

	return nil
}

// GetGroupParticipants returns the list of participants in a group.
func (m *MockProvider) GetGroupParticipants(conversationID string) ([]models.GroupParticipant, error) {
	fmt.Printf("MockProvider: Getting participants for group %s\n", conversationID)

	conv, ok := m.conversations[conversationID]
	if !ok {
		return nil, fmt.Errorf("conversation not found: %s", conversationID)
	}
	if !conv.IsGroup {
		return nil, fmt.Errorf("conversation is not a group: %s", conversationID)
	}

	// Return mock participants
	participants := []models.GroupParticipant{}
	for i, contact := range m.contacts {
		if i < 3 { // Limit to first 3 contacts
			participants = append(participants, models.GroupParticipant{
				UserID:   contact.UserID,
				IsAdmin:  i == 0, // First one is admin
				JoinedAt: time.Now().Add(-time.Duration(i) * time.Hour),
			})
		}
	}

	return participants, nil
}

// --- Invite Links ---

// CreateGroupInviteLink creates an invite link for a group.
func (m *MockProvider) CreateGroupInviteLink(conversationID string) (string, error) {
	fmt.Printf("MockProvider: Creating invite link for group %s\n", conversationID)

	conv, ok := m.conversations[conversationID]
	if !ok {
		return "", fmt.Errorf("conversation not found: %s", conversationID)
	}
	if !conv.IsGroup {
		return "", fmt.Errorf("conversation is not a group: %s", conversationID)
	}

	inviteLink := fmt.Sprintf("https://mock.invite/%s/%d", conversationID, secureRandInt(100000))
	return inviteLink, nil
}

// RevokeGroupInviteLink revokes the current invite link for a group.
func (m *MockProvider) RevokeGroupInviteLink(conversationID string) error {
	fmt.Printf("MockProvider: Revoking invite link for group %s\n", conversationID)

	conv, ok := m.conversations[conversationID]
	if !ok {
		return fmt.Errorf("conversation not found: %s", conversationID)
	}
	if !conv.IsGroup {
		return fmt.Errorf("conversation is not a group: %s", conversationID)
	}

	return nil
}

// JoinGroupByInviteLink joins a group using an invite link.
func (m *MockProvider) JoinGroupByInviteLink(inviteLink string) (*models.Conversation, error) {
	fmt.Printf("MockProvider: Joining group via invite link: %s\n", inviteLink)

	// Extract group ID from invite link (mock implementation)
	groupID := fmt.Sprintf("group-invited-%d", secureRandInt(10000))
	conv := models.Conversation{
		ProtocolConvID: groupID,
		IsGroup:        true,
		GroupName:      "Invited Group",
		IsPinned:       false,
		IsMuted:        false,
	}

	m.conversations[groupID] = conv
	m.messages[groupID] = []models.Message{}

	// Emit group change event
	m.eventChan <- core.GroupChangeEvent{
		ConversationID: groupID,
		ChangeType:     core.GroupChangeParticipantAdded,
		ParticipantID:  "me",
		Timestamp:      time.Now().Unix(),
	}

	return &conv, nil
}

// JoinGroupByInviteMessage joins a group using an invite message.
func (m *MockProvider) JoinGroupByInviteMessage(inviteMessageID string) (*models.Conversation, error) {
	fmt.Printf("MockProvider: Joining group via invite message: %s\n", inviteMessageID)

	// Similar to JoinGroupByInviteLink
	return m.JoinGroupByInviteLink(fmt.Sprintf("invite-from-msg-%s", inviteMessageID))
}

// --- Receipts ---

// MarkMessageAsRead marks a message as read.
func (m *MockProvider) MarkMessageAsRead(conversationID string, messageID string) error {
	fmt.Printf("MockProvider: Marking message %s as read in conv %s\n", messageID, conversationID)

	// Emit receipt event
	m.eventChan <- core.ReceiptEvent{
		ConversationID: conversationID,
		MessageID:      messageID,
		ReceiptType:    core.ReceiptTypeRead,
		UserID:         "me",
		Timestamp:      time.Now().Unix(),
	}

	return nil
}

// MarkConversationAsRead marks all messages in a conversation as read.
func (m *MockProvider) MarkConversationAsRead(conversationID string) error {
	fmt.Printf("MockProvider: Marking all messages as read in conv %s\n", conversationID)

	messages, ok := m.messages[conversationID]
	if !ok {
		return fmt.Errorf("conversation not found: %s", conversationID)
	}

	// Mark all messages as read
	for _, msg := range messages {
		if !msg.IsFromMe {
			m.MarkMessageAsRead(conversationID, msg.ProtocolMsgID)
		}
	}

	return nil
}

// --- App State (Pin/Mute) ---

// PinConversation pins a conversation.
func (m *MockProvider) PinConversation(conversationID string) error {
	fmt.Printf("MockProvider: Pinning conversation %s\n", conversationID)

	conv, ok := m.conversations[conversationID]
	if !ok {
		return fmt.Errorf("conversation not found: %s", conversationID)
	}

	conv.IsPinned = true
	m.conversations[conversationID] = conv

	return nil
}

// UnpinConversation unpins a conversation.
func (m *MockProvider) UnpinConversation(conversationID string) error {
	fmt.Printf("MockProvider: Unpinning conversation %s\n", conversationID)

	conv, ok := m.conversations[conversationID]
	if !ok {
		return fmt.Errorf("conversation not found: %s", conversationID)
	}

	conv.IsPinned = false
	m.conversations[conversationID] = conv

	return nil
}

// MuteConversation mutes a conversation.
func (m *MockProvider) MuteConversation(conversationID string) error {
	fmt.Printf("MockProvider: Muting conversation %s\n", conversationID)

	conv, ok := m.conversations[conversationID]
	if !ok {
		return fmt.Errorf("conversation not found: %s", conversationID)
	}

	conv.IsMuted = true
	m.conversations[conversationID] = conv

	return nil
}

// UnmuteConversation unmutes a conversation.
func (m *MockProvider) UnmuteConversation(conversationID string) error {
	fmt.Printf("MockProvider: Unmuting conversation %s\n", conversationID)

	conv, ok := m.conversations[conversationID]
	if !ok {
		return fmt.Errorf("conversation not found: %s", conversationID)
	}

	conv.IsMuted = false
	m.conversations[conversationID] = conv

	return nil
}

// GetConversationState returns the state of a conversation (pin/mute status, etc.).
func (m *MockProvider) GetConversationState(conversationID string) (*models.Conversation, error) {
	fmt.Printf("MockProvider: Getting conversation state for %s\n", conversationID)

	conv, ok := m.conversations[conversationID]
	if !ok {
		return nil, fmt.Errorf("conversation not found: %s", conversationID)
	}

	return &conv, nil
}

// --- Retry Receipts ---

// SendRetryReceipt sends a retry receipt when message decryption fails.
func (m *MockProvider) SendRetryReceipt(conversationID string, messageID string) error {
	fmt.Printf("MockProvider: Sending retry receipt for message %s in conv %s\n", messageID, conversationID)

	// Emit retry receipt event
	m.eventChan <- core.RetryReceiptEvent{
		ConversationID: conversationID,
		MessageID:      messageID,
		UserID:         "me",
		Timestamp:      time.Now().Unix(),
	}

	return nil
}

// --- Status Messages ---

// SendStatusMessage sends a status message (broadcast to all contacts).
func (m *MockProvider) SendStatusMessage(text string, file *core.Attachment) (*models.Message, error) {
	fmt.Printf("MockProvider: Sending status message: %s\n", text)

	body := text
	if file != nil {
		body = fmt.Sprintf("%s [File: %s]", text, file.FileName)
	}

	statusMessage := models.Message{
		ProtocolMsgID:   fmt.Sprintf("status-msg-%d", secureRandInt(100000)),
		ProtocolConvID:  "status",
		SenderID:        "me",
		Body:            body,
		Timestamp:       time.Now(),
		IsFromMe:        true,
		IsStatusMessage: true,
	}

	if file != nil {
		statusMessage.Attachments = fmt.Sprintf(`[{"fileName":"%s","mimeType":"%s","fileSize":%d}]`, file.FileName, file.MimeType, file.FileSize)
	}

	// Emit status message event
	m.eventChan <- core.MessageEvent{Message: statusMessage}

	return &statusMessage, nil
}

func (m *MockProvider) simulateRealtimeEvents() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Simulate a random incoming message to the group chat
			if len(m.contacts) > 0 {
				sender := m.contacts[secureRandInt(len(m.contacts))] // Random contact
				newMessage := models.Message{
					ProtocolMsgID:  fmt.Sprintf("mock-event-%d", secureRandInt(100000)),
					ProtocolConvID: "group-work-chat",
					SenderID:       sender.UserID,
					Body:           fmt.Sprintf("Random event from %s: %s", sender.Username, loremIpsum[secureRandInt(len(loremIpsum))]),
					Timestamp:      time.Now(),
					IsFromMe:       false,
				}
				// Add to messages storage
				if _, ok := m.messages["group-work-chat"]; ok {
					m.messages["group-work-chat"] = append(m.messages["group-work-chat"], newMessage)
				}
				m.eventChan <- core.MessageEvent{Message: newMessage}
			}
		case <-m.stopChan:
			return
		}
	}
}
