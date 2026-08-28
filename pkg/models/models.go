// Package models defines the data models for the chat application.
package models

import (
	"time"

	"gorm.io/gorm"
)

// MetaContact is the unified contact displayed to the user.
type MetaContact struct {
	ID             uint            `gorm:"primarykey;index" json:"id"`
	DisplayName    string          `json:"displayName"`
	AvatarURL      string          `json:"avatarUrl"`
	LinkedAccounts []LinkedAccount `gorm:"foreignKey:MetaContactID" json:"linkedAccounts"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

// ContactProfile is the provider-neutral contact card exposed to the frontend.
// ProviderFields contains optional metadata which does not warrant a common column.
type ContactProfile struct {
	UserID             string            `json:"userId"`
	DisplayName        string            `json:"displayName"`
	AvatarURL          string            `json:"avatarUrl"`
	Protocol           string            `json:"protocol"`
	ProviderInstanceID string            `json:"providerInstanceId"`
	PhoneNumbers       []string          `json:"phoneNumbers"`
	Emails             []string          `json:"emails"`
	Address            string            `json:"address,omitempty"`
	Company            string            `json:"company,omitempty"`
	JobTitle           string            `json:"jobTitle,omitempty"`
	Department         string            `json:"department,omitempty"`
	Timezone           string            `json:"timezone,omitempty"`
	Presence           string            `json:"presence,omitempty"`
	StatusText         string            `json:"statusText,omitempty"`
	StatusEmoji        string            `json:"statusEmoji,omitempty"`
	LastSeen           *time.Time        `json:"lastSeen,omitempty"`
	ProviderFields     map[string]string `json:"providerFields"`
}

// ContactExchangeStats contains aggregates calculated from Loom's persisted history.
type ContactExchangeStats struct {
	IsGroup                   bool       `json:"isGroup"`
	TotalMessages             int64      `json:"totalMessages"`
	SentMessages              int64      `json:"sentMessages"`
	ReceivedMessages          int64      `json:"receivedMessages"`
	ActiveDays                int64      `json:"activeDays"`
	AttachmentMessages        int64      `json:"attachmentMessages"`
	ReactionsGiven            int64      `json:"reactionsGiven"`
	ReactionsReceived         int64      `json:"reactionsReceived"`
	Calls                     int64      `json:"calls"`
	MissedCalls               int64      `json:"missedCalls"`
	TotalCallDurationSecs     int64      `json:"totalCallDurationSecs"`
	FirstExchange             *time.Time `json:"firstExchange,omitempty"`
	LastExchange              *time.Time `json:"lastExchange,omitempty"`
	MedianContactResponseSecs *int64     `json:"medianContactResponseSecs,omitempty"`
	MedianMyResponseSecs      *int64     `json:"medianMyResponseSecs,omitempty"`
}

// CommunicationCount contains directional message aggregates.
type CommunicationCount struct {
	Total    int64 `json:"total"`
	Sent     int64 `json:"sent"`
	Received int64 `json:"received"`
}

// CommunicationSeriesPoint is one time bucket in the statistics chart.
type CommunicationSeriesPoint struct {
	Timestamp time.Time `json:"timestamp"`
	CommunicationCount
}

// InstanceCommunicationStats contains aggregates for one configured provider instance.
type InstanceCommunicationStats struct {
	ProviderInstanceID string `json:"providerInstanceId"`
	ProviderID         string `json:"providerId"`
	InstanceName       string `json:"instanceName"`
	CommunicationCount
	CallCount            int64 `json:"callCount"`
	CallDurationSecs     int64 `json:"callDurationSecs"`
	CallsWithoutDuration int64 `json:"callsWithoutDuration"`
}

// ContactCommunicationStats contains aggregates for one contact/provider-instance pair.
type ContactCommunicationStats struct {
	MetaContactID      uint   `json:"metaContactId"`
	DisplayName        string `json:"displayName"`
	AvatarURL          string `json:"avatarUrl"`
	ProviderInstanceID string `json:"providerInstanceId"`
	ProviderID         string `json:"providerId"`
	InstanceName       string `json:"instanceName"`
	CommunicationCount
	CallCount            int64 `json:"callCount"`
	CallDurationSecs     int64 `json:"callDurationSecs"`
	CallsWithoutDuration int64 `json:"callsWithoutDuration"`
}

// CommunicationStats is the complete on-demand dashboard payload.
type CommunicationStats struct {
	From            time.Time                    `json:"from"`
	To              time.Time                    `json:"to"`
	Summary         CommunicationCount           `json:"summary"`
	PreviousSummary CommunicationCount           `json:"previousSummary"`
	Series          []CommunicationSeriesPoint   `json:"series"`
	Instances       []InstanceCommunicationStats `json:"instances"`
	Contacts        []ContactCommunicationStats  `json:"contacts"`
}

// LinkedAccount represents a protocol-specific account (WhatsApp, Slack, etc.).
type LinkedAccount struct {
	ID                 uint           `gorm:"primarykey" json:"id"`
	MetaContactID      uint           `gorm:"index:idx_la_meta_contact_id" json:"metaContactId"`
	Protocol           string         `gorm:"index" json:"protocol"`           // "slack", "whatsapp", "google_messages"
	ProviderInstanceID string         `gorm:"index" json:"providerInstanceId"` // ID of the provider instance (e.g., "whatsapp-1")
	UserID             string         `json:"userId"`                          // User's ID on the remote platform (canonical ID, e.g., phone number for WhatsApp)
	Username           string         `json:"username"`
	AvatarURL          string         `json:"avatarUrl,omitempty"`                 // Profile picture URL from the provider
	Status             string         `json:"status"`                              // "online", "offline", "away", "busy", etc.
	IsGroup            bool           `json:"isGroup"`                             // Whether this account represents a group/channel
	LastSeen           *time.Time     `json:"lastSeen,omitempty"`                  // Last seen timestamp (nil if not available)
	Extra              string         `gorm:"type:text" json:"extra,omitempty"`    // JSON-encoded extra data (e.g., LID mappings for WhatsApp)
	Conversations      []Conversation `gorm:"foreignKey:LinkedAccountID" json:"-"` // Avoid JSON cycles
	ConversationID     string         `gorm:"-" json:"conversationId,omitempty"`   // Computed: ProtocolConvID of the associated Conversation (not persisted)
	CreatedAt          time.Time      `json:"createdAt"`
	UpdatedAt          time.Time      `json:"updatedAt"`
}

// Conversation represents a chat (Direct, Group).
type Conversation struct {
	ID                uint               `gorm:"primarykey" json:"id"`
	LinkedAccountID   uint               `json:"linkedAccountId"`
	ProtocolConvID    string             `gorm:"uniqueIndex" json:"protocolConvId"` // Conversation ID on the platform
	IsGroup           bool               `json:"isGroup"`
	ConversationType  string             `json:"conversationType,omitempty"` // group, group_message, private_channel, public_channel
	GroupName         string             `json:"groupName,omitempty"`
	IsPinned          bool               `json:"isPinned"`                                                     // Whether the conversation is pinned
	IsMuted           bool               `json:"isMuted"`                                                      // Whether the conversation is muted
	GroupParticipants []GroupParticipant `gorm:"foreignKey:ConversationID" json:"groupParticipants,omitempty"` // Group participants (only for groups)
	Messages          []Message          `gorm:"foreignKey:ConversationID" json:"messages"`
	CreatedAt         time.Time          `json:"createdAt"`
	UpdatedAt         time.Time          `json:"updatedAt"`
}

// OpenConversationRequest describes the provider-neutral new conversation flow.
type OpenConversationRequest struct {
	ProviderInstanceID string   `json:"providerInstanceId"`
	ParticipantIDs     []string `json:"participantIds"`
	ConversationType   string   `json:"conversationType"`
	Title              string   `json:"title"`
}

// ConversationResolution either contains one or more existing matching
// conversations, or the newly created conversation in Created.
type ConversationResolution struct {
	Matches []MetaContact `json:"matches"`
	Created *MetaContact  `json:"created,omitempty"`
}

// GroupParticipant represents a participant in a group conversation.
type GroupParticipant struct {
	ID             uint      `gorm:"primarykey" json:"id"`
	ConversationID uint      `gorm:"index" json:"conversationId"`
	UserID         string    `json:"userId"`          // User ID on the platform
	IsAdmin        bool      `json:"isAdmin"`         // Whether the participant is an admin
	IsSelf         bool      `gorm:"-" json:"isSelf"` // Whether this participant is the authenticated user
	JoinedAt       time.Time `json:"joinedAt"`        // When the participant joined
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// GroupDetails contains provider-independent group metadata shown in the
// conversation details panel.
type GroupDetails struct {
	ConversationID  string `json:"conversationId"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	AvatarURL       string `json:"avatarUrl"`
	IsMember        bool   `json:"isMember"`
	CanSendMessages bool   `json:"canSendMessages"`
}

