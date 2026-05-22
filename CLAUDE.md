# Loom — Architecture

## Overview

Wails desktop application (Go backend + React frontend). The frontend communicates with the backend via the Wails-generated bindings in `frontend/wailsjs/`.

## Message view architecture

The message view was historically a single `MessageList.tsx` file of 2700+ lines. It has been split into distinct layers:

```
src/
├── lib/
│   └── messageUtils.ts          — Pure utilities (no React hooks)
├── hooks/
│   ├── useMessageData.ts        — Data fetching + processing
│   ├── useFileUpload.ts         — File upload + drag & drop
│   └── useMessageEdit.ts        — Inline edit state + keyboard navigation
└── components/
    ├── MessageList.tsx           — Orchestrator (~280 lines)
    ├── MessageBubbleItem.tsx     — Renders a message in "bubble" layout
    ├── MessageIRCItem.tsx        — Renders a message in "irc" layout
    ├── MessageDateSeparator.tsx  — Date separator
    ├── MessageUnreadDivider.tsx  — "New messages" divider
    └── MessageThreadPreview.tsx  — Last message preview for a thread
```

### `src/lib/messageUtils.ts`

Pure functions with no React dependency. Shared between `MessageList`, `MessageBubbleItem`, `MessageIRCItem`, and `ThreadView`.

- `getMessageDomId(message)` — stable DOM ID (protocolMsgId > message-{id} > ts-{timestamp})
- `isDifferentDay(date1, date2)` — date comparison for separators
- `formatDateSeparator(date, t)` — localized label (today / yesterday / Monday May 5…)
- `getColorFromString(str)` — deterministic HSL color from a userId (for IRC display)
- `getSenderDisplayName(senderName, senderId, isFromMe, t)` — displayed name, with WhatsApp number formatting

### `src/hooks/useMessageData.ts`

Encapsulates all data logic, no JSX.

- `useInfiniteQuery` for pagination (50 messages per page, scroll up = load more)
- Deduplication by `protocolMsgId`
- Separation of main messages / threads (`threadsByParent`)
- Loading participant names via `GetParticipantNames`
- Sync with `useMessageReadStore` (sync + cleanup of stale messages)
- Query invalidation after loading

Signature: `useMessageData(conversationId: string, isGroupFromProvider: boolean)`

### `src/hooks/useFileUpload.ts`

Everything file-related, kept in a single hook because both flows (drag & drop and retry/delete local) mutate the React Query cache.

- Drag state management (`isDragging`, `pendingFiles`, `pendingFilePaths`)
- `handleFileUpload`: file path priority (`SendFileFromPath`), JS FileReader fallback, Go clipboard fallback
- Image compression before sending (>1 MB → JPEG 1600px max)
- `handleRetrySend` / `handleDeleteLocalMessage`: optimistic cache updates

Signature: `useFileUpload(conversationId: string)`

### `src/hooks/useMessageEdit.ts`

State and behaviors for inline message editing.

- `editingMessageId`, `editingText`, `originalEditText`
- `editingInputRef`: ref attached to the `<Input>` in `MessageBubbleItem` for capture-phase keyboard listening
- `handleNavigateToEdit(direction, returnFocusToInput?)`: up/down navigation between sent messages (ArrowUp = older, ArrowDown = newer, then back to input)
- Capture-phase keyboard listener on the edit input (to intercept before Virtuoso)

Signature: `useMessageEdit({ messages, conversationId, showToast, t })`

### `MessageList.tsx` — Orchestrator

Remaining responsibilities in the main component:
- Calling the three hooks + assembling props
- Window focus/blur handling (for read marking)
- Mark conversation as read effect (`MarkConversationAsRead`)
- "New messages" divider (calculation + auto-dismiss after 10s)
- Delete confirmation (`AlertDialog`)
- `handleReaction` with optimistic cache update
- `MessageHandlers` interface passed to both layouts

### `MessageBubbleItem` / `MessageIRCItem`

Both receive the same props via a `handlers: MessageHandlers` object (defined in `MessageBubbleItem.tsx` and re-exported). Key differences:
- **Bubble**: avatar + timestamp left/right based on `isFromMe`, colored background, `editingInputRef` attached for ArrowUp/Down keyboard navigation
- **IRC**: fixed 60px left column, grouping of consecutive messages from the same sender (< 5 min), per-sender color via `getColorFromString`

