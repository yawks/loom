# Creating a new Loom provider

This is the reference guide for integrating a new messaging source into Loom. It is designed to be followed deterministically by a developer or an AI: do not skip the decision sections, even if the provider appears simple.

Reference implementations:

- `pkg/providers/whatsapp`: QR authentication, local storage, and rich events;
- `pkg/providers/slack`: remote API, WebSocket/RTM, incremental sync, and persistence;
- `pkg/providers/googlechat`: OAuth2, polling, and per-instance token isolation.

## 1. Define the scope before writing code

Explicitly answer every question below. The answers determine the architecture, configuration schema, capabilities, and test plan.

### Identity and remote model

1. What is the provider's stable, lowercase identifier (`discord`, `telegram`, etc.)? It must also be the prefix for instances (`discord-1`, `discord-2`).
2. What are the canonical remote identifiers for the user, a conversation, a message, a thread, and an attachment?
3. Are identifiers globally unique, unique only per account, or unique only per conversation?
4. Does a direct conversation have different identifiers depending on direction or client (as with Slack DMs)? If so, which canonical representation must Loom use everywhere?
5. Does the service distinguish DMs, groups, channels, spaces, announcements, or status updates? Which should have `IsGroup` set?
6. Which remote objects can be deleted, edited, expired, or replaced?

### Authentication and security

1. Does the provider use a token, OAuth2, QR code, API key, browser session, or no authentication?
2. Which scopes, permissions, redirect URLs, developer applications, and environments must be created before Loom can run?
3. What is the secret lifecycle: validation, refresh, expiry, revocation, and sign-out?
4. Can the session be restored after a restart without user interaction? If yes, where is per-instance data stored?
5. Which fields are secrets? **Important: user configuration is serialized as JSON in SQLite.** Do not put secrets in logs, errors, events, or fields that do not need them; document the risk and use separate secure storage when appropriate.
6. What should `IsAuthenticated()` return when an expired token is still present? It should return `false`, not `true`.

### Features and constraints

For every row, answer “supported”, “not supported”, or “supported with limitations” and describe the limitation.

| Feature | Questions to decide |
| --- | --- |
| Messages | Empty text, markdown/rich text, mentions, links, system messages, calls, ephemeral messages? |
| Files | Maximum size, upload, download, expiring URL, thumbnail, MIME type, audio/video duration? |
| History | Pagination, API ordering, `before` bound, `since` bound, rate limits, retention? |
| Real time | WebSocket, webhook, long polling, polling; reconnection and deduplication? |
| Replies/threads | Quoted reply, native thread, both, or neither? |
| Reactions | Unicode or internal name, add/remove, one reaction per user? |
| Presence/typing | Incoming events, sending support, expiration delay? |
| Groups | Creation, names, members, admin roles, invitations, leaving? |
| State | Read, played, pinned, muted, deletion, editing? |
| Emojis | Custom emojis and durable URLs? |

### Integration choices

1. Is there a maintained Go library with a project-compatible license? Otherwise, which HTTP/WebSocket API will be used?
2. Which errors are transient and retryable, and which are permanent (authentication, permissions, deleted conversation)?
3. What rate-limit strategy is required: pagination, backoff, maximum concurrency, request delay?
4. Can network calls be cancelled during `Disconnect()`? Use a cancellable `context.Context` whenever possible.
5. Which data belongs in memory, Loom's database, or a directory owned by the provider instance?

Record the answers in the PR or in a short provider-specific document. An unverified assumption must be visible rather than hidden in code.

## 2. Loom contract to implement

The exact contract is `core.Provider` in `pkg/core/provider.go`. Add this safeguard to the package immediately:

```go
var _ core.Provider = (*XxxProvider)(nil)
```

It makes the compiler report every missing method whenever the interface changes.

### Lifecycle and instances

Loom supports several accounts of the same provider type. `ProviderManager`:

1. registers one factory per `providerID`;
2. creates an instance named `providerID-N`;
3. adds `_instance_id` to `core.ProviderConfig` before `Init`;
4. persists configuration without keys beginning with `_`;
5. restores every instance on startup, then connects authenticated ones;
6. calls `Cleanup()` when an instance is removed.

