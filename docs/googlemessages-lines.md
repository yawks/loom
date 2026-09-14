# Google Messages: local lines

## Live diagnosis, 2026-09-14

Checked the pinned libgm revision `b8c77b3deec1`, the local database in read-only
mode, and the phone relay with the existing pairing. No messages were sent and
no pairing/session or production database files were written by the diagnostic.

- The database contained 424 incoming and 302 outgoing messages. The outgoing
  messages used two distinct sender IDs. No SIM or raw-message metadata had been
  persisted, so the receiving line cannot be reconstructed from that database.
- The phone supplied `Settings.SIMCards` with two entries.
- A bounded sample from 12 recent conversations returned 60 incoming and 39
  outgoing messages. All had `senderParticipant.simPayload`.
- All 39 outgoing payloads had a SIM number matching a local line. None of the
  60 incoming payloads matched a local line, even comparing `SIMNumber` alone.
  The message's opaque `someInt` field also did not match the local SIM numbers.
- The decoded `Message` schema has no explicit local recipient/SIM field.
  These observations do not prove the information is absent from every raw
  protocol message; they rule out using the currently decoded sender payload as
  the receiving SIM. Incoming messages therefore remain explicitly unknown.

To reproduce the bounded, read-only probe (it opens a temporary relay connection):

```sh
LOOM_GMESSAGES_LINE_DIAGNOSTIC=1 go test ./pkg/providers/googlemessages \
  -run '^TestLiveLineDiagnostic$' -count=1 -v -timeout 55s
```

The test is skipped by default. Logs contain aggregate counts, never message
bodies, phone numbers, participant IDs, credentials, or raw protobuf data.

## Implemented contract

One provider instance owns multiple `CommunicationIdentity` records. The frontend
receives opaque IDs, labels and addresses through `GetConversationIdentities`.
`SupportsIdentitySelection` controls the selector. All protocol translation stays
in the provider; physical SIM versus eSIM does not affect the frontend contract.

`Conversation.OutgoingIdentityID` stores an explicit Loom preference. Empty means
follow the phone's current conversation default. This does not change the default
on the phone. The provider snapshots the selected card before uploading media and
sets both `MessagePayload.ParticipantID` and `SendMessageRequest.SIMPayload` from
that card. Text, quoted replies and attachments share this path. If an explicit
selection is no longer available, sending fails instead of selecting another SIM.
Reactions retain the existing conversation routing; changing their routing is
outside this sending-line selector.

Settings replace the in-memory inventory; absent SIM data is a partial update.
Conversation metadata can seed the inventory before settings arrive. It cannot
revive a line removed by an authoritative settings inventory. Reconnection clears
the inventory, while the explicit preference remains in SQLite.

Messages carry canonical local identity metadata. Outgoing messages can be mapped
from their own sender ID; incoming messages are marked as applicable but unknown.
A transaction batches historical outgoing mappings when settings arrive, scoped
to the emitting provider instance. Existing identity snapshots are preserved by
that reconciliation. No historical attribution uses today's conversation default.

The selector and status-popover identity details are available in French and
English, and in both bubble and IRC layouts. Identity details appear on hover,
keyboard focus or activation of the outgoing delivery checks, not on every row.
Incoming messages have no delivery checks and no repeated unknown-line label. Configuration and icon mappings remain the only frontend
code that knows individual providers. The full frontend provider-name search also
finds ordinary English uses of “signal” in syntax highlighting/read-state comments
and the emoji dictionary; those are unrelated to the Signal provider.

## Validation and remaining limit

Tests cover persisted selection after recreating the provider, returning to the
phone default, removal of a selected line, coherent participant/payload selection,
provider isolation, preservation of historical snapshots, and refusal to treat an
incoming sender payload as the receiving line. Wails bindings are regenerated.

The read-only phone diagnostic validates discovery and message metadata. It does
not validate delivery: SMS, MMS and RCS sends from each line still need a manual
check in the rebuilt application. No dependency upgrade or library fork is used.
Reliable incoming-line attribution needs an additional protocol field or a future
library extension, validated against messages received on each number.


## Follow-up: incoming wire fields and contact collisions

A second diagnostic inspects unknown protobuf fields throughout the message tree,
without logging values. No additional top-level field was present on the 60
incoming messages. `someKindOfGroupID` carries unknown field 3 on all 99 messages;
the [current upstream schema](https://github.com/mautrix/gmessages/blob/main/pkg/libgm/gmproto/conversations.proto)
names it `maybePhoneNumber`. On the sample, it matched the remote sender's number
for 26 incoming messages and never a local SIM number. It is not a reliable local
receiving-line identifier. Small integers matching local IDs in a subset of
message-status records are not sufficient evidence of routing either.

The contact-name bug has a separate, confirmed cause: conversation IDs and
participant IDs use independent numeric sequences. Previously, a sender such as
`2` could match a LinkedAccount for conversation `2`, overwriting the correct name
from the message with an unrelated conversation name and persisting that error in
the participant cache.

The provider now qualifies participant identities as `participant:<remote-id>` on
messages, reactions, participant lists and profiles. Conversation-backed accounts
keep their existing IDs. The backend normalizes legacy message IDs before name
lookup, including realtime messages with no numeric local conversation ID. Provider
ownership is resolved independently for each message instead of sharing a fallback
entry for local ID zero.

On provider initialization, an idempotent transaction migrates stored senders,
quoted senders, reactions, participant receipts and group participants. Aggregate
conversation receipts are retained. It scopes every affected table through the
provider's messages or conversations. Ambiguous old profile entries are not copied
into the qualified participant namespace; those entries may still be legitimate
conversation profiles. Existing message names remain available to repopulate the
correct participant profiles. The migration runs when the updated backend starts.

Profile lookup also distinguishes qualified participants from conversation-backed
accounts. A participant profile can no longer fall back to the DM's linked account
(the previous fallback could give the user's own profile the contact's name).
No protocol-specific parsing or branches were added to the frontend.
