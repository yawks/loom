import i18n from "@/i18n";

/**
 * Tries to translate a message from the Go backend.
 * If the message is already a translation key, it will be translated.
 * Otherwise, it will try to find a matching translation key based on common patterns.
 * 
 * @param message - The message from the Go backend
 * @returns The translated message or the original message if no translation is found
 */
export function translateBackendMessage(message: string): string {
  if (!message || typeof message !== "string") {
    return message;
  }

  // Trim the message
  const trimmedMessage = message.trim();
  if (!trimmedMessage) {
    return message;
  }

  // First, try to use the message as a direct translation key
  const directTranslation = i18n.t(trimmedMessage, { defaultValue: null });
  if (directTranslation && directTranslation !== trimmedMessage) {
    return directTranslation;
  }

  // Try common patterns for backend messages
  // Map common backend messages to translation keys
  const messageMap: Record<string, string> = {
    "Synchronizing...": "synchronizing",
    "Fetching contacts...": "fetching_contacts",
    "Fetching message history...": "fetching_history",
    "Fetching avatars...": "fetching_avatars",
    "Sync completed": "sync_completed",
    "Sync failed": "sync_failed",
    "Error": "error",
    "Online": "online",
    "Offline": "offline",
  };

  // Check if the message matches a known pattern
  const normalizedMessage = trimmedMessage;
  if (messageMap[normalizedMessage]) {
    const translation = i18n.t(messageMap[normalizedMessage], { defaultValue: null });
    if (translation && translation !== messageMap[normalizedMessage]) {
      return translation;
    }
  }

  // Try case-insensitive matching
  const lowerMessage = normalizedMessage.toLowerCase();
  for (const [key, translationKey] of Object.entries(messageMap)) {
    if (key.toLowerCase() === lowerMessage) {
      const translation = i18n.t(translationKey, { defaultValue: null });
      if (translation && translation !== translationKey) {
        return translation;
      }
    }
  }

  // If no translation found, return the original message
  // This will trigger the missing key handler if used with i18n.t()
  return trimmedMessage;
}

/**
 * Translates a message with fallback to backend translation
 * 
 * @param key - The translation key
 * @param fallback - Fallback message (usually from backend)
 * @returns The translated message
 */
export function translateWithFallback(key: string, fallback?: string): string {
  const translation = i18n.t(key, { defaultValue: fallback || key });
  
  // If translation is the same as key and we have a fallback, try to translate the fallback
  if (translation === key && fallback) {
    return translateBackendMessage(fallback);
  }
  
  return translation;
}







