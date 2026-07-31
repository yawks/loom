import { models } from "../../wailsjs/go/models";

// Module-level store of messages received via event but not yet confirmed in the DB.
// The queryFn in useMessageData merges these into every refetch result until the DB
// returns them on its own, eliminating the race where a background refetch overwrites
// an optimistic setQueryData before the message has been committed.
const pending = new Map<string, models.Message[]>();
const MAX_PENDING_PER_CONVERSATION = 50;
const MAX_PENDING_MESSAGES = 500;

function boundPendingMessages(): void {
  let total = 0;
  for (const messages of pending.values()) total += messages.length;
  while (total > MAX_PENDING_MESSAGES) {
    const oldest = pending.entries().next().value as [string, models.Message[]] | undefined;
    if (!oldest) break;
    pending.delete(oldest[0]);
    total -= oldest[1].length;
  }
}

export function addPendingMessage(convId: string, message: models.Message): void {
  const existing = pending.get(convId) ?? [];
  if (!existing.some((m) => m.protocolMsgId && m.protocolMsgId === message.protocolMsgId)) {
    // This is only a short bridge while SQLite commits the event. Do not let a
    // resume sync turn it into a second, unbounded message store.
    pending.delete(convId);
    pending.set(convId, [...existing, message].slice(-MAX_PENDING_PER_CONVERSATION));
    boundPendingMessages();
  }
}

// Merges pending messages into dbMessages (first-page fetch only).
// Messages confirmed by the DB result are removed from pending automatically.
export function mergePendingMessages(
  convId: string,
  dbMessages: models.Message[]
): models.Message[] {
  const msgs = pending.get(convId);
  if (!msgs || msgs.length === 0) return dbMessages;

  const dbIds = new Set(dbMessages.map((m) => m.protocolMsgId).filter(Boolean));

  // Keep in pending only those that the DB hasn't confirmed yet
  const stillPending = msgs.filter((m) => m.protocolMsgId && !dbIds.has(m.protocolMsgId));
  if (stillPending.length === 0) {
    pending.delete(convId);
  } else if (stillPending.length !== msgs.length) {
    pending.set(convId, stillPending);
  }

  return stillPending.length > 0 ? [...dbMessages, ...stillPending] : dbMessages;
}
