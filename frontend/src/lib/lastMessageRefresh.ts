type TimedMessage = { timestamp?: unknown };

// Preserve live updates that arrived while the database summary was loading.
// Unchanged cache entries are replaced normally, including deletions.
export function mergeInFlightLastMessages<T extends TimedMessage>(
  before: Record<string, T | null> | undefined,
  current: Record<string, T | null> | undefined,
  loaded: Record<string, T | null>,
): Record<string, T | null> {
  const result = { ...loaded };
  for (const [id, message] of Object.entries(current ?? {})) {
    if (!message || message === before?.[id]) continue;
    const fetched = result[id];
    const liveTime = new Date(String(message.timestamp ?? "")).getTime();
    const fetchedTime = fetched ? new Date(String(fetched.timestamp ?? "")).getTime() : -Infinity;
    if (!fetched || (Number.isFinite(liveTime) && liveTime >= fetchedTime)) result[id] = message;
  }
  return result;
}
