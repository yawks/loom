// Package core provides the core interfaces and types for chat providers.
package core

import (
	"Loom/pkg/models"
	"strings"
	"time"
)

// BuildConvID returns a namespaced conversation ID: "instanceID::rawConvID".
// If rawConvID is already namespaced it is returned unchanged.
// This ensures that the same contact JID from two different provider instances
// produces distinct ProtocolConvID values in the database.
func BuildConvID(instanceID, rawConvID string) string {
	if rawConvID == "" || instanceID == "" {
		return rawConvID
	}
	if strings.Contains(rawConvID, "::") {
		return rawConvID // already namespaced
	}
	return instanceID + "::" + rawConvID
}

// StripConvID extracts the raw provider conversation ID from a namespaced ID.
// Returns the input unchanged when it contains no "::" separator.
func StripConvID(namespacedConvID string) string {
	if idx := strings.Index(namespacedConvID, "::"); idx >= 0 {
		return namespacedConvID[idx+2:]
	}
	return namespacedConvID
}

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
	// beforeTimestamp, if not nil, limits results to messages before this timestamp (for pagination).
	// Returns messages ordered by timestamp (oldest first).
	GetConversationHistory(conversationID string, limit int, beforeTimestamp *time.Time, sinceTimestamp *time.Time) ([]models.Message, error)

	// SendMessage sends a text message to a given conversation.
	// If file is not nil, the file will be attached to the message.
	// If threadID is not nil, the message will be sent as a reply in the specified thread.
	// Returns the created message or an error.
	SendMessage(conversationID string, text string, file *Attachment, threadID *string) (*models.Message, error)

	// SendReply sends a text message as a reply to another message.
	// conversationID is the protocol-specific conversation ID.
	// text is the message text to send.
	// quotedMessageID is the protocol-specific message ID being replied to.
	// Returns the created message or an error.
	SendReply(conversationID string, text string, quotedMessageID string) (*models.Message, error)

	// SendThreadReply sends a text message as a quoted reply to another message inside a thread.
	SendThreadReply(conversationID string, text string, threadID string, quotedMessageID string) (*models.Message, error)

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

	// MarkMessageAsPlayed marks a voice message as played (listened to).
	// conversationID is the protocol-specific conversation ID.
	// messageID is the protocol-specific message ID.
	// Returns an error if the message could not be marked as played.
	MarkMessageAsPlayed(conversationID string, messageID string) error

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

	// GetContactName retrieves the display name for a contact ID.
	// This is used to resolve contact names from LIDs, phone numbers, or other identifiers.
	// Returns the display name or an error if the contact cannot be found.
	GetContactName(contactID string) (string, error)

	// Cleanup cleans up the provider's data when it is removed.
	// This should remove any local files, databases, or session data associated with the provider instance.
	Cleanup() error

	// GetCapabilities returns the features supported by this provider.
	GetCapabilities() Capabilities

	// GetCustomEmojis returns the list of custom emojis for this provider.
	GetCustomEmojis() (map[string]string, error)

	// GetAuthQRCode returns the QR code for authentication.
	GetAuthQRCode() (string, error)

	// RefreshContact refreshes the metadata for a specific contact.
	// This is used to update information like avatars when a conversation is opened.
	RefreshContact(contactID string) error
}

