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