Required consequences:

- Keep no mutable package-global state.
- Use `_instance_id` for session files, caches, external databases, and logs.
- Do not close `eventChan` while a goroutine could still write to it; cancel producers first and wait for them when needed.
- Make `Connect()` and `Disconnect()` as idempotent as possible: configuration can be edited and an instance can be replaced.
- `Cleanup()` must delete only local artifacts for **that** instance and close loggers/connections. Loom relational data is removed by `ProviderManager.RemoveProvider`.

`_instance_id` is currently the only automatically injected internal key. Do not assume `_instance_name` exists in `ProviderConfig`; the display name is stored separately in `models.ProviderConfiguration`.

### Configuration

`core.ProviderConfig` is a `map[string]interface{}`. Use `GetString`, `GetInt`, or `GetBool`: after JSON round-tripping, a number can be a `float64`. Do not directly type-assert a user-provided value.

`Init` should initialize mutexes, the logger, and local dependencies, then call or reproduce `SetConfig` validation. `SetConfig` must leave the provider in a consistent configuration and may recreate its remote client. Never overwrite `_instance_id`.

`GetConfig` must be synchronized. Prefer returning a copy if either the provider or caller may mutate the map; existing providers currently return the stored map, but that is fragile under concurrency.

The React form supports only a reduced JSON-Schema subset: every property is rendered as a text `Input`. `number`, `boolean`, `enum`, `format: password`, and `required` do not currently receive specialized widgets or validation. Therefore validate all fields in Go, and never expose secrets in descriptions or defaults.

### Normalized data

The structures are in `pkg/models/models.go`.

- `LinkedAccount` represents a remote contact or conversation. Always populate `Protocol`, `ProviderInstanceID`, `UserID`, `Username`, `AvatarURL`, `Status`, and `IsGroup` when the remote source provides them.
- `Conversation.ProtocolConvID` is the conversation identifier used by the Provider interface.
- `Message.ProtocolMsgID` is the stable remote message identifier; `ProtocolConvID` must be its canonical conversation; `Timestamp` must be the actual service instant with the correct timezone; `IsFromMe` must not be guessed from the display name.
- `ThreadID` is Loom's parent-message identifier (`ProtocolMsgID`), not necessarily the API's internal thread identifier. Google Chat demonstrates this conversion.
- `QuotedMessageID` identifies a quoted message; populate the `Quoted*` fields too when the API provides them.
- `Attachments` is a string containing a JSON array of `models.Attachment`. The client expects at least `URL`, `FileName`, `FileSize`, and `MimeType`, with optional `Thumbnail` and `Duration`.

Current database constraints are global on `Conversation.ProtocolConvID` and `Message.ProtocolMsgID`. Choose canonical IDs that cannot collide between accounts and providers. If the API does not guarantee this, prefix them consistently (`<instanceID>:<remoteID>`) and apply the rule in **every** operation and event. Confirm that this normalization does not break remote API calls, which may require the raw ID; in that case, implement explicit `toRemoteID` / `toLoomID` functions.

Existing providers persist contacts, conversations, and messages themselves through `db.DB` / `db.ContactStore` during synchronization. Do not assume the application automatically persists a `MessageEvent`: the application listener primarily forwards it to the frontend. Perform idempotent upserts before or alongside emission, and handle a second delivery of the same message without duplication.

### Events

`StreamEvents()` returns the internal `<-chan core.ProviderEvent>` channel. Create it in the constructor with a buffer suitable for the traffic volume (the reference providers use 200 or 500).

Every event must contain the correct `InstanceID`. Available types are defined in `pkg/core/events.go`:

- `MessageEvent` or `MessageBatchEvent` for new messages; use batches during synchronization to avoid flooding the frontend;
- `ReactionEvent`, `TypingEvent`, `ContactStatusEvent`, `PresenceEvent`;
- `GroupChangeEvent`, `ReceiptEvent`, `RetryReceiptEvent`, `ConversationReadStatusEvent`;
- `SyncStatusEvent` for synchronization start/progress/error/completion.

