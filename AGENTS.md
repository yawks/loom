# Loom agent instructions

## Golden rule: provider-neutral frontend

Except for provider configuration and bundled brand assets/icons, code under
`frontend/src` must not know about individual providers.

- Do not branch on provider IDs or provider-specific conversation/message ID
  prefixes in frontend business logic.
- Do not parse or normalize provider wire formats, remote IDs, receipts,
  reactions, quotes, or attachments in React/TypeScript. Normalize them in the
  provider/backend and expose a canonical model.
- Drive frontend behavior through provider-neutral capabilities and data
  contracts. Add a capability or canonical field when providers differ.
- Compatibility handling for already-persisted data must use a generic format
  detector, be documented as legacy behavior, and include regression coverage.
- Provider configuration screens and local brand icon mappings are explicit
  exceptions, but provider-specific behavior must not leak from them into the
  rest of the frontend.

When touching frontend code, search for provider names and explain any remaining
occurrence outside those exceptions.

## SQLite transactions

- All provider and backend transactions must use `db.Transaction(database, fn)`
  instead of calling `database.Transaction(fn)` or `db.DB.Transaction(fn)`
  directly.
- The wrapper retries the complete transaction on `SQLITE_BUSY` and
  `SQLITE_BUSY_SNAPSHOT`, which commonly occur during concurrent synchronization
  and after system wake.
- Initialize values mutated by GORM inside the transaction callback, especially
  models passed to `Create`, so every retry starts without IDs or other state
  left by a rolled-back attempt.
- Do not add provider-specific SQLite retry loops.

## Synchronization hot paths

- Do not start one goroutine or one database transaction per message, contact,
  group, attachment, receipt, or reaction during bulk synchronization. Use a
  bounded worker pool for network work and batch database work.
- Avoid check-then-write loops (`SELECT` followed by `CREATE`/`SAVE`) for sync
  batches. Prefer loading existing keys once and using transaction-wrapped batch
  upserts.
- Sync status labels must describe the work actually blocking completion. In
  particular, do not report contact synchronization while message history is
  still being converted or persisted.
- Network enrichment that is not required for correctness (avatars, group
  discovery, presence, metadata) must have a timeout and must not indefinitely
  block a provider's terminal `completed` or `error` sync status.
- Treat provider sync events as potentially out of order. A late history or app
  state event after the provider's nominal completion must either rearm final
  reconciliation or be prevented from regressing the visible status forever.
- Keep per-item success/miss logging behind verbose logging. Default logs should
  summarize a batch and retain individual errors only.
