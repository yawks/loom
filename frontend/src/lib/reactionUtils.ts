import { emojiNameToUnicode, unicodeToEmojiName } from "./emojiMap.ts";

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
  const mappedName = unicodeToEmojiName(originalUnicode) || unicodeToEmojiName(unicode);
  const canonicalName = mappedName || unicode;
  // Named reaction APIs need the canonical name from the shared emoji map,
  // regardless of whether the picker supplied Unicode or one of its own
  // aliases. Custom emoji have no Unicode mapping and keep their exact name.
  const namedApiEmoji = resolvedUnicode || mappedName || !hasNamedForm ? canonicalName : clean;

  return {
    // Standard emoji always go through the shared name converter. Unknown
    // names are custom emoji and are deliberately preserved verbatim.
    apiEmoji: nativeEmojiReactions ? unicode : namedApiEmoji,
    canonicalName,
    storedEmoji: `:${canonicalName}:`,
  };
}

export function reactionMatches(emoji: string, canonicalName: string): boolean {
  return normalizeReaction(emoji, false).canonicalName === canonicalName;
}