Do not block a network goroutine forever when the event channel is full. Use a `select` with the shutdown context or a timeout, log an acceptable dropped event, and consider a longer timeout for error/completion events. WhatsApp contains an example for sync-status events.

The application forwards events to Wails in `App.startEventListenerForProvider` (`app.go`). This listener starts only after connection. Do not emit critical initial-sync events before the stream is available; the application triggers history synchronization after installing the listener.

### Synchronization and call order

The normal sequence is:

```text
factory registration → creation/restoration → Init
→ Connect → StreamEvents/listener → SyncHistory(since)
→ idempotent persistence + events → Disconnect/Cleanup
```

`Connect` establishes the session and starts only required real-time producers. It should not begin a large blocking synchronization on its own: the application calls `SyncHistory` afterwards and, at startup, defers that work until the frontend can receive events.

`SyncHistory(since)` should:

1. check connection state and return a useful error if synchronization cannot proceed;
2. discover or update contacts and conversations;
3. retrieve the delta since `since`, respecting pagination and rate limits;
4. deduplicate and upsert;
5. emit progress (`fetching_contacts`, `fetching_history`, and possibly `fetching_avatars`);
6. emit exactly one final `completed` or `error` status per run;
7. never block the UI indefinitely. Long work may continue in a controlled goroutine, but its errors must remain observable.

`GetConversationHistory(conversationID, limit, beforeTimestamp, sinceTimestamp)` returns messages oldest first. The contract defines `limit == 0` as unlimited; if the API imposes a maximum, paginate or document the effective limit. Respect both `beforeTimestamp` (backward pagination) and `sinceTimestamp` (incremental sync). Never mix seconds, milliseconds, and local dates.

## 3. Step-by-step implementation

### Step A — Create the package and export

Create a domain-oriented structure; empty files are not required:

```text
pkg/providers/acmechat/
  provider.go      # struct, config, lifecycle, capabilities
  auth.go          # OAuth/QR/token, when needed
  contacts.go      # contacts, cache, avatars
  messages.go      # conversion, history, sending, files
  events.go        # WebSocket/polling, conversion, emission
  groups.go        # only when supported
  receipts.go      # only when supported
  state.go         # pin/mute, when supported
pkg/providers/acmechat_export.go
```

The export separates `pkg/providers` from implementation details:

```go
package providers

import (
    "Loom/pkg/core"
    acmechat "Loom/pkg/providers/acmechat"
)

func NewAcmeChatProvider() core.Provider {
    return acmechat.NewAcmeChatProvider()
}
```

The constructor initializes every structure usable before `Init`: channel, maps, cancellable context, and possible semaphores. This is deliberately minimal:

```go
type AcmeChatProvider struct {
    mu        sync.RWMutex
    config    core.ProviderConfig
    eventChan chan core.ProviderEvent
    ctx       context.Context
    cancel    context.CancelFunc
    // client, logger, caches, wait group…
}

var _ core.Provider = (*AcmeChatProvider)(nil)

func NewAcmeChatProvider() *AcmeChatProvider {
    ctx, cancel := context.WithCancel(context.Background())
    return &AcmeChatProvider{
        config:    make(core.ProviderConfig),
        eventChan: make(chan core.ProviderEvent, 500),
        ctx:       ctx,
        cancel:    cancel,
    }
}
```

Do not reuse a context already cancelled by `Disconnect`; create a new one for each subsequent `Connect`.

### Step B — Implement the complete contract and capabilities

Implement every `core.Provider` method, including methods that do not apply. For an unavailable operation, return an explicit, stable error:

```go
return fmt.Errorf("acmechat: EditMessage not supported")
```

Do not return misleading `nil` or silent success: the frontend may expose actions based on `GetCapabilities()`.

Declare capabilities honestly:

