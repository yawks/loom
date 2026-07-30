import { getCachedEmojiUrl, setCachedEmojiUrl } from "../lib/emojiUrlCache";
import { useEffect, useState } from "react";

import { GetCustomEmojis } from "../../wailsjs/go/main/App";
import { cleanEmoji } from "@/lib/userDisplayNames";
import { emojiNameToUnicode } from "../lib/emojiMap";

interface EmojiProps {
  emoji: string; // Emoji string (e.g., ":calendar:", "📅", or "calendar")
  providerInstanceId?: string; // Provider instance ID
  className?: string;
  size?: number; // Size in pixels (default: 16)
  fallback?: string; // Fallback text/emoji if image fails to load
}

/**
 * Generic component to display emojis (both Unicode and custom/provider-specific)
 */
export function Emoji({
  emoji,
  providerInstanceId,
  className = "",
  size = 16,
  fallback,
}: EmojiProps) {
  const [emojiUrl, setEmojiUrl] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);

  const cleanedEmoji = cleanEmoji(emoji);
  const emojiName = cleanedEmoji.replace(/^:|:$/g, "");
  const mappedUnicodeEmoji = emojiNameToUnicode(emojiName);
  // Reaction grouping can wrap a real Unicode glyph in colons, for example
  // ":🐐:". Such values must not trigger a custom-emoji provider lookup.
  const containsUnicodeGlyph = /[\p{Extended_Pictographic}\p{Regional_Indicator}]/u.test(emojiName);
  const unicodeDisplay = mappedUnicodeEmoji || (containsUnicodeGlyph ? emojiName : null);
  const isUnicodeEmoji = !emoji.startsWith(":") || unicodeDisplay !== null;

  useEffect(() => {
    if (isUnicodeEmoji) {
      setLoading(false);
      return;
    }

    // Skip skin-tone modifiers
    if (/^skin-tone-[2-6]$/.test(emojiName)) {
      setLoading(false);
      setError(true);
      return;
    }

    // Check if this emoji exists in our Unicode mapping
    const unicodeEmoji = mappedUnicodeEmoji;
    
    if (unicodeEmoji) {
      setEmojiUrl(null);
      setError(true); // Set error to trigger Unicode fallback rendering
      setLoading(false);
      return;
    }

    // Not in Unicode map, might be a custom provider emoji
    if (!providerInstanceId) {
      setLoading(false);
      setError(true);
      return;
    }

    // Check cache first
    const cachedUrl = getCachedEmojiUrl(providerInstanceId, emojiName);
    if (cachedUrl !== undefined) {
      if (cachedUrl) {
        setEmojiUrl(cachedUrl);
      } else {
        setError(true);
      }
      setLoading(false);
      return;
    }

    // Fetch custom emoji map from backend
    GetCustomEmojis(providerInstanceId)
      .then((emojiMap: Record<string, string>) => {
        const url = emojiMap?.[emojiName];
        setCachedEmojiUrl(providerInstanceId, emojiName, url || null);

        if (url && url.trim() !== "") {
          setEmojiUrl(url);
        } else {
          setError(true);
        }
        setLoading(false);
      })
      .catch((err: unknown) => {
        console.error(`[Emoji] Failed to get emoji URL for ${emojiName}:`, err);
        setCachedEmojiUrl(providerInstanceId, emojiName, null);
        setError(true);
        setLoading(false);
      });
  }, [emojiName, isUnicodeEmoji, mappedUnicodeEmoji, providerInstanceId]);

  if (isUnicodeEmoji) {
    return (
      <span className={className} style={{ fontSize: `${size}px` }}>
        {unicodeDisplay || emoji}
      </span>
    );
  }

  if (loading) {
    const cleanedEmoji = cleanEmoji(emoji);
    const emojiName = cleanedEmoji.replace(/^:|:$/g, "");
    const unicodeEmoji = emojiNameToUnicode(emojiName);
    
    if (unicodeEmoji) {
      return (
        <span className={`${className} inline-block`} style={{ fontSize: `${size}px`, lineHeight: 1 }}>
          {unicodeEmoji}
        </span>
      );
    }
    
    return (
      <span
        className={`${className} inline-block align-baseline`}
        style={{
          width: `${size}px`,
          height: `${size}px`,
          fontSize: `${size * 0.7}px`,
        }}
        title={emoji}
      >
        {emojiName}
      </span>
    );
  }

  if (error || !emojiUrl) {
    const cleanedEmoji = cleanEmoji(emoji);
    const emojiName = cleanedEmoji.replace(/^:|:$/g, "");
    const unicodeEmoji = emojiNameToUnicode(emojiName);
    
    if (unicodeEmoji) {
      return (
        <span className={`${className} inline-block`} style={{ fontSize: `${size}px`, lineHeight: 1 }}>
          {unicodeEmoji}
        </span>
      );
    }

    const displayText = fallback || emojiName;
    return (
      <span
        className={`${className} inline-block align-baseline`}
        style={{
          fontSize: `${size * 0.7}px`,
        }}
        title={emoji}
      >
        {displayText}
      </span>
    );
  }

  return (
    <img
      src={emojiUrl}
      alt={emoji}
      className={`${className} inline-block align-baseline`}
      style={{
        width: `${size}px`,
        height: `${size}px`,
        objectFit: "contain",
      }}
      onError={() => {
        setError(true);
      }}
      title={emoji}
    />
  );
}
