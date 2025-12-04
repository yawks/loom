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

    contacts.forEach((contact) => {
      const conversationId = contact.linkedAccounts[0]?.userId;
      if (!conversationId) {
        return;
      }

      const conversationState = readStateByConversation[conversationId];
      if (!conversationState) {
        return;
      }

      const unreadCount = Object.values(conversationState).filter(
        (isRead) => !isRead
      ).length;
      total += unreadCount;
    });

    return total;
  }, [readStateByConversation, contacts]);

  // Update system tray badge when unread count changes
  useEffect(() => {
    updateSystemTrayBadge(totalUnreadCount).catch((error: unknown) => {
      // Silently handle errors (e.g., if system tray is not available)
      console.debug("Failed to update system tray badge:", error);
    });
  }, [totalUnreadCount]);
}

