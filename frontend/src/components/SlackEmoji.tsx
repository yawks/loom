import { useEffect, useState } from "react";

import { GetSlackEmojiURL } from "../../wailsjs/go/main/App";
import { cleanSlackEmoji } from "../lib/userDisplayNames";
import { unicodeEmojiMap } from "../lib/emojiMap";

interface SlackEmojiProps {
  emoji: string; // Emoji string from Slack (e.g., ":calendar:", "📅", or "calendar")
  providerInstanceId?: string; // Provider instance ID (e.g., "slack-1")
  className?: string;
  size?: number; // Size in pixels (default: 16)
  fallback?: string; // Fallback text/emoji if image fails to load
}

/**
 * Component to display Slack emojis (both Unicode and custom)
 * For custom emojis, it fetches the image URL from Slack API
 * For Unicode emojis, it displays them directly
 */
export function SlackEmoji({
  emoji,
  providerInstanceId,
  className = "",
  size = 16,
  fallback,
}: SlackEmojiProps) {
  const [emojiUrl, setEmojiUrl] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);

  // Clean emoji to remove skin-tone modifiers before processing
  const cleanedEmoji = cleanSlackEmoji(emoji);

  // Check if it's a Unicode emoji (doesn't start with : or is a standard Unicode emoji)
  // Standard Unicode emojis in Slack are wrapped in colons but don't have custom images
  const isUnicodeEmoji = !cleanedEmoji.startsWith(":") || cleanedEmoji.length <= 2;

  useEffect(() => {
    if (isUnicodeEmoji) {
      // Unicode emoji - no need to fetch URL
      setLoading(false);
      return;
    }

    // Extract emoji name (remove colons)
    const emojiName = cleanedEmoji.replace(/^:|:$/g, "");
    
    // Skip skin-tone modifiers (skin-tone-2 through skin-tone-6)
    if (/^skin-tone-[2-6]$/.test(emojiName)) {
      setLoading(false);
      setError(true);
      return;
    }

    // FIRST, check if this emoji exists in our Unicode mapping
    // This allows us to display standard emojis immediately without backend call
    const unicodeEmoji = unicodeEmojiMap[emojiName];
    
    if (unicodeEmoji) {
      // Found in Unicode map - display it immediately
      setEmojiUrl(null);
      setError(true); // Set error to trigger Unicode fallback rendering
      setLoading(false);
      return;
    }

    // Not in Unicode map, so it might be a custom Slack emoji
    if (!providerInstanceId) {
      // No provider instance ID - can't fetch custom emoji
      setLoading(false);
      setError(true);
      return;
    }

    // Fetch emoji URL from backend
    GetSlackEmojiURL(providerInstanceId, emojiName)
      .then((url: string) => {
        if (url && url.trim() !== "") {
          setEmojiUrl(url);
        } else {
          // Empty URL means emoji not found in custom emoji cache
          setError(true);
        }
        setLoading(false);
      })
      .catch((err: unknown) => {
        console.error(`[SlackEmoji] Failed to get emoji URL for ${emojiName}:`, err);
        setError(true);
        setLoading(false);
      });
  }, [cleanedEmoji, providerInstanceId, isUnicodeEmoji]);

  // Unicode emoji - display directly
  if (isUnicodeEmoji) {
    return (
      <span className={className} style={{ fontSize: `${size}px` }}>
        {cleanedEmoji}
      </span>
    );
  }

  // Custom emoji - display image if available
  if (loading) {
    // While loading, check if it's a Unicode emoji we know about
    const emojiName = cleanedEmoji.replace(/^:|:$/g, "");
    const unicodeEmoji = unicodeEmojiMap[emojiName];
    
    if (unicodeEmoji) {
      // Found Unicode emoji, display it immediately instead of showing loading state
      return (
        <span className={`${className} inline-block`} style={{ fontSize: `${size}px`, lineHeight: 1 }}>
          {unicodeEmoji}
        </span>
      );
    }
    
    // Show placeholder while loading (only for custom emojis)
    return (
      <span
        className={`${className} inline-block align-baseline`}
        style={{
          width: `${size}px`,
          height: `${size}px`,
          fontSize: `${size * 0.7}px`,
        }}
        title={cleanedEmoji}
      >
        {emojiName}
      </span>
    );
  }

  if (error || !emojiUrl) {
    // If no URL found, it might be a Unicode emoji wrapped in colons
    // Try to convert Slack emoji names to Unicode using the comprehensive mapping
    const emojiName = cleanedEmoji.replace(/^:|:$/g, "");
    
    // Try to find Unicode emoji in map (from emojilib)
    const unicodeEmoji = unicodeEmojiMap[emojiName];
    
    if (unicodeEmoji) {
      // Found Unicode emoji, display it directly
      return (
        <span className={`${className} inline-block`} style={{ fontSize: `${size}px`, lineHeight: 1 }}>
          {unicodeEmoji}
        </span>
      );
    }

    // Show fallback or emoji name
    const displayText = fallback || emojiName;
    return (
      <span
        className={`${className} inline-block align-baseline`}
        style={{
          fontSize: `${size * 0.7}px`,
        }}
        title={cleanedEmoji}
      >
        {displayText}
      </span>
    );
  }

  // Display emoji image
  return (
    <img
      src={emojiUrl}
      alt={cleanedEmoji}
      className={`${className} inline-block align-baseline`}
      style={{
        width: `${size}px`,
        height: `${size}px`,
        objectFit: "contain",
      }}
      onError={() => {
        setError(true);
      }}
      title={cleanedEmoji}
    />
  );
}