Both share `MessageDateSeparator`, `MessageUnreadDivider`, `MessageThreadPreview`.

### `ThreadView.tsx`

Now uses `getColorFromString` and `getSenderDisplayName` from `@/lib/messageUtils` instead of redefining them locally.

## Conventions

- The "bubble" and "irc" layouts are mutually exclusive branches in `Virtuoso.itemContent`
- `MessageHandlers` is the shared interface for all interaction callbacks — passing it as a `handlers` prop avoids prop explosion
- Optimistic React Query cache updates follow the pattern: immediate `queryClient.setQueryData` → API call → rollback on error
- `getMessageDomId` is the source of truth for message DOM IDs (used everywhere for scrolling, read marking, etc.)

---

## Go provider architecture

### File structure

```
pkg/
├── core/
│   ├── provider.go          — Provider interface + Attachment type + Capabilities
│   ├── config.go            — ProviderConfig (map[string]interface{} with GetString/GetInt/GetBool helpers)
│   ├── events.go            — All event types (ProviderEvent + implementations)
│   └── provider_manager.go  — ProviderManager: registration, instantiation, persistence
├── providers/
│   ├── <name>_export.go     — Public re-export of NewXxxProvider() → core.Provider
│   ├── slack/
│   │   ├── provider.go      — Struct + Init/Connect/Disconnect/Cleanup/GetCapabilities
│   │   ├── messages.go      — GetConversationHistory / SendMessage / SendReply / SendFile / EditMessage / DeleteMessage
│   │   ├── contacts.go      — GetContacts / GetContactName / RefreshContact
│   │   ├── events.go        — StreamEvents + receiving goroutines
│   │   ├── groups.go        — CreateGroup / UpdateGroupName / Add/RemoveGroupParticipants / LeaveGroup / …
│   │   ├── receipts.go      — MarkMessageAsRead / MarkConversationAsRead / MarkMessageAsPlayed
│   │   ├── state.go         — PinConversation / UnpinConversation / MuteConversation / UnmuteConversation / GetConversationState
│   │   └── …               — Additional files (rtm.go, socket.go, sync.go, typing.go, …)
│   └── whatsapp/
│       └── …               — Same domain-based split
└── models/
    └── models.go            — models.Message, models.LinkedAccount, models.Conversation, models.GroupParticipant
```

### `core.Provider` — Full interface

Every provider must implement **all** methods. Unsupported features return `fmt.Errorf("not supported")` (or a zero value).

**Lifecycle**

| Method | Role |
|---|---|
| `Init(config ProviderConfig) error` | Initialize with config (called at instance creation) |
| `GetConfig() ProviderConfig` | Return the current config |
| `SetConfig(config ProviderConfig) error` | Update config (may recreate the internal client) |
| `IsAuthenticated() bool` | `true` if already authenticated — determines auto-reconnect on startup |
| `Connect() error` | Establish the network connection, start background goroutines |
| `Disconnect() error` | Close the connection, stop goroutines |
| `SyncHistory(since time.Time) error` | Catch up on history since a given date |
| `Cleanup() error` | Delete local data (files, DB) for this instance |

**Events**

| Method | Role |
|---|---|
| `StreamEvents() (<-chan ProviderEvent, error)` | Return the real-time event channel |

**Contacts & conversations**

| Method | Role |
|---|---|
| `GetContacts() ([]models.LinkedAccount, error)` | Contact list with current status |
| `GetContactName(contactID string) (string, error)` | Resolve an ID to a display name |
| `RefreshContact(contactID string) error` | Refresh contact metadata (avatar, name) |
| `GetConversationHistory(convID string, limit int, beforeTS *time.Time, sinceTS *time.Time) ([]models.Message, error)` | Paginated history, ordered oldest first |

**Sending messages**

