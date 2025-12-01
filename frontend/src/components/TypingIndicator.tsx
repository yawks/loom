import { useTranslation } from "react-i18next";
import { useTypingStore } from "@/lib/typingStore";
import { useMemo } from "react";

interface TypingIndicatorProps {
  conversationId: string;
}

/**
 * Displays who is currently typing in a conversation
 * Shows "user is typing", "user1 and user2 are typing", "user1, user2 and user3 are typing"
 */
export function TypingIndicator({ conversationId }: TypingIndicatorProps) {
  const { t } = useTranslation();
  
  // Get typing users from store - use selector to avoid unnecessary re-renders
  const typingUsersRaw = useTypingStore((state) => state.typingByConversation[conversationId]);
  
  // Memoize the typing users to avoid re-renders when the array reference changes
  const typingUsers = useMemo(() => typingUsersRaw || [], [typingUsersRaw]);

  // Format the typing message
  const typingMessage = useMemo(() => {
    if (typingUsers.length === 0) {
      return null;
    }

    // Get display names for typing users
    const displayNames = typingUsers.map((user) => {
      // Use userName from backend if available (it's already resolved)
      if (user.userName) {
        return user.userName;
      }
      
      // Fallback: Extract phone number from WhatsApp JID if possible
      const match = user.userId.match(/^(\d+)@/);
      if (match) {
        const phoneNumber = match[1];
        // Format French numbers nicely
        if (phoneNumber.startsWith("33") && phoneNumber.length === 11) {
          const rest = phoneNumber.substring(2);
          return `+33 ${rest.substring(0, 1)} ${rest.substring(1, 3)} ${rest.substring(3, 5)} ${rest.substring(5, 7)} ${rest.substring(7, 9)}`;
        }
        return `+${phoneNumber}`;
      }
      
      // Last resort: use userId as-is
      return user.userId;
    });

    if (displayNames.length === 1) {
      return t("typing_single", { user: displayNames[0] });
    } else if (displayNames.length === 2) {
      return t("typing_double", { user1: displayNames[0], user2: displayNames[1] });
    } else {
      // 3 or more users
      const firstUsers = displayNames.slice(0, -1).join(", ");
      const lastUser = displayNames[displayNames.length - 1];
      return t("typing_multiple", { users: firstUsers, lastUser });
    }
  }, [typingUsers, t]);

  if (!typingMessage) {
    return null;
  }

  return (
    <div className="px-4 py-2 border-t">
      <div className="flex items-center gap-2">
        <div className="flex gap-1">
          <span className="h-2 w-2 rounded-full bg-primary animate-bounce [animation-delay:-0.3s]"></span>
          <span className="h-2 w-2 rounded-full bg-primary animate-bounce [animation-delay:-0.15s]"></span>
          <span className="h-2 w-2 rounded-full bg-primary animate-bounce"></span>
        </div>
        <span className="text-sm text-muted-foreground">{typingMessage}</span>
      </div>
    </div>
  );
}

