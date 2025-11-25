// Package models defines the data models for the chat application.
package models

import (
	"time"

	"gorm.io/gorm"
)

// MetaContact is the unified contact displayed to the user.
type MetaContact struct {
	ID             uint            `gorm:"primarykey" json:"id"`
	DisplayName    string          `json:"displayName"`
	AvatarURL      string          `json:"avatarUrl"`
	LinkedAccounts []LinkedAccount `gorm:"foreignKey:MetaContactID" json:"linkedAccounts"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

// LinkedAccount represents a protocol-specific account (WhatsApp, Slack, etc.).
type LinkedAccount struct {
	ID            uint           `gorm:"primarykey" json:"id"`
	MetaContactID uint           `json:"metaContactId"`
	Protocol      string         `gorm:"index" json:"protocol"` // "slack", "whatsapp", "google_messages"
	UserID        string         `json:"userId"`                // User's ID on the remote platform
	Username      string         `json:"username"`
	Status        string         `json:"status"`                              // "online", "offline", "away", "busy", etc.
	LastSeen      *time.Time     `json:"lastSeen,omitempty"`                  // Last seen timestamp (nil if not available)
	Conversations []Conversation `gorm:"foreignKey:LinkedAccountID" json:"-"` // Avoid JSON cycles
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
}

// Conversation represents a chat (Direct, Group).
type Conversation struct {
	ID                uint               `gorm:"primarykey" json:"id"`
	LinkedAccountID   uint               `json:"linkedAccountId"`
	ProtocolConvID    string             `gorm:"uniqueIndex" json:"protocolConvId"` // Conversation ID on the platform
	IsGroup           bool               `json:"isGroup"`
	GroupName         string             `json:"groupName,omitempty"`
	IsPinned          bool               `json:"isPinned"`                                                     // Whether the conversation is pinned
	IsMuted           bool               `json:"isMuted"`                                                      // Whether the conversation is muted
	GroupParticipants []GroupParticipant `gorm:"foreignKey:ConversationID" json:"groupParticipants,omitempty"` // Group participants (only for groups)
	Messages          []Message          `gorm:"foreignKey:ConversationID" json:"messages"`
	CreatedAt         time.Time          `json:"createdAt"`
	UpdatedAt         time.Time          `json:"updatedAt"`
}

// GroupParticipant represents a participant in a group conversation.
type GroupParticipant struct {
	ID             uint      `gorm:"primarykey" json:"id"`
	ConversationID uint      `gorm:"index" json:"conversationId"`
	UserID         string    `json:"userId"`   // User ID on the platform
	IsAdmin        bool      `json:"isAdmin"`  // Whether the participant is an admin
	JoinedAt       time.Time `json:"joinedAt"` // When the participant joined
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// Message contains the content of a message.
type Message struct {
	ID              uint             `gorm:"primarykey" json:"id"`
	ConversationID  uint             `json:"conversationId"`
	ProtocolConvID  string           `json:"protocolConvId"`                   // Conversation ID on the platform
	ProtocolMsgID   string           `gorm:"uniqueIndex" json:"protocolMsgId"` // Message ID on the platform
	SenderID        string           `json:"senderId"`                         // Sender's ID on the platform
	SenderName      string           `gorm:"-" json:"senderName,omitempty"`    // Human-readable sender name (not persisted yet)
	Body            string           `json:"body"`
	Timestamp       time.Time        `json:"timestamp"`
	IsFromMe        bool             `json:"isFromMe"`
	ThreadID        *string          `gorm:"index" json:"threadId,omitempty"`                 // Nullable, for replies
	Attachments     string           `json:"attachments"`                                     // Could be a JSON []string of URLs/paths
	Reactions       []Reaction       `gorm:"foreignKey:MessageID" json:"reactions,omitempty"` // Reactions to this message
	Receipts        []MessageReceipt `gorm:"foreignKey:MessageID" json:"receipts,omitempty"`  // Delivery and read receipts
	IsStatusMessage bool             `json:"isStatusMessage"`                                 // Whether this is a status message
	DeletedAt       gorm.DeletedAt   `gorm:"index" json:"-"`
}

// MessageReceipt represents a delivery or read receipt for a message.
type MessageReceipt struct {
	ID          uint      `gorm:"primarykey" json:"id"`
	MessageID   uint      `gorm:"index" json:"messageId"` // Foreign key to Message
	UserID      string    `json:"userId"`                 // User ID who sent the receipt
	ReceiptType string    `json:"receiptType"`            // "delivery" or "read"
	Timestamp   time.Time `json:"timestamp"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Reaction represents a reaction to a message.
type Reaction struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	MessageID uint      `gorm:"index" json:"messageId"` // Foreign key to Message
	UserID    string    `json:"userId"`                 // User who reacted
	Emoji     string    `json:"emoji"`                  // Emoji reaction (e.g., "👍", "❤️")
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ProviderConfiguration stores the configuration of a provider instance.
type ProviderConfiguration struct {
	ID         uint       `gorm:"primarykey" json:"id"`
	ProviderID string     `gorm:"uniqueIndex;not null" json:"providerId"` // e.g., "whatsapp", "mock"
	ConfigJSON string     `gorm:"type:text" json:"configJson"`            // JSON-encoded configuration
	IsActive   bool       `json:"isActive"`                               // Whether this provider is currently active
	LastSyncAt *time.Time `json:"lastSyncAt,omitempty"`                   // Last time messages were synced
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}