// Message contains the content of a message.
type Message struct {
	ID               uint             `gorm:"primarykey" json:"id"`
	ConversationID   uint             `json:"conversationId"`
	ProtocolConvID   string           `gorm:"index:idx_protocol_conv_id_timestamp,priority:1;index:idx_protocol_conv_id;index:idx_messages_deleted_conv,priority:2;index:idx_msg_conv_ts_del,priority:1" json:"protocolConvId"` // Conversation ID on the platform
	ProtocolMsgID    string           `gorm:"uniqueIndex" json:"protocolMsgId"`                                                                                                                                                 // Message ID on the platform
	SenderID         string           `json:"senderId"`                                                                                                                                                                         // Sender's ID on the platform
	SenderName       string           `json:"senderName,omitempty"`                                                                                                                                                             // Human-readable sender name
	SenderAvatarURL  string           `json:"senderAvatarUrl,omitempty"`                                                                                                                                                        // Sender's avatar URL
	Body             string           `json:"body"`
	Timestamp        time.Time        `gorm:"index:idx_protocol_conv_id_timestamp,priority:2;index:idx_msg_conv_ts_del,priority:2" json:"timestamp"`
	IsFromMe         bool             `json:"isFromMe"`
	ThreadID         *string          `gorm:"index" json:"threadId,omitempty"`                   // Nullable, for replies
	ThreadReplyCount int              `gorm:"-" json:"threadReplyCount"`                         // Lightweight thread metadata; replies are loaded on demand
	QuotedMessageID  *string          `gorm:"index" json:"quotedMessageId,omitempty"`            // ID of the message being replied to
	QuotedSenderID   *string          `json:"quotedSenderId,omitempty"`                          // Sender ID of the quoted message
	QuotedSenderName string           `json:"quotedSenderName,omitempty"`                        // Sender name of the quoted message
	QuotedBody       *string          `json:"quotedBody,omitempty"`                              // Body of the quoted message
	Attachments      string           `json:"attachments"`                                       // Could be a JSON []string of URLs/paths
	Reactions        []Reaction       `gorm:"foreignKey:MessageID" json:"reactions,omitempty"`   // Reactions to this message
	Receipts         []MessageReceipt `gorm:"foreignKey:MessageID" json:"receipts,omitempty"`    // Delivery and read receipts
	IsStatusMessage  bool             `json:"isStatusMessage"`                                   // Whether this is a status message
	IsDeleted        bool             `json:"isDeleted"`                                         // Flag when the remote client deleted the message
	DeletedBy        string           `json:"deletedBy,omitempty"`                               // User ID who triggered the deletion
	DeletedReason    string           `json:"deletedReason,omitempty"`                           // Reason (e.g., "revoked")
	DeletedTimestamp *time.Time       `json:"deletedTimestamp,omitempty"`                        // When the deletion happened
	IsEdited         bool             `json:"isEdited"`                                          // Flag when the message has been edited
	EditedTimestamp  *time.Time       `json:"editedTimestamp,omitempty"`                         // When the message was edited
	IsForwarded      bool             `json:"isForwarded"`                                       // Whether the provider marked the message as forwarded
	HighlightReasons []string         `gorm:"serializer:json" json:"highlightReasons,omitempty"` // Canonical reasons why this message belongs in the attention inbox
	CallType         string           `json:"callType,omitempty"`                                // Type of call: "missed_voice", "missed_video", "missed_group_voice", "missed_group_video", "scheduled_start", "scheduled_cancel", "linked_group_start"
	CallDurationSecs *int32           `json:"callDurationSecs,omitempty"`                        // Duration of the call in seconds (from CallLogMessage)
	CallParticipants string           `json:"callParticipants,omitempty"`                        // JSON array of participant JIDs (from CallLogMessage)
	CallOutcome      string           `json:"callOutcome,omitempty"`                             // Call outcome: "CONNECTED", "MISSED", "FAILED", etc. (from CallLogMessage)
	CallIsVideo      bool             `json:"callIsVideo"`                                       // Whether the call was a video call (from CallLogMessage)
	CallUrl          string           `json:"callUrl,omitempty"`                                 // URL to join or view the call in a browser (provider-specific)
	CallLinkAction   string           `json:"callLinkAction,omitempty"`                          // Generic link action: "join" (default) or "open"
	DeletedAt        gorm.DeletedAt   `gorm:"index;index:idx_messages_deleted_conv,priority:1;index:idx_msg_conv_ts_del,priority:3" json:"-"`
}