```go
func (p *AcmeChatProvider) GetCapabilities() core.Capabilities {
    return core.Capabilities{
        SupportsThreads:         true,
        SupportsReactions:       true,
        SupportsTypingIndicator: true,
        SupportsDeleteMessage:   true,
        SupportsReadReceipts:    true,
        NativeEmojiReactions:    true, // only if the API expects 👍, not "+1"
    }
}
```

Possible fields are `SupportsThreads`, `SupportsReactions`, `SupportsCustomEmojis`, `SupportsTypingIndicator`, `SupportsGroupManagement`, `SupportsDeleteMessage`, `SupportsEditMessage`, `SupportsReadReceipts`, `SupportsPinConversation`, `SupportsMuteConversation`, `SupportsQRCodeAuth`, and `NativeEmojiReactions`.

`GetCustomEmojis` and `GetAuthQRCode` must return a “not supported” error when the corresponding capability is `false`. `RefreshContact` may be a documented no-op when no metadata can be refreshed, but must not conceal a connection error.

### Step C — Convert remote objects in one place

Write pure, testable functions such as `toModelMessage`, `toLinkedAccount`, `toAttachment`, `normalizeConversationID`, and `remoteConversationID`. Use them for both history and real time; divergent conversion paths are a common source of duplicates and missing conversations.

For every conversion, verify:

- Message ID is non-empty and stable.
- Canonical conversation matches the one used by `Send*`, reactions, edits, and events.
- Author and `IsFromMe` are correct.
- Timestamp is correct in UTC.
- Text/rich text conversion retains essential mentions.
- Thread and quoted reply are resolved without a loop.
- Attachments are serializable with `json.Marshal`.
- Deleted/edited messages use `IsDeleted`, `Deleted*`, `IsEdited`, and `EditedTimestamp` when the API supports them.

Download media into a per-instance directory if Loom needs offline rendering, and use names derived from a safe ID rather than an untrusted remote filename. Create the directory with restrictive permissions. Never treat a remote URL or filename as a trustworthy local path.

### Step D — Persist without duplicate side effects

Adopt a clear idempotency key (usually the canonical message ID, namespaced as described above). Before creating a message, look up the existing record and update fields that can change. Do the same for contacts, conversations, reactions, and receipts.

Ensure a conversation exists, or that orphan messages can later be attached consistently. Slack and Google Chat show conversation creation/association and upsert patterns. Keep database writes outside network locks when possible; do not hold `p.mu` during a slow request.

### Step E — Authentication, reconnection, and shutdown

- `Init` must not need the network for an unauthenticated instance.
- `IsAuthenticated` must be fast and safe; it is used at startup to decide automatic reconnection.
- `Connect` validates configuration before starting goroutines. For interactive OAuth, it is acceptable to start the flow and return before authentication is complete; that state must subsequently lead to a real connection and required events.
- Every provider goroutine must stop through `ctx.Done()` or a stop channel. Avoid non-cancellable `time.Sleep` calls inside loops.
- On network loss, reconnect with bounded backoff, honor `Disconnect`, and do not create multiple receive loops during reconnection.
- `Disconnect` cancels contexts, closes network connections, waits for goroutines when needed, then updates connection state. It must not destroy a persisted session unless that is the intended meaning of disconnect for the provider.
- `Cleanup` calls `Disconnect` when needed, then removes local per-instance storage. Resolve and validate the exact path before any deletion.

### Step F — Register the provider and expose configuration

In `App.startup` (`app.go`), register the provider before `LoadProviderConfigs`:

```go
a.providerManager.RegisterProvider("acmechat", core.ProviderInfo{
    ID:          "acmechat",
    Name:        "Acme Chat",
    Description: "Acme messaging through the official API",
    ConfigSchema: map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "api_token": map[string]interface{}{
                "type":        "string",
                "title":       "API token",
                "description": "Token created in Acme Developer Settings.",
            },
            "sync_days": map[string]interface{}{
                "type":        "number",
                "title":       "Days to synchronize",
                "description": "0 means unlimited.",
                "default":     30,
                "minimum":     0,
            },
        },
        "required": []string{"api_token"},
    },
}, func() core.Provider {
    return providers.NewAcmeChatProvider()
})
```

