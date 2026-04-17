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

  // First, try to get from participantNames map
  if (options?.participantNames) {
    const name = options.participantNames.get(userId);
    if (name && name.trim().length > 0) {
      return name;
    }
  }

  // If not found, try to find in messages (for @userIds)
  if (options?.allMessages) {
    for (const message of options.allMessages) {
      // Check if this message is from the user we're looking for
      if (message.senderId === userId && message.senderName && message.senderName.trim().length > 0) {
        return message.senderName;
      }
    }
  }

  // Fallback: format the ID in a readable way
  // For User IDs (U1234567890), just return the ID as-is
  // For other formats, try to format them
  if (userId.startsWith("U") && /^U[A-Z0-9]+$/.test(userId)) {
    // @userId - return as-is (will be handled by UI)
    return userId;
  }

  // For WhatsApp IDs, format as phone number
  const whatsappMatch = userId.match(/^(\d+)@s\.whatsapp\.net$/);
  if (whatsappMatch) {
    const phoneNumber = whatsappMatch[1];
    if (phoneNumber.startsWith("33") && phoneNumber.length === 11) {
      const countryCode = phoneNumber.substring(0, 2);
      const rest = phoneNumber.substring(2);
      return `+${countryCode} ${rest.substring(0, 1)} ${rest.substring(1, 3)} ${rest.substring(3, 5)} ${rest.substring(5, 7)} ${rest.substring(7, 9)}`;
    }
    return `+${phoneNumber}`;
  }

  // Generic fallback
  return userId
    .replace(/^user-/, "")
    .replace(/^whatsapp-/, "")
    .replace(/^[a-z]+-/, "")
    .split("-")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}
