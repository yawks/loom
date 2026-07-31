import { useCallback, useEffect, useMemo } from "react";

import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useSortedContacts } from "./useSortedContacts";
import type { models } from "../../wailsjs/go/models";

export function useKeyboardShortcuts() {
  const selectedContact = useAppStore((state) => state.selectedContact);
  const setSelectedContact = useAppStore((state) => state.setSelectedContact);
  const setShowThreads = useAppStore((state) => state.setShowThreads);
  const setSelectedThreadId = useAppStore((state) => state.setSelectedThreadId);
  const navigateHistoryBack = useAppStore((state) => state.navigateHistoryBack);
  const navigateHistoryForward = useAppStore((state) => state.navigateHistoryForward);
  const contactSortBy = useAppStore((state) => state.contactSortBy);
  const selectedProviderFilter = useAppStore((state) => state.selectedProviderFilter);
  const readStateByConversation = useMessageReadStore(
    (state) => state.readByConversation
  );
  const finishConversationNavigation = useCallback(() => {
    setShowThreads(false);
    setSelectedThreadId(null);
    window.dispatchEvent(new Event("focus-main-composer"));
  }, [setSelectedThreadId, setShowThreads]);
  const relevantAccounts = useCallback(
    (contact: models.MetaContact) =>
      selectedProviderFilter
        ? contact.linkedAccounts.filter(
            (account) => account.providerInstanceId === selectedProviderFilter
          )
        : contact.linkedAccounts,
    [selectedProviderFilter]
  );
  const hasUnread = useCallback(
    (contact: models.MetaContact) =>
      relevantAccounts(contact).some((account) => {
        const conversationId = account.conversationId;
        const state = conversationId
          ? readStateByConversation[conversationId]
          : undefined;
        return state
          ? Object.entries(state).some(
              ([key, isRead]) => !key.startsWith("_") && !isRead
            )
          : false;
      }),
    [readStateByConversation, relevantAccounts]
  );
  const selectContactForCurrentProvider = useCallback(
    (contact: models.MetaContact) => {
      const activeAccount =
        relevantAccounts(contact).find((account) => {
          if (contactSortBy !== "unread") return true;
          const conversationId = account.conversationId;
          const state = conversationId
            ? readStateByConversation[conversationId]
            : undefined;
          return state
            ? Object.entries(state).some(
                ([key, isRead]) => !key.startsWith("_") && !isRead
              )
            : false;
        }) ?? contact.linkedAccounts[0];
      if (!activeAccount || contact.linkedAccounts[0] === activeAccount) {
        setSelectedContact(contact);
        return;
      }
      setSelectedContact({
        ...contact,
        linkedAccounts: [
          activeAccount,
          ...contact.linkedAccounts.filter((account) => account !== activeAccount),
        ],
      } as models.MetaContact);
    },
    [
      contactSortBy,
      readStateByConversation,
      relevantAccounts,
      setSelectedContact,
    ]
  );

  // Build the same sorted and filtered list as ContactList.
  const { sortedContacts: sortedContactsBase } = useSortedContacts(contactSortBy);
  const sortedContacts = useMemo(() => {
    const providerContacts = selectedProviderFilter
      ? sortedContactsBase.filter((contact) =>
          (contact.linkedAccounts ?? []).some(
            (account) => account.providerInstanceId === selectedProviderFilter
          )
        )
      : sortedContactsBase;

    if (contactSortBy !== "unread") {
      return providerContacts;
    }

    return providerContacts.filter(hasUnread);
  }, [
    contactSortBy,
    hasUnread,
    selectedProviderFilter,
    sortedContactsBase,
  ]);

  const navigateToUnreadConversation = useCallback((direction: "up" | "down") => {
    if (!selectedContact || sortedContacts.length === 0) {
      return;
    }

    // Find conversations with unread messages
    const unreadConversations = sortedContacts.filter(hasUnread);

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
        selectContactForCurrentProvider(targetContact);
        finishConversationNavigation();
      }
      return;
    }

    // Find next/previous unread conversation
    if (direction === "up") {
      // Look for unread conversations above current
      for (let i = currentIndex - 1; i >= 0; i--) {
        const contact = sortedContacts[i];
        if (hasUnread(contact)) {
          selectContactForCurrentProvider(contact);
          finishConversationNavigation();
          return;
        }
      }
      // Wrap around: go to last unread conversation
      const lastUnread = unreadConversations[unreadConversations.length - 1];
      if (lastUnread && lastUnread.id !== selectedContact.id) {
        selectContactForCurrentProvider(lastUnread);
        finishConversationNavigation();
      }
    } else {
      // Look for unread conversations below current
      for (let i = currentIndex + 1; i < sortedContacts.length; i++) {
        const contact = sortedContacts[i];
        if (hasUnread(contact)) {
          selectContactForCurrentProvider(contact);
          finishConversationNavigation();
          return;
        }
      }
      // Wrap around: go to first unread conversation
      const firstUnread = unreadConversations[0];
      if (firstUnread && firstUnread.id !== selectedContact.id) {
        selectContactForCurrentProvider(firstUnread);
        finishConversationNavigation();
      }
    }
  }, [selectedContact, sortedContacts, hasUnread, selectContactForCurrentProvider, finishConversationNavigation]);

  const navigateInList = useCallback((direction: "up" | "down") => {
    if (!selectedContact || sortedContacts.length === 0) {
      // If no contact selected, select first or last
      if (sortedContacts.length > 0) {
        const targetContact = direction === "down" ? sortedContacts[0] : sortedContacts[sortedContacts.length - 1];
        setSelectedContact(targetContact);
        finishConversationNavigation();
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
        finishConversationNavigation();
      }
      return;
    }

    // Navigate to next/previous conversation in list
    if (direction === "down") {
      // Go to conversation below
      if (currentIndex < sortedContacts.length - 1) {
        setSelectedContact(sortedContacts[currentIndex + 1]);
        finishConversationNavigation();
      } else {
        // Wrap around: go to first
        setSelectedContact(sortedContacts[0]);
        finishConversationNavigation();
      }
    } else {
      // Go to conversation above
      if (currentIndex > 0) {
        setSelectedContact(sortedContacts[currentIndex - 1]);
        finishConversationNavigation();
      } else {
        // Wrap around: go to last
        setSelectedContact(sortedContacts[sortedContacts.length - 1]);
        finishConversationNavigation();
      }
    }
  }, [selectedContact, sortedContacts, setSelectedContact, finishConversationNavigation]);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      const isMac = navigator.platform.toUpperCase().indexOf("MAC") >= 0;
      const optionKey = e.altKey; // Alt on both Mac and PC
      const commandKey = isMac ? e.metaKey : e.ctrlKey;
      const shiftKey = e.shiftKey;
      const isArrowLeft = e.key === "ArrowLeft" || e.code === "ArrowLeft";
      const isArrowRight = e.key === "ArrowRight" || e.code === "ArrowRight";

      // For arrow keys without modifiers, check if we're in a textarea to avoid conflicts
      // with message history navigation
      const target = e.target as HTMLElement;
      if (
        (e.key === "ArrowUp" || e.key === "ArrowDown") &&
        !optionKey && !commandKey && !shiftKey &&
        (target.tagName === "TEXTAREA" || target.tagName === "INPUT")
      ) {
        // Let the textarea handle arrow keys for message history navigation
        return;
      }

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

      // Ctrl + Shift + ArrowLeft: Navigate to previous conversation in history
      if (e.ctrlKey && shiftKey && isArrowLeft) {
        e.preventDefault();
        e.stopPropagation();
        const previousContact = navigateHistoryBack();
        if (previousContact) {
          setSelectedContact(previousContact, true); // Skip history to avoid duplicates
          finishConversationNavigation();
        }
        return;
      }

      // Ctrl + Shift + ArrowRight: Navigate to next conversation in history
      if (e.ctrlKey && shiftKey && isArrowRight) {
        e.preventDefault();
        e.stopPropagation();
        const nextContact = navigateHistoryForward();
        if (nextContact) {
          setSelectedContact(nextContact, true); // Skip history to avoid duplicates
          finishConversationNavigation();
        }
        return;
      }

      // Option/Alt + ArrowDown: Navigate to conversation below in list
      if (optionKey && !shiftKey && e.key === "ArrowDown") {
        e.preventDefault();
        navigateInList("down");
        return;
      }

      // Option/Alt + ArrowUp: Navigate to conversation above in list
      if (optionKey && !shiftKey && e.key === "ArrowUp") {
        e.preventDefault();
        navigateInList("up");
        return;
      }
    };

    window.addEventListener("keydown", handleKeyDown, true);
    return () => {
      window.removeEventListener("keydown", handleKeyDown, true);
    };
  }, [selectedContact, sortedContacts, readStateByConversation, setSelectedContact, navigateHistoryBack, navigateHistoryForward, navigateToUnreadConversation, navigateInList, finishConversationNavigation]);
}
