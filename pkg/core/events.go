// Package core provides the core interfaces and types for chat providers.
package core

import "Loom/pkg/models"

// EventType represents the type of event that can be emitted by a provider.
type EventType string

const (
	// EventTypeMessage represents a new message event (text or file).
	EventTypeMessage EventType = "message"
	// EventTypeReaction represents a reaction added/removed event.
	EventTypeReaction EventType = "reaction"
	// EventTypeTyping represents a typing indicator event.
	EventTypeTyping EventType = "typing"
	// EventTypeContactStatus represents a contact status change event.
	EventTypeContactStatus EventType = "contact_status"
	// EventTypePresence represents a real-time presence (online/offline) event.
	EventTypePresence EventType = "presence"
	// EventTypeGroupChange represents a group change event (created, updated, participant added/removed, etc.).
	EventTypeGroupChange EventType = "group_change"
	// EventTypeReceipt represents a delivery or read receipt event.
	EventTypeReceipt EventType = "receipt"
	// EventTypeRetryReceipt represents a retry receipt event (when message decryption fails).
	EventTypeRetryReceipt EventType = "retry_receipt"
	// EventTypeSyncStatus represents a synchronization status update event.
	EventTypeSyncStatus EventType = "sync_status"
)

// ProviderEvent is the base interface for all provider events.
type ProviderEvent interface {
	Type() EventType
}

// MessageEvent represents a new message event (text or file).
type MessageEvent struct {
	Message models.Message
}

// Type returns the event type for MessageEvent.
func (e MessageEvent) Type() EventType {
	return EventTypeMessage
}

// ReactionEvent represents a reaction to a message.
type ReactionEvent struct {
	ConversationID string // Protocol conversation ID
	MessageID      string // Protocol message ID
	UserID         string // User who reacted
	Emoji          string // Emoji reaction (e.g., "👍", "❤️")
	Added          bool   // true if reaction added, false if removed
	Timestamp      int64  // Unix timestamp
}

// Type returns the event type for ReactionEvent.
func (e ReactionEvent) Type() EventType {
	return EventTypeReaction
}

// TypingEvent represents a typing indicator event.
type TypingEvent struct {
	ConversationID string // Protocol conversation ID
	UserID         string // User who is typing
	UserName       string // Display name of the user who is typing
	IsTyping       bool   // true if typing, false if stopped
}

// Type returns the event type for TypingEvent.
func (e TypingEvent) Type() EventType {
	return EventTypeTyping
}

// ContactStatusEvent represents a change in contact status (online/offline, last seen, etc.).
type ContactStatusEvent struct {
	UserID      string // Protocol user ID
	Status      string // "online", "offline", "away", "busy", etc.
	LastSeen    *int64 // Unix timestamp of last seen (nil if not available)
	StatusEmoji string // Emoji associated with the status (e.g., ":calendar:", "📅")
	StatusText  string // Status text (e.g., "en réunion", "in a meeting")
}

// Type returns the event type for ContactStatusEvent.
func (e ContactStatusEvent) Type() EventType {
	return EventTypeContactStatus
}

// PresenceEvent represents a real-time presence update (online/offline).
type PresenceEvent struct {
	UserID   string // Protocol user ID
	IsOnline bool   // true if online, false if offline
	LastSeen int64  // Unix timestamp of last seen (0 if not available or user is online)
}

// Type returns the event type for PresenceEvent.
func (e PresenceEvent) Type() EventType {
	return EventTypePresence
}

// GroupChangeType represents the type of group change.
type GroupChangeType string

const (
	// GroupChangeCreated indicates a group was created.
	GroupChangeCreated GroupChangeType = "created"
	// GroupChangeUpdated indicates a group was updated (name, description, etc.).
	GroupChangeUpdated GroupChangeType = "updated"
	// GroupChangeParticipantAdded indicates a participant was added to the group.
	GroupChangeParticipantAdded GroupChangeType = "participant_added"
	// GroupChangeParticipantRemoved indicates a participant was removed from the group.
	GroupChangeParticipantRemoved GroupChangeType = "participant_removed"
	// GroupChangeParticipantLeft indicates a participant left the group.
	GroupChangeParticipantLeft GroupChangeType = "participant_left"
	// GroupChangeParticipantPromoted indicates a participant was promoted to admin.
	GroupChangeParticipantPromoted GroupChangeType = "participant_promoted"
	// GroupChangeParticipantDemoted indicates a participant was demoted from admin.
	GroupChangeParticipantDemoted GroupChangeType = "participant_demoted"
)

// GroupChangeEvent represents a change in a group (created, updated, participants, etc.).
type GroupChangeEvent struct {
	ConversationID string          // Protocol conversation ID
	ChangeType     GroupChangeType // Type of change
	GroupName      string          // Updated group name (if applicable)
	ParticipantID  string          // User ID of the participant (if applicable)
	Timestamp      int64           // Unix timestamp
}

// Type returns the event type for GroupChangeEvent.
func (e GroupChangeEvent) Type() EventType {
	return EventTypeGroupChange
}

// ReceiptType represents the type of receipt.
type ReceiptType string

const (
	// ReceiptTypeDelivery indicates a message was delivered.
	ReceiptTypeDelivery ReceiptType = "delivery"
	// ReceiptTypeRead indicates a message was read.
	ReceiptTypeRead ReceiptType = "read"
	// ReceiptTypePlayed indicates a voice message was played.
	ReceiptTypePlayed ReceiptType = "played"
)

// ReceiptEvent represents a delivery or read receipt for a message.
type ReceiptEvent struct {
	ConversationID string      // Protocol conversation ID
	MessageID      string      // Protocol message ID
	ReceiptType    ReceiptType // Type of receipt (delivery or read)
	UserID         string      // User ID who sent the receipt
	Timestamp      int64       // Unix timestamp
}

// Type returns the event type for ReceiptEvent.
func (e ReceiptEvent) Type() EventType {
	return EventTypeReceipt
}

// RetryReceiptEvent represents a retry receipt when message decryption fails.
type RetryReceiptEvent struct {
	ConversationID string // Protocol conversation ID
	MessageID      string // Protocol message ID that failed to decrypt
	UserID         string // User ID who sent the retry receipt
	Timestamp      int64  // Unix timestamp
}

// Type returns the event type for RetryReceiptEvent.
func (e RetryReceiptEvent) Type() EventType {
	return EventTypeRetryReceipt
}

// SyncStatusType represents the type of synchronization status.
type SyncStatusType string

const (
	// SyncStatusFetchingContacts indicates contacts are being fetched.
	SyncStatusFetchingContacts SyncStatusType = "fetching_contacts"
	// SyncStatusFetchingHistory indicates message history is being fetched for a conversation.
	SyncStatusFetchingHistory SyncStatusType = "fetching_history"
	// SyncStatusFetchingAvatars indicates profile pictures are being loaded.
	SyncStatusFetchingAvatars SyncStatusType = "fetching_avatars"
	// SyncStatusCompleted indicates synchronization is completed.
	SyncStatusCompleted SyncStatusType = "completed"
	// SyncStatusError indicates an error occurred during synchronization.
	SyncStatusError SyncStatusType = "error"
)

// SyncStatusEvent represents a synchronization status update.
type SyncStatusEvent struct {
	Status         SyncStatusType // Type of sync status
	Message        string         // Human-readable message describing the current step
	ConversationID string         // Conversation ID (if fetching history for a specific conversation)
	Progress       int            // Progress percentage (0-100, -1 if unknown)
}

// Type returns the event type for SyncStatusEvent.
func (e SyncStatusEvent) Type() EventType {
	return EventTypeSyncStatus
}