| Method | Role |
|---|---|
| `SendMessage(convID, text string, file *Attachment, threadID *string) (*models.Message, error)` | Text message ± attachment ± thread |
| `SendReply(convID, text, quotedMessageID string) (*models.Message, error)` | Quoted reply to an existing message |
| `SendFile(convID string, file *Attachment, threadID *string) (*models.Message, error)` | File only, no text |
| `EditMessage(convID, messageID, newText string) (*models.Message, error)` | Edit an existing message |
| `DeleteMessage(convID, messageID string) error` | Delete a message |
| `SendTypingIndicator(convID string, isTyping bool) error` | Typing indicator |
| `SendStatusMessage(text string, file *Attachment) (*models.Message, error)` | Status message (broadcast) |
| `SendRetryReceipt(convID, messageID string) error` | Retry when decryption fails |

**Threads**

| Method | Role |
|---|---|
| `GetThreads(parentMessageID string) ([]models.Message, error)` | All replies in a thread |

**Reactions**

| Method | Role |
|---|---|
| `AddReaction(convID, messageID, emoji string) error` | Add an emoji reaction |
| `RemoveReaction(convID, messageID, emoji string) error` | Remove an emoji reaction |

**Group management**

| Method | Role |
|---|---|
| `CreateGroup(groupName string, participantIDs []string) (*models.Conversation, error)` | Create a group |
| `UpdateGroupName(convID, newName string) error` | Rename a group |
| `AddGroupParticipants(convID string, participantIDs []string) error` | Add members |
| `RemoveGroupParticipants(convID string, participantIDs []string) error` | Remove members |
| `LeaveGroup(convID string) error` | Leave a group |
| `PromoteGroupAdmins(convID string, participantIDs []string) error` | Promote to admin |
| `DemoteGroupAdmins(convID string, participantIDs []string) error` | Demote from admin |
| `GetGroupParticipants(convID string) ([]models.GroupParticipant, error)` | List members |
| `CreateGroupInviteLink(convID string) (string, error)` | Generate an invite link |
| `RevokeGroupInviteLink(convID string) error` | Revoke the invite link |
| `JoinGroupByInviteLink(inviteLink string) (*models.Conversation, error)` | Join via link |
| `JoinGroupByInviteMessage(inviteMessageID string) (*models.Conversation, error)` | Join via invite message |

**Receipts & state**

| Method | Role |
|---|---|
| `MarkMessageAsRead(convID, messageID string) error` | Mark a message as read |
| `MarkConversationAsRead(convID string) error` | Mark an entire conversation as read |
| `MarkMessageAsPlayed(convID, messageID string) error` | Mark a voice message as played |
| `PinConversation(convID string) error` | Pin a conversation |
| `UnpinConversation(convID string) error` | Unpin a conversation |
| `MuteConversation(convID string) error` | Mute notifications |
| `UnmuteConversation(convID string) error` | Unmute notifications |
| `GetConversationState(convID string) (*models.Conversation, error)` | Current pin/mute state |

**Capabilities & emojis**

| Method | Role |
|---|---|
| `GetCapabilities() Capabilities` | Declare supported features (see below) |
| `GetCustomEmojis() (map[string]string, error)` | Map of name → URL for custom emojis |
| `GetAuthQRCode() (string, error)` | Auth QR code (base64) — return an error if not supported |

### `core.Capabilities` — Declaring supported features

```go
type Capabilities struct {
    SupportsThreads          bool // GetThreads / SendMessage with threadID
    SupportsReactions        bool // AddReaction / RemoveReaction
    SupportsCustomEmojis     bool // GetCustomEmojis returns data
    SupportsTypingIndicator  bool // SendTypingIndicator
    SupportsGroupManagement  bool // Create/Update/Add/Remove/Leave…
    SupportsDeleteMessage    bool // DeleteMessage
    SupportsEditMessage      bool // EditMessage
    SupportsReadReceipts     bool // MarkMessageAsRead / MarkConversationAsRead
    SupportsPinConversation  bool // PinConversation / UnpinConversation
    SupportsMuteConversation bool // MuteConversation / UnmuteConversation
    SupportsQRCodeAuth       bool // GetAuthQRCode returns a real QR code
}
```

### `core.ProviderConfig` — Key-value configuration

`ProviderConfig` is a `map[string]interface{}`. Available helpers: `GetString(key)`, `GetInt(key)`, `GetBool(key)`, `Set(key, value)`.