Keep `ID` and the first argument to `RegisterProvider` identical. Confirm they do not collide with an existing identifier, and that package/export naming matches.

If authentication needs specialized UX (QR, OAuth button, browser, device approval), the generic form may not be sufficient. `ProviderConfigForm.tsx` currently has dedicated connection flows only for WhatsApp and Slack; QR rendering is only shown for WhatsApp. Add a provider-specific frontend flow, or generalize this component, when the new provider needs it. The backend already exposes `GetProviderQRCode`, but it returns a QR only when `SupportsQRCodeAuth` is true.

## 4. Acceptance checklist

### Static checks

- [ ] `var _ core.Provider = (*XxxProvider)(nil)` compiles.
- [ ] `go fmt` has been applied to modified Go files.
- [ ] `go build ./...` succeeds.
- [ ] Added dependencies are justified, pinned through `go.mod`, and license-compatible.
- [ ] No token, cookie, QR code, message content, or authentication header is fully logged.
- [ ] Errors add useful context without exposing secrets.

### Recommended unit tests

- [ ] Conversion of every message type, including a message without text.
- [ ] Timestamp, `IsFromMe`, and canonical conversation/IDs.
- [ ] Thread, quoted reply, reaction, deletion, and edit when supported.
- [ ] Attachment serialization and deserialization.
- [ ] `before` pagination, `since` increment, and final ascending ordering.
- [ ] Deduplication when the same message arrives through sync and real time.
- [ ] `SetConfig`, missing/expired authentication, repeated `Connect` / `Disconnect`.
- [ ] All goroutines stop and no send occurs on a closed channel.

### Manual multi-instance test

1. Create two instances of the new provider with two separate accounts or remote environments.
2. Restart Loom: each must restore only its own session and cache.
3. Connect both, open their conversations, and send/receive a message on each.
4. Verify real-time events, then force a network reconnection.
5. Run synchronization twice and verify no duplicates and correct message ordering.
6. Test every declared capability and confirm undeclared capabilities are not offered by the UI.
7. Remove one instance: the other must keep working; only the removed instance's session files and data must disappear.
8. Inspect logs and local database: there must be no secret exposure or cross-instance data mix.

## 5. Frequent pitfalls

- Forgetting `ProviderInstanceID` on a `LinkedAccount`: data from two accounts can be mixed.
- Using a raw ID in history and a normalized ID in WebSocket events: events no longer find existing messages.
- Emitting messages without persisting them: they appear, then disappear after refresh.
- Starting multiple polling goroutines on every `Connect`: every message arrives multiple times.
- Declaring a capability because an API exists, without handling permissions, errors, and corresponding UI.
- Treating a present token as valid authentication, then failing silently on the first request.
- Blocking the event channel during a long sync or sending one event per message when `MessageBatchEvent` is appropriate.
- Omitting the final sync status: the frontend can remain in a loading state.
- Putting secrets in configuration without assessing its JSON persistence and the user's backups.
- Deleting a generic path in `Cleanup` rather than the directory derived from `_instance_id`.

## 6. Code references to read before opening the PR

| Subject | Reference |
| --- | --- |
| Interface, capabilities, configuration, events | `pkg/core/provider.go`, `pkg/core/config.go`, `pkg/core/events.go` |
| Instances, save/restore/removal | `pkg/core/provider_manager.go` |
| Persisted types | `pkg/models/models.go` |
| Registration, event listener, synchronization | `app.go` (`startup`, `startEventListenerForProvider`) |
| QR/local session | `pkg/providers/whatsapp/provider.go` |
| Real time, storage, incremental sync | `pkg/providers/slack/events.go`, `pkg/providers/slack/messages.go` |
| OAuth, cancellation, polling | `pkg/providers/googlechat/provider.go` |
| Configuration schema and UX | `frontend/src/components/ProviderConfigForm.tsx` |

Before declaring the provider complete, reread this guide with the implementation open and answer every question in section 1. Any uncovered choice must either be implemented or explicitly rejected with a `false` capability and a clear error.
