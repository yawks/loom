import { emojiNameToUnicode, unicodeToEmojiName } from "./emojiMap";

export interface NormalizedReaction {
  apiEmoji: string;
  canonicalName: string;
  storedEmoji: string;
}

function stripEmojiVariationSelectors(emoji: string): string {
  return emoji.replace(/[\uFE0E\uFE0F]/g, "");
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
  const hasNamedForm = emoji.startsWith(":") && emoji.endsWith(":");
  const clean = hasNamedForm
    ? emoji.slice(1, -1)
    : emoji;
  const resolvedUnicode = emojiNameToUnicode(clean);
  const originalUnicode = resolvedUnicode || clean;
  const unicode = stripEmojiVariationSelectors(originalUnicode);
  // Look up the intact sequence first. Variation selectors are significant in
  // ZWJ emojis (for example 🙋‍♂️); stripping them before the reverse lookup
  // prevents the provider shortcode from being found and sends invalid Unicode
  // value as the reaction name.
  const canonicalName = unicodeToEmojiName(originalUnicode) || unicodeToEmojiName(unicode) || unicode;
  const namedApiEmoji = hasNamedForm || resolvedUnicode ? clean : canonicalName;

  return {
    // A provider picker may already know the exact API shortcode. Preserve it:
    // converting it to Unicode and back through the generated alias map can
    // turn a canonical "grinning" shortcode into a loose textual alias such as ":d".
    apiEmoji: nativeEmojiReactions ? unicode : namedApiEmoji,
    canonicalName,
    storedEmoji: `:${canonicalName}:`,
  };
}

export function reactionMatches(emoji: string, canonicalName: string): boolean {
  return normalizeReaction(emoji, false).canonicalName === canonicalName;
}