// ThreadSummary is lightweight metadata for a message thread. It deliberately
// excludes the reply content, which is retrieved only when a thread is opened.
type ThreadSummary struct {
	ParentMessageID string              `json:"parentMessageId"`
	ReplyCount      int                 `json:"replyCount"`
	Participants    []ThreadParticipant `gorm:"-" json:"participants"`
}

// ThreadParticipant contains only the identity fields needed by thread previews.
type ThreadParticipant struct {
	SenderID        string `json:"senderId"`
	SenderName      string `json:"senderName,omitempty"`
	SenderAvatarURL string `json:"senderAvatarUrl,omitempty"`
	IsFromMe        bool   `json:"isFromMe"`
}

// UnreadMessageLocation lets the renderer locate unread messages that are not
// part of the paginated main timeline (notably native thread replies).
type UnreadMessageLocation struct {
	MessageID string    `json:"messageId"`
	ThreadID  string    `json:"threadId,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	IsFromMe  bool      `json:"isFromMe"`
}

// ScheduledMessage is a provider-neutral representation of a message queued
// for future delivery by the remote service.
type ScheduledMessage struct {
	ID                 string    `json:"id"`
	ProviderInstanceID string    `json:"providerInstanceId"`
	ProtocolConvID     string    `json:"protocolConvId"`
	Body               string    `json:"body"`
	ScheduledAt        time.Time `json:"scheduledAt"`
	CreatedAt          time.Time `json:"createdAt"`
}

// MessagePinScope describes who can see a provider-side message pin.
type MessagePinScope string

const (
	MessagePinScopePersonal MessagePinScope = "personal"
	MessagePinScopeShared   MessagePinScope = "shared"
)

// MessagePinResolution tracks whether Loom has the pinned message locally.
type MessagePinResolution string

const (
	MessagePinResolutionResolved    MessagePinResolution = "resolved"
	MessagePinResolutionUnresolved  MessagePinResolution = "unresolved"
	MessagePinResolutionLoading     MessagePinResolution = "loading"
	MessagePinResolutionUnavailable MessagePinResolution = "unavailable"
)

// MessagePin persists provider-side pin state independently from message history.
// Message is populated for API responses only and is not stored in this table.
type MessagePin struct {
	ID                 uint                 `gorm:"primarykey" json:"id"`
	ProviderInstanceID string               `gorm:"uniqueIndex:idx_message_pin_provider_message,priority:1;index" json:"providerInstanceId"`
	ProtocolConvID     string               `gorm:"index" json:"protocolConvId"`
	ProtocolMsgID      string               `gorm:"uniqueIndex:idx_message_pin_provider_message,priority:2;index" json:"protocolMsgId"`
	SenderID           string               `json:"senderId,omitempty"`
	MessageIsFromMe    bool                 `json:"messageIsFromMe"`
	Scope              MessagePinScope      `json:"scope"`
	Resolution         MessagePinResolution `json:"resolution"`
	PinnedAt           *time.Time           `json:"pinnedAt,omitempty"`
	MessageTimestamp   *time.Time           `json:"messageTimestamp,omitempty"`
	ProviderPinID      string               `json:"providerPinId,omitempty"`
	Message            *Message             `gorm:"-" json:"message,omitempty"`
	CreatedAt          time.Time            `json:"createdAt"`
	UpdatedAt          time.Time            `json:"updatedAt"`
}

// MessageContext is a bounded historical window centered on a target message.
type MessageContext struct {
	TargetMessageID string    `json:"targetMessageId"`
	Messages        []Message `json:"messages"`
	HasMoreBefore   bool      `json:"hasMoreBefore"`
	HasMoreAfter    bool      `json:"hasMoreAfter"`
}

// MessageSearchResult contains the message and the conversation metadata needed
// to render a global database search result without additional frontend queries.
type MessageSearchResult struct {
	Message            Message `gorm:"embedded" json:"message"`
	MetaContactID      uint    `json:"metaContactId"`
	ConversationName   string  `json:"conversationName"`
	ConversationAvatar string  `json:"conversationAvatar"`
	Protocol           string  `json:"protocol"`
	ProviderInstanceID string  `json:"providerInstanceId"`
}

type MessageSearchPage struct {
	Items   []MessageSearchResult `json:"items"`
	HasMore bool                  `json:"hasMore"`
}

// HighlightedMessageRef is the lightweight identity used to count unread
// attention-inbox messages without loading every paginated message body.
type HighlightedMessageRef struct {
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
}

const HighlightReasonDirectMention = "direct_mention"

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
	MessageID uint      `gorm:"index:idx_reactions_msg_id_created_at,priority:1" json:"messageId"`                                // Foreign key to Message
	UserID    string    `json:"userId"`                                                                                           // User who reacted
	Emoji     string    `json:"emoji"`                                                                                            // Emoji reaction (e.g., "👍", "❤️")
	CreatedAt time.Time `gorm:"index:idx_reactions_msg_id_created_at,priority:2;index:idx_reactions_created_at" json:"createdAt"` // Index for last reaction timestamps
	UpdatedAt time.Time `json:"updatedAt"`
}

// Attachment represents a file attached to a message.
type Attachment struct {
	Type          string     `json:"type"`                // "image", "video", "audio", "document", "sticker"
	URL           string     `json:"url"`                 // Local file path or remote URL
	FileName      string     `json:"fileName"`            // Original filename
	FileSize      int64      `json:"fileSize"`            // File size in bytes
	MimeType      string     `json:"mimeType"`            // MIME type (e.g., "image/jpeg", "application/pdf")
	Thumbnail     string     `json:"thumbnail,omitempty"` // Thumbnail URL for images/videos (optional)
	Duration      uint32     `json:"duration,omitempty"`  // Duration in seconds (for audio/video)
	Latitude      *float64   `json:"latitude,omitempty"`  // Latitude for location attachments
	Longitude     *float64   `json:"longitude,omitempty"` // Longitude for location attachments
	LocationName  string     `json:"locationName,omitempty"`
	Address       string     `json:"address,omitempty"`
	IsLive        bool       `json:"isLive,omitempty"`
	Accuracy      uint32     `json:"accuracy,omitempty"`
	UpdatedAt     *time.Time `json:"locationUpdatedAt,omitempty"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	ContactName   string     `json:"contactName,omitempty"`
	ContactPhones []string   `json:"contactPhones,omitempty"`
	CardJSON      string     `json:"cardJson,omitempty"` // Structured provider card payload (e.g. Teams Adaptive Card)
}

// ProviderConfiguration stores the configuration of a provider instance.
type ProviderConfiguration struct {
	ID           uint       `gorm:"primarykey" json:"id"`
	ProviderID   string     `gorm:"index;not null" json:"providerId"` // e.g., "whatsapp", "mock"
	InstanceID   string     `gorm:"uniqueIndex" json:"instanceId"`    // Unique instance identifier (e.g., "whatsapp-1", "whatsapp-2") - nullable for migration compatibility
	InstanceName string     `gorm:"" json:"instanceName"`             // Display name for this instance (e.g., "WhatsApp Personal", "WhatsApp Work") - nullable for migration compatibility
	ConfigJSON   string     `gorm:"type:text" json:"configJson"`      // JSON-encoded configuration
	IsActive     bool       `json:"isActive"`                         // Whether this provider is currently active
	LastSyncAt   *time.Time `json:"lastSyncAt,omitempty"`             // Last time messages were synced
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

// ContactAlias stores user-defined custom names for contacts.
type ContactAlias struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    string    `gorm:"uniqueIndex;not null" json:"userId"` // User ID on the platform (e.g., WhatsApp JID)
	Alias     string    `gorm:"not null" json:"alias"`              // Custom name set by the user
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
