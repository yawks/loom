/** Utility functions for provider-neutral user display names. */

import type { models } from "../../wailsjs/go/models";

/**
 * Removes skin-tone modifiers from emoji text
 * Handles cases like ":+1::skin-tone-2:" -> ":+1:" or "+1::skin-tone-2" -> "+1"
 * This is used to clean emoji text before displaying it
 */
export function cleanEmoji(text: string): string {
  if (!text) return text;
  
  // Remove skin-tone modifiers from the text
  // Pattern matches ":skin-tone-X:" where X is typically 2-6
  // Also handles cases without leading colon like "+1::skin-tone-2"
  let cleaned = text.replace(/:skin-tone-[2-6]:/g, '');
  
  // Also handle cases where skin-tone is attached without leading colon
  // e.g., "+1::skin-tone-2" -> "+1"
  cleaned = cleaned.replace(/::skin-tone-[2-6]/g, '');
  
  return cleaned;
}

/**
 * Gets display name for a user ID from various sources
 * @param userId The user ID (e.g., @userId like "U1234567890")
 * @param options Options object with participantNames map and/or allMessages array
 * @returns Display name for the user, or a fallback if not found
 */
export function getUserDisplayName(
  userId: string,
  options?: {
    participantNames?: Map<string, string>;
    allMessages?: models.Message[];
  }
): string {
  if (!userId) return userId;

  // Instance-qualified IDs use Loom's own `instance::remote-id` envelope.
  const rawId = userId.includes("::") ? userId.split("::")[1] : userId;

  // 1. First, try to get from participantNames map
  if (options?.participantNames) {
    const candidateKeys = [userId, rawId];
    for (const key of candidateKeys) {
      const name = options.participantNames.get(key);
      if (name && name.trim().length > 0) {
        return name;
      }
    }
  }

  // 2. If not found, try to find in messages (senderName)
  if (options?.allMessages) {
    const candidateKeys = new Set([userId, rawId]);
    for (const message of options.allMessages) {
      if (
        message.senderName &&
        message.senderName.trim().length > 0 &&
        (candidateKeys.has(message.senderId) ||
          (message.senderId?.includes("::") && candidateKeys.has(message.senderId.split("::")[1])))
      ) {
        return message.senderName;
      }
    }
  }

  // 3. Fallback: format the ID in a readable way
  // For User IDs (U1234567890), just return the ID as-is
  if (rawId.startsWith("U") && /^U[A-Z0-9]+$/.test(rawId)) {
    return rawId;
  }

  // Generic fallback. Providers are responsible for supplying a human-readable
  // name when their remote identifier is not suitable for display.
  return rawId
    .replace(/^user-/, "")
    .replace(/^[a-z]+-/, "")
    .split("-")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}
