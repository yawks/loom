import { useEffect, useMemo } from "react";

import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { countUnreadMessages } from "@/lib/unreadBadgeCounts";
import { EventsOn } from "../../wailsjs/runtime/runtime";

// Helper function to call UpdateSystemTrayBadge via Wails runtime
const updateSystemTrayBadge = (count: number): Promise<void> => {
  return new Promise((resolve, reject) => {
    if (typeof window === "undefined") {
      reject(new Error("Window is undefined"));
      return;
    }
    if (!window.go?.main?.App) {
      // Silently fail if Wails runtime is not available (e.g., in browser)
      console.debug("Wails runtime not available, skipping system tray badge update");
      resolve();
      return;
    }

    // Try to call the method even if it's not in TypeScript bindings
    const updateBadgeFn = window.go.main.App.UpdateSystemTrayBadge;
    if (!updateBadgeFn || typeof updateBadgeFn !== "function") {
      // Try direct access
      const directAccess = window.go?.main?.App?.UpdateSystemTrayBadge;
      if (!directAccess || typeof directAccess !== "function") {
        console.debug("UpdateSystemTrayBadge method not available, skipping badge update");
        resolve();
        return;
      }
      directAccess(count)
        .then(() => resolve())
        .catch((error: unknown) => {
          console.error("Failed to update system tray badge:", error);
          reject(error);
        });
      return;
    }

    updateBadgeFn(count)
      .then(() => resolve())
      .catch((error: unknown) => {
        console.error("Failed to update system tray badge:", error);
        reject(error);
      });
  });
};

/**
 * Hook to update the system tray badge with the total unread message count.
 * This hook listens to changes in the message read store and updates the badge accordingly.
 */
export function useSystemTrayBadge() {
  const readStateByConversation = useMessageReadStore(
    (state) => state.readByConversation
  );
  const contacts = useAppStore((state) => state.metaContacts);
  const badgeUntrackedConversationIds = useAppStore(
    (state) => state.badgeUntrackedConversationIds
  );
  const setConversationBadgeTracked = useAppStore(
    (state) => state.setConversationBadgeTracked
  );

  useEffect(() => {
    const unsubscribe = EventsOn("conversation-mute-status", (payload: string) => {
      try {
        const event = JSON.parse(payload) as { conversationId?: string; muted?: boolean };
        if (event.conversationId && typeof event.muted === "boolean") {
          setConversationBadgeTracked(event.conversationId, !event.muted);
        }
      } catch (error) {
        console.error("Failed to parse conversation mute status:", error);
      }
    });
    return () => {
      if (unsubscribe) unsubscribe();
    };
  }, [setConversationBadgeTracked]);

  // Calculate total unread count across all conversations
  const totalUnreadCount = useMemo(() => {
    let total = 0;
    const countedConversations = new Set<string>();

    contacts.forEach((contact) => {
      contact.linkedAccounts.forEach((account) => {
        const conversationId = account.conversationId;
        if (!conversationId || countedConversations.has(conversationId)) return;
        countedConversations.add(conversationId);
        if (badgeUntrackedConversationIds[conversationId]) return;
        total += countUnreadMessages(readStateByConversation[conversationId]);
      });
    });

    return total;
  }, [badgeUntrackedConversationIds, readStateByConversation, contacts]);

  // Update system tray badge when unread count changes — debounced to avoid
  // hammering the macOS badge API during bulk sync (hundreds of messages arrive in burst).
  useEffect(() => {
    const timer = setTimeout(() => {
      updateSystemTrayBadge(totalUnreadCount).catch((error: unknown) => {
        console.debug("Failed to update system tray badge:", error);
      });
    }, 800);
    return () => clearTimeout(timer);
  }, [totalUnreadCount]);
}