// Capabilities defines the features supported by a provider.
type Capabilities struct {
	SupportsThreads               bool   `json:"supportsThreads"`
	SupportsReactions             bool   `json:"supportsReactions"`
	SupportsCustomEmojis          bool   `json:"supportsCustomEmojis"`
	SupportsTypingIndicator       bool   `json:"supportsTypingIndicator"`
	SupportsGroupManagement       bool   `json:"supportsGroupManagement"`
	SupportsAddGroupMembers       bool   `json:"supportsAddGroupMembers"`
	SupportsRemoveGroupMembers    bool   `json:"supportsRemoveGroupMembers"`
	SupportsRenameGroup           bool   `json:"supportsRenameGroup"`
	SupportsGroupDescription      bool   `json:"supportsGroupDescription"`
	SupportsGroupPhoto            bool   `json:"supportsGroupPhoto"`
	SupportsGroupAdminRoles       bool   `json:"supportsGroupAdminRoles"`
	SupportsLeaveGroup            bool   `json:"supportsLeaveGroup"`
	SupportsDeleteMessage         bool   `json:"supportsDeleteMessage"`
	SupportsEditMessage           bool   `json:"supportsEditMessage"`
	SupportsReadReceipts          bool   `json:"supportsReadReceipts"`
	SupportsPinConversation       bool   `json:"supportsPinConversation"`
	SupportsPinMessage            bool   `json:"supportsPinMessage"`
	SupportsListMessagePins       bool   `json:"supportsListMessagePins"`
	SupportsScheduledMessages     bool   `json:"supportsScheduledMessages"`
	SupportsListScheduledMessages bool   `json:"supportsListScheduledMessages"`
	MessagePinScope               string `json:"messagePinScope"`
	SupportsMuteConversation      bool   `json:"supportsMuteConversation"`
	SupportsQRCodeAuth            bool   `json:"supportsQRCodeAuth"`
	// NativeEmojiReactions indicates the provider's reaction API expects raw Unicode
	// emoji characters (e.g. "👍") rather than Slack-style names (e.g. "+1").
	NativeEmojiReactions bool `json:"nativeEmojiReactions"`
	// Conversation creation capabilities are intentionally more precise than
	// SupportsGroupManagement, which also covers editing existing groups.
	SupportsContactDirectory     bool `json:"supportsContactDirectory"`
	SupportsDirectConversation   bool `json:"supportsDirectConversation"`
	SupportsPhoneNumberRecipient bool `json:"supportsPhoneNumberRecipient"`
	SupportsGroupConversation    bool `json:"supportsGroupConversation"`
	SupportsGroupTitle           bool `json:"supportsGroupTitle"`
	RequiresGroupTitle           bool `json:"requiresGroupTitle"`
	// GroupConversationTypes is a comma-separated list so Capabilities remains
	// comparable (several provider contract tests compare it as a value).
	GroupConversationTypes string `json:"groupConversationTypes"`
}

// ScheduledMessageProvider is implemented by providers whose remote API can
// queue, list and cancel messages for future delivery.
type ScheduledMessageProvider interface {
	ScheduleMessage(conversationID, text string, scheduledAt time.Time, parentMsgID string) (*models.ScheduledMessage, error)
	ListScheduledMessages(conversationID string) ([]models.ScheduledMessage, error)
	CancelScheduledMessage(conversationID, scheduledMessageID string) error
}

// MessagePinProvider is implemented only by providers that can mutate remote
// message pins. Keeping it optional avoids fake unsupported implementations.
type MessagePinProvider interface {
	PinMessage(conversationID, messageID string) (*models.MessagePin, error)
	UnpinMessage(conversationID, messageID string) error
	ListMessagePins(conversationID string) ([]models.MessagePin, error)
	ResolvePinnedMessage(conversationID, messageID string) (*models.Message, error)
}

// GroupDetailsProvider is implemented by providers that expose rich group
// metadata beyond the participant list.
type GroupDetailsProvider interface {
	GetGroupDetails(conversationID string) (*models.GroupDetails, error)
	UpdateGroupDescription(conversationID, description string) error
	UpdateGroupPhoto(conversationID string, photo []byte) error
}

// ConversationCreator is optional. Providers implement it when creating a
// conversation requires more information than the legacy CreateGroup method.
type ConversationCreator interface {
	CreateConversation(conversationType, title string, participantIDs []string) (*models.Conversation, error)
}

// ContactSearcher is implemented by providers whose directory is a remote
// people picker rather than a list that can be fully synchronized up front.
type ContactSearcher interface {
	SearchContacts(query string) ([]models.LinkedAccount, error)
}

// DirectConversationCreator resolves providers (such as Teams) whose DM
// conversation ID cannot be derived from the target user ID alone.
type DirectConversationCreator interface {
	CreateDirectConversation(participantID string) (*models.Conversation, error)
}

// PhoneConversationCreator is implemented by providers that can start a direct
// conversation from a phone number that is not present in their directory.
type PhoneConversationCreator interface {
	CreatePhoneConversation(phoneNumber string) (*models.Conversation, error)
}

type CurrentUserProvider interface {
	CurrentUserID() string
}

// ContactStatusRefresher provides accurate on-demand presence for providers
// whose directory endpoint does not carry reliable presence information.
type ContactStatusRefresher interface {
	RefreshContactStatuses(userIDs []string) map[string]string
}

// GlobalHistorySyncer is implemented by providers that can explicitly ask the
// primary device to re-send history for every locally known conversation.
// It is reserved for user-triggered full resyncs because it can be expensive.
type GlobalHistorySyncer interface {
	SyncAllHistory(since time.Time) error
}
