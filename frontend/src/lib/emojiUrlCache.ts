// Emoji URL cache for Slack custom emojis
// Maps providerInstanceId -> emojiName -> url

const emojiUrlCache = new Map<string, Map<string, string | null>>();

export function getCachedEmojiUrl(providerInstanceId: string, emojiName: string): string | null | undefined {
  const providerCache = emojiUrlCache.get(providerInstanceId);
  if (!providerCache) {
    return undefined; // Not in cache
  }
  return providerCache.get(emojiName); // Could be string, null, or undefined
}

export function setCachedEmojiUrl(providerInstanceId: string, emojiName: string, url: string | null): void {
  let providerCache = emojiUrlCache.get(providerInstanceId);
  if (!providerCache) {
    providerCache = new Map<string, string | null>();
    emojiUrlCache.set(providerInstanceId, providerCache);
  }
  providerCache.set(emojiName, url);
}

export function clearEmojiCache(providerInstanceId?: string): void {
  if (providerInstanceId) {
    emojiUrlCache.delete(providerInstanceId);
  } else {
    emojiUrlCache.clear();
  }
}
