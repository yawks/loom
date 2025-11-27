// Package core provides the core interfaces and types for chat providers.
package core

import (
	"Loom/pkg/models"
	"time"
)

// Attachment represents a file attached to a message.
type Attachment struct {
	FileName string
	FileSize int
	MimeType string
	Data     []byte
}

// Provider defines the interface that each protocol adapter must implement.
// All providers (WhatsApp, Slack, Google Messages, etc.) must implement this interface.
type Provider interface {
	// Init initializes the provider with its configuration.
	// The config parameter contains the provider-specific configuration.
	Init(config ProviderConfig) error

	// GetConfig returns the current configuration of the provider.
	GetConfig() ProviderConfig

	// SetConfig updates the configuration of the provider.
	// Returns an error if the configuration is invalid.
	SetConfig(config ProviderConfig) error

	// GetQRCode returns the latest QR code string for authentication (if applicable).
	// Returns an empty string and nil error if QR code is not needed, not available, or already authenticated.
	GetQRCode() (string, error)

	// IsAuthenticated returns true if the provider is already authenticated/logged in.
	// For providers that don't require authentication, this should always return true.
	// This is used to determine if the provider should be automatically connected on startup.
	IsAuthenticated() bool

	// Connect establishes the connection with the remote service.
	Connect() error

	// Disconnect closes the connection and stops all background operations.
	Disconnect() error

	// SyncHistory retrieves message history since a certain date.
	// Useful for catching up on missed messages when the app was offline.
	SyncHistory(since time.Time) error

	// StreamEvents returns a channel for receiving real-time events.
	// Events can be new messages, reactions, typing indicators, contact status changes, etc.
	// The channel emits ProviderEvent types (MessageEvent, ReactionEvent, TypingEvent, ContactStatusEvent).
	StreamEvents() (<-chan ProviderEvent, error)

	// GetContacts returns the list of contacts for this protocol with their current status.
	// The LinkedAccount.Status field should be populated with the current status
	// (e.g., "online", "offline", "away", "busy").
	GetContacts() ([]models.LinkedAccount, error)

	// GetConversationHistory retrieves the message history for a specific conversation.
	// conversationID is the protocol-specific conversation ID.
	// limit specifies the maximum number of messages to retrieve (0 = no limit).
	// Returns messages ordered by timestamp (oldest first).
	GetConversationHistory(conversationID string, limit int) ([]models.Message, error)

	// SendMessage sends a text message to a given conversation.
	// If file is not nil, the file will be attached to the message.
	// If threadID is not nil, the message will be sent as a reply in the specified thread.
	// Returns the created message or an error.
	SendMessage(conversationID string, text string, file *Attachment, threadID *string) (*models.Message, error)

	// SendFile sends a file to a given conversation without text.
	// If threadID is not nil, the file will be sent as a reply in the specified thread.
	// Returns the created message or an error.
	SendFile(conversationID string, file *Attachment, threadID *string) (*models.Message, error)

	// EditMessage edits an existing message.
	// conversationID is the protocol-specific conversation ID.
	// messageID is the protocol-specific message ID.
	// newText is the new message text.
	// Returns the updated message or an error.
	EditMessage(conversationID string, messageID string, newText string) (*models.Message, error)

	// DeleteMessage deletes a message.
	// conversationID is the protocol-specific conversation ID.
	// messageID is the protocol-specific message ID.
	// Returns an error if the message could not be deleted.
	DeleteMessage(conversationID string, messageID string) error

	// GetThreads loads all messages in a discussion thread from a parent message ID.
	// parentMessageID is the protocol-specific message ID of the parent message.
	GetThreads(parentMessageID string) ([]models.Message, error)

	// AddReaction adds a reaction (emoji) to a message.
	// conversationID is the protocol-specific conversation ID.
	// messageID is the protocol-specific message ID.
	// emoji is the emoji to add (e.g., "👍", "❤️").
	// Returns an error if the reaction could not be added.
	AddReaction(conversationID string, messageID string, emoji string) error

	// RemoveReaction removes a reaction (emoji) from a message.
	// conversationID is the protocol-specific conversation ID.
	// messageID is the protocol-specific message ID.
	// emoji is the emoji to remove.
	// Returns an error if the reaction could not be removed.
	RemoveReaction(conversationID string, messageID string, emoji string) error

	// SendTypingIndicator sends a typing indicator to a conversation.
	// conversationID is the protocol-specific conversation ID.
	// isTyping should be true when the user starts typing, false when they stop.
	// Returns an error if the typing indicator could not be sent.
	SendTypingIndicator(conversationID string, isTyping bool) error

	// --- Group Management ---

	// CreateGroup creates a new group conversation.
	// groupName is the name of the group.
	// participantIDs is a list of user IDs to add to the group.
	// Returns the created conversation or an error.
	CreateGroup(groupName string, participantIDs []string) (*models.Conversation, error)

	// UpdateGroupName updates the name of a group.
	// conversationID is the protocol-specific conversation ID.
	// newName is the new group name.
	// Returns an error if the group name could not be updated.
	UpdateGroupName(conversationID string, newName string) error

	// AddGroupParticipants adds participants to a group.
	// conversationID is the protocol-specific conversation ID.
	// participantIDs is a list of user IDs to add.
	// Returns an error if participants could not be added.
	AddGroupParticipants(conversationID string, participantIDs []string) error

	// RemoveGroupParticipants removes participants from a group.
	// conversationID is the protocol-specific conversation ID.
	// participantIDs is a list of user IDs to remove.
	// Returns an error if participants could not be removed.
	RemoveGroupParticipants(conversationID string, participantIDs []string) error

	// LeaveGroup leaves a group conversation.
	// conversationID is the protocol-specific conversation ID.
	// Returns an error if the user could not leave the group.
	LeaveGroup(conversationID string) error

	// PromoteGroupAdmins promotes participants to admin in a group.
	// conversationID is the protocol-specific conversation ID.
	// participantIDs is a list of user IDs to promote.
	// Returns an error if participants could not be promoted.
	PromoteGroupAdmins(conversationID string, participantIDs []string) error

	// DemoteGroupAdmins demotes admins to regular participants in a group.
	// conversationID is the protocol-specific conversation ID.
	// participantIDs is a list of user IDs to demote.
	// Returns an error if admins could not be demoted.
	DemoteGroupAdmins(conversationID string, participantIDs []string) error

	// GetGroupParticipants returns the list of participants in a group.
	// conversationID is the protocol-specific conversation ID.
	// Returns a list of group participants or an error.
	GetGroupParticipants(conversationID string) ([]models.GroupParticipant, error)

	// --- Invite Links ---

	// CreateGroupInviteLink creates an invite link for a group.
	// conversationID is the protocol-specific conversation ID.
	// Returns the invite link URL or an error.
	CreateGroupInviteLink(conversationID string) (string, error)

	// RevokeGroupInviteLink revokes the current invite link for a group.
	// conversationID is the protocol-specific conversation ID.
	// Returns an error if the invite link could not be revoked.
	RevokeGroupInviteLink(conversationID string) error

	// JoinGroupByInviteLink joins a group using an invite link.
	// inviteLink is the invite link URL.
	// Returns the conversation or an error.
	JoinGroupByInviteLink(inviteLink string) (*models.Conversation, error)

	// JoinGroupByInviteMessage joins a group using an invite message.
	// inviteMessageID is the protocol-specific message ID of the invite message.
	// Returns the conversation or an error.
	JoinGroupByInviteMessage(inviteMessageID string) (*models.Conversation, error)

	// --- Receipts ---

	// MarkMessageAsRead marks a message as read.
	// conversationID is the protocol-specific conversation ID.
	// messageID is the protocol-specific message ID.
	// Returns an error if the message could not be marked as read.
	MarkMessageAsRead(conversationID string, messageID string) error

	// MarkConversationAsRead marks all messages in a conversation as read.
	// conversationID is the protocol-specific conversation ID.
	// Returns an error if the conversation could not be marked as read.
	MarkConversationAsRead(conversationID string) error

	// --- App State (Pin/Mute) ---

	// PinConversation pins a conversation.
	// conversationID is the protocol-specific conversation ID.
	// Returns an error if the conversation could not be pinned.
	PinConversation(conversationID string) error

	// UnpinConversation unpins a conversation.
	// conversationID is the protocol-specific conversation ID.
	// Returns an error if the conversation could not be unpinned.
	UnpinConversation(conversationID string) error

	// MuteConversation mutes a conversation.
	// conversationID is the protocol-specific conversation ID.
	// Returns an error if the conversation could not be muted.
	MuteConversation(conversationID string) error

	// UnmuteConversation unmutes a conversation.
	// conversationID is the protocol-specific conversation ID.
	// Returns an error if the conversation could not be unmuted.
	UnmuteConversation(conversationID string) error

	// GetConversationState returns the state of a conversation (pin/mute status, etc.).
	// conversationID is the protocol-specific conversation ID.
	// Returns the conversation with its state or an error.
	GetConversationState(conversationID string) (*models.Conversation, error)

	// --- Retry Receipts ---

	// SendRetryReceipt sends a retry receipt when message decryption fails.
	// conversationID is the protocol-specific conversation ID.
	// messageID is the protocol-specific message ID that failed to decrypt.
	// Returns an error if the retry receipt could not be sent.
	SendRetryReceipt(conversationID string, messageID string) error

	// --- Status Messages ---

	// SendStatusMessage sends a status message (broadcast to all contacts).
	// text is the status message text.
	// file is an optional file attachment.
	// Returns the created message or an error.
	SendStatusMessage(text string, file *Attachment) (*models.Message, error)
}
