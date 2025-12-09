import { useEffect, useCallback } from "react";
import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useSortedContacts } from "./useSortedContacts";

export function useKeyboardShortcuts() {
  const selectedContact = useAppStore((state) => state.selectedContact);
  const setSelectedContact = useAppStore((state) => state.setSelectedContact);
  const navigateHistoryBack = useAppStore((state) => state.navigateHistoryBack);
  const navigateHistoryForward = useAppStore((state) => state.navigateHistoryForward);
  const readStateByConversation = useMessageReadStore(
    (state) => state.readByConversation
  );

  // Get sorted contacts using the same logic as ContactList
  const { sortedContacts } = useSortedContacts("last_message");

  const navigateToUnreadConversation = useCallback((direction: "up" | "down") => {
    if (!selectedContact || sortedContacts.length === 0) {
      return;
    }

    // Find conversations with unread messages
    const unreadConversations = sortedContacts.filter((contact) => {
      const conversationId = contact.linkedAccounts[0]?.userId;
      if (!conversationId) return false;
      const conversationState = readStateByConversation[conversationId];
      if (!conversationState) return false;
      const unreadCount = Object.values(conversationState).filter(
        (isRead) => !isRead
      ).length;
      return unreadCount > 0;
    });

    if (unreadConversations.length === 0) {
      return;
    }

    // Find current position in sorted list
    const currentIndex = sortedContacts.findIndex(
      (c) => c.id === selectedContact.id
    );

    if (currentIndex === -1) {
      // Current contact not found, select first/last unread
      const targetContact =
        direction === "up"
          ? unreadConversations[unreadConversations.length - 1]
          : unreadConversations[0];
      if (targetContact) {
        setSelectedContact(targetContact);
      }
      return;
    }

    // Find next/previous unread conversation
    if (direction === "up") {
      // Look for unread conversations above current
      for (let i = currentIndex - 1; i >= 0; i--) {
        const contact = sortedContacts[i];
        const conversationId = contact.linkedAccounts[0]?.userId;
        if (conversationId) {
          const conversationState = readStateByConversation[conversationId];
          if (conversationState) {
            const unreadCount = Object.values(conversationState).filter(
              (isRead) => !isRead
            ).length;
            if (unreadCount > 0) {
              setSelectedContact(contact);
              return;
            }
          }
        }
      }
      // Wrap around: go to last unread conversation
      const lastUnread = unreadConversations[unreadConversations.length - 1];
      if (lastUnread && lastUnread.id !== selectedContact.id) {
        setSelectedContact(lastUnread);
      }
    } else {
      // Look for unread conversations below current
      for (let i = currentIndex + 1; i < sortedContacts.length; i++) {
        const contact = sortedContacts[i];
        const conversationId = contact.linkedAccounts[0]?.userId;
        if (conversationId) {
          const conversationState = readStateByConversation[conversationId];
          if (conversationState) {
            const unreadCount = Object.values(conversationState).filter(
              (isRead) => !isRead
            ).length;
            if (unreadCount > 0) {
              setSelectedContact(contact);
              return;
            }
          }
        }
      }
      // Wrap around: go to first unread conversation
      const firstUnread = unreadConversations[0];
      if (firstUnread && firstUnread.id !== selectedContact.id) {
        setSelectedContact(firstUnread);
      }
    }
  }, [selectedContact, sortedContacts, readStateByConversation, setSelectedContact]);

  const navigateInList = useCallback((direction: "up" | "down") => {
    if (!selectedContact || sortedContacts.length === 0) {
      // If no contact selected, select first or last
      if (sortedContacts.length > 0) {
        const targetContact = direction === "down" ? sortedContacts[0] : sortedContacts[sortedContacts.length - 1];
        setSelectedContact(targetContact);
      }
      return;
    }

    // Find current position in sorted list
    const currentIndex = sortedContacts.findIndex(
      (c) => c.id === selectedContact.id
    );

    if (currentIndex === -1) {
      // Current contact not found, select first or last
      const targetContact = direction === "down" ? sortedContacts[0] : sortedContacts[sortedContacts.length - 1];
      if (targetContact) {
        setSelectedContact(targetContact);
      }
      return;
    }

    // Navigate to next/previous conversation in list
    if (direction === "down") {
      // Go to conversation below
      if (currentIndex < sortedContacts.length - 1) {
        setSelectedContact(sortedContacts[currentIndex + 1]);
      } else {
        // Wrap around: go to first
        setSelectedContact(sortedContacts[0]);
      }
    } else {
      // Go to conversation above
      if (currentIndex > 0) {
        setSelectedContact(sortedContacts[currentIndex - 1]);
      } else {
        // Wrap around: go to last
        setSelectedContact(sortedContacts[sortedContacts.length - 1]);
      }
    }
  }, [selectedContact, sortedContacts, setSelectedContact]);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // Don't trigger shortcuts if user is typing in an input, textarea, or contenteditable
      const target = e.target as HTMLElement;
      if (
        target.tagName === "INPUT" ||
        target.tagName === "TEXTAREA" ||
        target.isContentEditable
      ) {
        return;
      }

      const isMac = navigator.platform.toUpperCase().indexOf("MAC") >= 0;
      const optionKey = e.altKey; // Alt on both Mac and PC
      const commandKey = isMac ? e.metaKey : e.ctrlKey;
      const shiftKey = e.shiftKey;

      // Option/Alt + Shift + ArrowUp: Navigate to conversation above with unread messages
      if (optionKey && shiftKey && e.key === "ArrowUp") {
        e.preventDefault();
        navigateToUnreadConversation("up");
        return;
      }

      // Option/Alt + Shift + ArrowDown: Navigate to conversation below with unread messages
      if (optionKey && shiftKey && e.key === "ArrowDown") {
        e.preventDefault();
        navigateToUnreadConversation("down");
        return;
      }

      // Command/Ctrl + ArrowLeft: Navigate to previous conversation in history
      if (commandKey && !shiftKey && e.key === "ArrowLeft") {
        e.preventDefault();
        const previousContact = navigateHistoryBack();
        if (previousContact) {
          setSelectedContact(previousContact, true); // Skip history to avoid duplicates
        }
        return;
      }

      // Command/Ctrl + ArrowRight: Navigate to next conversation in history
      if (commandKey && !shiftKey && e.key === "ArrowRight") {
        e.preventDefault();
        const nextContact = navigateHistoryForward();
        if (nextContact) {
          setSelectedContact(nextContact, true); // Skip history to avoid duplicates
        }
        return;
      }

      // Command/Ctrl + ArrowDown: Navigate to conversation below in list
      if (commandKey && !shiftKey && e.key === "ArrowDown") {
        e.preventDefault();
        navigateInList("down");
        return;
      }

      // Command/Ctrl + ArrowUp: Navigate to conversation above in list
      if (commandKey && !shiftKey && e.key === "ArrowUp") {
        e.preventDefault();
        navigateInList("up");
        return;
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
    };
  }, [selectedContact, sortedContacts, readStateByConversation, setSelectedContact, navigateHistoryBack, navigateHistoryForward, navigateToUnreadConversation, navigateInList]);
}

