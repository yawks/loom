import { emojiNameToUnicode, unicodeToEmojiName } from "./emojiMap";

export interface NormalizedReaction {
  apiEmoji: string;
  canonicalName: string;
  storedEmoji: string;
}

/**
 * Normalizes the UI representation of a reaction using the format advertised
 * by the active provider. Generic message components never need to know which
 * provider is active.
 */
export function normalizeReaction(
  emoji: string,
  nativeEmojiReactions: boolean,
): NormalizedReaction {
  const clean = emoji.startsWith(":") && emoji.endsWith(":")
    ? emoji.slice(1, -1)
    : emoji;
  const unicode = emojiNameToUnicode(clean) || clean;
  const canonicalName = unicodeToEmojiName(unicode) || clean;

  return {
    apiEmoji: nativeEmojiReactions ? unicode : canonicalName,
    canonicalName,
    storedEmoji: `:${canonicalName}:`,
  };
}

export function reactionMatches(emoji: string, canonicalName: string): boolean {
  return normalizeReaction(emoji, false).canonicalName === canonicalName;
}
