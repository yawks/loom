import type { models } from "../../wailsjs/go/models";

/**
 * Extracts status emoji from a LinkedAccount's Extra field
 * @param linkedAccount The linked account to extract emoji from
 * @returns The status emoji string (e.g., ":calendar:", "📅") or null if not found
 */
export function getStatusEmoji(linkedAccount: models.LinkedAccount | undefined): string | null {
  if (!linkedAccount?.extra) {
    return null;
  }

  try {
    const extra = JSON.parse(linkedAccount.extra);
    const emoji = extra.statusEmoji || null;
    return emoji;
  } catch (e) {
    // If parsing fails, return null
    return null;
  }
}

/**
 * Gets the provider instance ID from a LinkedAccount
 * @param linkedAccount The linked account
 * @returns The provider instance ID or null
 */
export function getProviderInstanceId(linkedAccount: models.LinkedAccount | undefined): string | null {
  return linkedAccount?.providerInstanceId || null;
}

/**
 * Gets status emoji and provider instance ID from a MetaContact's linked accounts
 * Checks all linked accounts and returns the first status emoji found
 * @param contact The meta contact to extract emoji from
 * @returns Object with emoji string and providerInstanceId, or null if not found
 */
export function getContactStatusEmoji(
  contact: models.MetaContact
): { emoji: string; providerInstanceId: string } | null {
  if (!contact.linkedAccounts || contact.linkedAccounts.length === 0) {
    return null;
  }

  for (const account of contact.linkedAccounts) {
    const emoji = getStatusEmoji(account);
    if (emoji) {
      const providerInstanceId = getProviderInstanceId(account);
      if (providerInstanceId) {
        return { emoji, providerInstanceId };
      }
      // If emoji exists but no provider instance ID, still return the emoji
      return { emoji, providerInstanceId: "" };
    }
  }
  return null;
}
