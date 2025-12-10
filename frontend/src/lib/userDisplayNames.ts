import type { models } from "../../wailsjs/go/models";

/**
 * Centralized utility functions for getting user display names.
 * This replaces duplicate logic across multiple components.
 */

/**
 * Cleans Slack emoji strings by removing skin-tone modifiers.
 * Examples: 
 *   ":santa::skin-tone-2:" -> ":santa:"
 *   ":+1::skin-tone-2:" -> ":+1:"
 *   ":thumbsup::skin-tone-3:" -> ":thumbsup:"
 */
export function cleanSlackEmoji(emoji: string): string {
  // Remove skin-tone modifiers (skin-tone-2 through skin-tone-6)
  // Pattern matches :skin-tone-X: anywhere in the string
  return emoji.replace(/:skin-tone-[2-6]:/g, "");
}

/**
 * Gets display name for a user ID.
 * Tries multiple sources in order:
 * 1. participantNames map (exact match or normalized)
 * 2. senderName from messages
 * 3. Formatted phone number (for WhatsApp)
 * 4. Formatted Slack user ID
 * 5. Fallback formatting
 */
export function getUserDisplayName(
  userId: string,
  options?: {
    participantNames?: Map<string, string>;
    allMessages?: models.Message[];
    senderName?: string;
  }
): string {
  const { participantNames, allMessages, senderName } = options || {};

  // First try to get from participantNames with the exact ID
  if (participantNames) {
    const name = participantNames.get(userId);
    if (name && name.trim().length > 0) {
      return name;
    }

    // If not found and ID contains ":", try without the ":digits" part
    // e.g., "33662865152:47@s.whatsapp.net" -> "33662865152@s.whatsapp.net"
    if (userId.includes(":")) {
      const normalizedId = userId.replace(/:\d+@/, "@");
      const normalizedName = participantNames.get(normalizedId);
      if (normalizedName && normalizedName.trim().length > 0) {
        return normalizedName;
      }
    }
  }

  // Try senderName if provided directly
  if (senderName && senderName.trim().length > 0) {
    return senderName;
  }

  // Try to get name from messages (useful for Slack where senderName is available)
  if (allMessages) {
    for (const msg of allMessages) {
      if (msg.senderId === userId && msg.senderName && msg.senderName.trim().length > 0) {
        return msg.senderName;
      }
    }
  }

  // Handle Slack user IDs (format: "U1234567890")
  if (userId.match(/^U[A-Z0-9]+$/)) {
    // Slack user ID - try to extract a readable name
    // For now, return a formatted version
    return `User ${userId.substring(1, 6)}...`;
  }

  // Robust handling: extract local part from various WhatsApp ID formats
  // Supports: "33603018166@s.whatsapp.net", "33662865152:47@s.whatsapp.net" (LID format)
  let phoneNumber: string | null = null;

  // Match "digits" optionally followed by ":digits@server"
  const match = userId.match(/^(\d+)(?::\d+)?@/);
  if (match) {
    phoneNumber = match[1];
  }

  if (phoneNumber) {
    // If this looks like a French number (starts with 33 and 11 digits) format nicely
    if (phoneNumber.startsWith("33") && phoneNumber.length === 11) {
      const countryCode = phoneNumber.substring(0, 2); // "33"
      const rest = phoneNumber.substring(2); // 9 digits
      const formatted = `+${countryCode} ${rest.substring(0, 1)} ${rest.substring(1, 3)} ${rest.substring(3, 5)} ${rest.substring(5, 7)} ${rest.substring(7, 9)}`;
      return formatted;
    }
    // For other numeric local parts, return with a leading + and no odd grouping
    return `+${phoneNumber}`;
  }

  // Fallback for other ID formats: try to return a readable label
  return userId
    .replace(/^user-/, "")
    .replace(/^whatsapp-/, "")
    .replace(/^slack-/, "")
    .split("-")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

/**
 * Gets display name for a message sender.
 * Handles "You" for current user and falls back to getUserDisplayName.
 */
export function getSenderDisplayName(
  senderName: string | undefined,
  senderId: string,
  isFromMe: boolean,
  t: (key: string) => string,
  options?: {
    participantNames?: Map<string, string>;
    allMessages?: models.Message[];
  }
): string {
  if (isFromMe) return t("you") || "You";
  
  return getUserDisplayName(senderId, {
    participantNames: options?.participantNames,
    allMessages: options?.allMessages,
    senderName,
  });
}
