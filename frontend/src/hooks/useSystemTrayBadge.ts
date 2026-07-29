import { useEffect, useMemo } from "react";

import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";

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

  // Calculate total unread count across all conversations
  const totalUnreadCount = useMemo(() => {
    let total = 0;
    const debugInfo: { convId: string; unread: number; messages: string[] }[] = [];
    const countedConversations = new Set<string>();

    contacts.forEach((contact) => {
      contact.linkedAccounts.forEach((account) => {
        const conversationId = account.conversationId;
        if (!conversationId || countedConversations.has(conversationId)) return;
        countedConversations.add(conversationId);
        const conversationState = readStateByConversation[conversationId];
        if (!conversationState) return;
        const unreadMessages = Object.entries(conversationState).filter(
          ([key, isRead]) => !key.startsWith("_") && !isRead
        );
        const unreadCount = unreadMessages.length;
        if (unreadCount > 0) {
          const unreadIds = unreadMessages.map(([key]) => key);
          debugInfo.push({
            convId: conversationId,
            unread: unreadCount,
            messages: unreadIds.slice(0, 5),
          });
        }
        total += unreadCount;
      });
    });

    console.log(`useSystemTrayBadge: Total unread messages across all conversations: ${total}`);
    if (debugInfo.length > 0) {
      console.table(debugInfo);
    }
    return total;
  }, [readStateByConversation, contacts]);

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
