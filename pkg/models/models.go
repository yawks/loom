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
	AvatarURL     string         `json:"avatarUrl,omitempty"`                 // Profile picture URL from the provider
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
	ID                uint             `gorm:"primarykey" json:"id"`
	ConversationID    uint             `json:"conversationId"`
	ProtocolConvID    string           `json:"protocolConvId"`                     // Conversation ID on the platform
	ProtocolMsgID     string           `gorm:"uniqueIndex" json:"protocolMsgId"`   // Message ID on the platform
	SenderID          string           `json:"senderId"`                           // Sender's ID on the platform
	SenderName        string           `gorm:"-" json:"senderName,omitempty"`      // Human-readable sender name (not persisted yet)
	SenderAvatarURL   string           `gorm:"-" json:"senderAvatarUrl,omitempty"` // Sender's avatar URL (not persisted yet)
	Body              string           `json:"body"`
	Timestamp         time.Time        `json:"timestamp"`
	IsFromMe          bool             `json:"isFromMe"`
	ThreadID          *string          `gorm:"index" json:"threadId,omitempty"`                     // Nullable, for replies
	QuotedMessageID   *string          `gorm:"index" json:"quotedMessageId,omitempty"`              // ID of the message being replied to
	QuotedSenderID    *string          `json:"quotedSenderId,omitempty"`                            // Sender ID of the quoted message
	QuotedSenderName  string           `gorm:"-" json:"quotedSenderName,omitempty"`                 // Sender name of the quoted message (not persisted)
	QuotedBody        *string          `json:"quotedBody,omitempty"`                                // Body of the quoted message
	Attachments       string           `json:"attachments"`                                         // Could be a JSON []string of URLs/paths
	Reactions         []Reaction       `gorm:"foreignKey:MessageID" json:"reactions,omitempty"`     // Reactions to this message
	Receipts          []MessageReceipt `gorm:"foreignKey:MessageID" json:"receipts,omitempty"`      // Delivery and read receipts
	IsStatusMessage   bool             `json:"isStatusMessage"`                                     // Whether this is a status message
	IsDeleted         bool             `json:"isDeleted"`                                           // Flag when the remote client deleted the message
	DeletedBy         string           `json:"deletedBy,omitempty"`                                 // User ID who triggered the deletion
	DeletedReason     string           `json:"deletedReason,omitempty"`                             // Reason (e.g., "revoked")
	DeletedTimestamp  *time.Time       `json:"deletedTimestamp,omitempty"`                          // When the deletion happened
	IsEdited          bool             `json:"isEdited"`                                            // Flag when the message has been edited
	EditedTimestamp   *time.Time       `json:"editedTimestamp,omitempty"`                           // When the message was edited
	CallType          string           `json:"callType,omitempty"`                                  // Type of call: "missed_voice", "missed_video", "missed_group_voice", "missed_group_video", "scheduled_start", "scheduled_cancel", "linked_group_start"
	CallDurationSecs   *int32           `json:"callDurationSecs,omitempty"`                           // Duration of the call in seconds (from CallLogMessage)
	CallParticipants  string           `json:"callParticipants,omitempty"`                          // JSON array of participant JIDs (from CallLogMessage)
	CallOutcome       string           `json:"callOutcome,omitempty"`                               // Call outcome: "CONNECTED", "MISSED", "FAILED", etc. (from CallLogMessage)
	CallIsVideo       bool             `json:"callIsVideo"`                                          // Whether the call was a video call (from CallLogMessage)
	DeletedAt         gorm.DeletedAt   `gorm:"index" json:"-"`
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

// LIDMapping stores the mapping between WhatsApp Local IDs (LID) and standard JIDs
// This is crucial for resolving typing indicators and other presence events
type LIDMapping struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	LID       string    `gorm:"column:lid;uniqueIndex" json:"lid"`     // WhatsApp Local ID (e.g., "176188215558395@lid")
	JID       string    `gorm:"column:jid;index" json:"jid"`           // Standard JID (e.g., "33677815440@s.whatsapp.net")
	Protocol  string    `gorm:"column:protocol;index" json:"protocol"` // Protocol (e.g., "whatsapp")
	LastSeen  time.Time `gorm:"column:last_seen" json:"lastSeen"`      // Last time this mapping was seen/confirmed
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TableName overrides the table name used by LIDMapping to 'lid_mappings'
func (LIDMapping) TableName() string {
	return "lid_mappings"
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

// Attachment represents a file attached to a message.
type Attachment struct {
	Type      string `json:"type"`                // "image", "video", "audio", "document", "sticker"
	URL       string `json:"url"`                 // Local file path or remote URL
	FileName  string `json:"fileName"`            // Original filename
	FileSize  int64  `json:"fileSize"`            // File size in bytes
	MimeType  string `json:"mimeType"`            // MIME type (e.g., "image/jpeg", "application/pdf")
	Thumbnail string `json:"thumbnail,omitempty"` // Thumbnail URL for images/videos (optional)
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

// ContactAlias stores user-defined custom names for contacts.
type ContactAlias struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    string    `gorm:"uniqueIndex;not null" json:"userId"` // User ID on the platform (e.g., WhatsApp JID)
	Alias     string    `gorm:"not null" json:"alias"`              // Custom name set by the user
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
