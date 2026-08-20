/**
 * Utility functions for handling user display names and provider-specific text formatting
 */

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

  // Extract raw ID if namespaced (e.g. "whatsapp-1::1234@s.whatsapp.net" -> "1234@s.whatsapp.net")
  const rawId = userId.includes("::") ? userId.split("::")[1] : userId;
  // Strip device suffixes if present (e.g. "1234:5@s.whatsapp.net" -> "1234@s.whatsapp.net")
  const normalizedId = userId.replace(/:\d+@/, "@");
  const rawNormalizedId = rawId.replace(/:\d+@/, "@");

  // 1. First, try to get from participantNames map
  if (options?.participantNames) {
    const candidateKeys = [userId, rawId, normalizedId, rawNormalizedId];
    for (const key of candidateKeys) {
      const name = options.participantNames.get(key);
      if (name && name.trim().length > 0) {
        return name;
      }
    }
  }

  // 2. If not found, try to find in messages (senderName)
  if (options?.allMessages) {
    const candidateKeys = new Set([userId, rawId, normalizedId, rawNormalizedId]);
    for (const message of options.allMessages) {
      if (
        message.senderName &&
        message.senderName.trim().length > 0 &&
        (candidateKeys.has(message.senderId) ||
          candidateKeys.has(message.senderId?.replace(/:\d+@/, "@")) ||
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

  // 4. For WhatsApp phone number IDs, format as phone number
  const whatsappMatch = rawNormalizedId.match(/^(\d+)@(?:s\.whatsapp\.net|c\.us)$/);
  if (whatsappMatch) {
    const phoneNumber = whatsappMatch[1];
    if (phoneNumber.startsWith("33") && phoneNumber.length === 11) {
      const countryCode = phoneNumber.substring(0, 2);
      const rest = phoneNumber.substring(2);
      return `+${countryCode} ${rest.substring(0, 1)} ${rest.substring(1, 3)} ${rest.substring(3, 5)} ${rest.substring(5, 7)} ${rest.substring(7, 9)}`;
    }
    return `+${phoneNumber}`;
  }

  // 5. If it's a raw WhatsApp LID that wasn't resolved yet
  if (rawId.endsWith("@lid")) {
    return rawId.replace(/@lid$/, "");
  }

  // 6. Generic fallback
  return rawId
    .replace(/^user-/, "")
    .replace(/^whatsapp-/, "")
    .replace(/^[a-z]+-/, "")
    .split("-")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}