Reserved keys injected automatically by the `ProviderManager`:

| Key | Type | Role |
|---|---|---|
| `_instance_id` | string | Unique instance ID (e.g. `"slack-1"`) |
| `_instance_name` | string | Display name (e.g. `"Slack Personal"`) |

### `core.ProviderEvent` — Real-time events

All events implement `Type() EventType`. The provider pushes them into its internal `eventChan`, which `StreamEvents()` returns.

| Event type | Struct | Key fields |
|---|---|---|
| `"message"` | `MessageEvent` | `InstanceID`, `Message models.Message` |
| `"reaction"` | `ReactionEvent` | `InstanceID`, `ConversationID`, `MessageID`, `UserID`, `Emoji`, `Added bool` |
| `"typing"` | `TypingEvent` | `InstanceID`, `ConversationID`, `UserID`, `UserName`, `IsTyping bool` |
| `"contact_status"` | `ContactStatusEvent` | `InstanceID`, `UserID`, `Status`, `StatusEmoji`, `StatusText`, `LastSeen *int64` |
| `"presence"` | `PresenceEvent` | `InstanceID`, `UserID`, `IsOnline bool`, `LastSeen int64` |
| `"group_change"` | `GroupChangeEvent` | `InstanceID`, `ConversationID`, `ChangeType` (`created`/`updated`/`participant_added`/…) |
| `"receipt"` | `ReceiptEvent` | `InstanceID`, `ConversationID`, `MessageID`, `ReceiptType` (`delivery`/`read`/`played`/`self_read`) |
| `"retry_receipt"` | `RetryReceiptEvent` | `InstanceID`, `ConversationID`, `MessageID`, `UserID` |
| `"sync_status"` | `SyncStatusEvent` | `InstanceID`, `Status` (`fetching_contacts`/`fetching_history`/`fetching_avatars`/`completed`/`error`), `Message`, `Progress` |
| `"conversation_read_status"` | `ConversationReadStatusEvent` | `InstanceID`, `ConversationID`, `LastReadTS` |

### Adding a new provider — Checklist

**1. Create the package**

```
pkg/providers/<name>/
    provider.go     — XxxProvider struct + lifecycle
    messages.go     — messages (GetConversationHistory, Send…)
    contacts.go     — GetContacts, GetContactName, RefreshContact
    events.go       — StreamEvents + receiving goroutine
    groups.go       — group management (if supported)
    receipts.go     — read receipts (if supported)
    state.go        — pin/mute (if supported)
```

**2. Minimal skeleton in `provider.go`**

```go
package <name>

import "Loom/pkg/core"

type XxxProvider struct {
    config    core.ProviderConfig
    eventChan chan core.ProviderEvent
    stopChan  chan struct{}
    // …
}

// Static interface compliance check
var _ core.Provider = (*XxxProvider)(nil)

func NewXxxProvider() *XxxProvider {
    return &XxxProvider{
        eventChan: make(chan core.ProviderEvent, 500),
        stopChan:  make(chan struct{}),
    }
}
```

**3. Create the export file** `pkg/providers/<name>_export.go`

```go
package providers

import (
    "Loom/pkg/core"
    <name> "Loom/pkg/providers/<name>"
)

func NewXxxProvider() core.Provider {
    return <name>.NewXxxProvider()
}
```

**4. Register in `app.go`**

```go
a.providerManager.RegisterProvider("<name>", core.ProviderInfo{
    ID:          "<name>",
    Name:        "Display Name",
    Description: "Short description",
    ConfigSchema: map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "token": map[string]interface{}{
                "type":        "string",
                "title":       "Auth Token",
                "description": "…",
            },
            // … other config fields
        },
        "required": []string{"token"},
    },
}, func() core.Provider {
    return providers.NewXxxProvider()
})
```

**5. Implement `GetCapabilities()`** — set to `false` anything that is not supported.

**6. Unsupported methods** — return `fmt.Errorf("<name>: <Method> not supported")` to maintain compile-time compliance without panicking.

**7. Verify compilation**

```bash
go build ./...
```

The line `var _ core.Provider = (*XxxProvider)(nil)` guarantees a clear compile error if any method is missing.
