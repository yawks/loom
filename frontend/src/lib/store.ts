import { create } from "zustand";
import type { core, models } from "../../wailsjs/go/models";

export type Theme = "light" | "dark" | "system";
export type ContactSortOption = "alphabetical" | "last_message" | "unread";
export interface ContactProfileTarget {
  conversationId: string;
  userId: string;
  displayName: string;
  avatarUrl?: string;
  status: string;
  isSelf?: boolean;
}

interface AppState {
  selectedContact: models.MetaContact | null;
  setSelectedContact: (contact: models.MetaContact | null, skipHistory?: boolean) => void;
  showThreads: boolean;
  setShowThreads: (show: boolean) => void;
  selectedThreadId: string | null;
  setSelectedThreadId: (threadId: string | null) => void;
  selectedThreadParentMessage: models.Message | null;
  setSelectedThreadParentMessage: (msg: models.Message | null) => void;
  showConversationDetails: boolean;
  setShowConversationDetails: (show: boolean) => void;
  messageLayout: "bubble" | "irc";
  setMessageLayout: (layout: "bubble" | "irc") => void;
  theme: Theme;
  setTheme: (theme: Theme) => void;
  language: "en" | "fr";
  setLanguage: (language: "en" | "fr") => void;
  fontSize: number;
  setFontSize: (fontSize: number) => void;
  selectedAvatarUrl: string | null;
  setSelectedAvatarUrl: (url: string | null) => void;
  selectedContactProfile: ContactProfileTarget | null;
  setSelectedContactProfile: (target: ContactProfileTarget | null) => void;
  metaContacts: models.MetaContact[];
  setMetaContacts: (contacts: models.MetaContact[]) => void;
  renameGroupConversation: (conversationId: string, name: string) => void;
  selectedProviderFilter: string | null;
  setSelectedProviderFilter: (providerInstanceId: string | null) => void;
  messageSearchTargetId: string | null;
  setMessageSearchTargetId: (messageId: string | null) => void;
  unreadNavigationTarget: { conversationId: string; messageId: string; threadId?: string } | null;
  setUnreadNavigationTarget: (target: { conversationId: string; messageId: string; threadId?: string } | null) => void;
  contactSortBy: ContactSortOption;
  setContactSortBy: (sortBy: ContactSortOption) => void;
  selectedUserId: string | null;
  setSelectedUserId: (userId: string | null) => void;
  capabilities: Record<string, core.Capabilities>;
  setCapabilities: (instanceId: string, capabilities: core.Capabilities) => void;
  removeCapabilities: (instanceId: string) => void;
  isTypingInInput: boolean;
  setIsTypingInInput: (isTyping: boolean) => void;
  syncErrors: Record<string, string>;
  setSyncError: (instanceId: string, message: string) => void;
  clearSyncError: (instanceId: string) => void;
  // Navigation history
  conversationHistory: models.MetaContact[];
  historyIndex: number;
  addToHistory: (contact: models.MetaContact) => void;
  navigateHistoryBack: () => models.MetaContact | null;
  navigateHistoryForward: () => models.MetaContact | null;
}

// Load initial values from localStorage
const loadFromStorage = <T>(key: string, defaultValue: T): T => {
  if (typeof window === "undefined") return defaultValue;
  try {
    const item = window.localStorage.getItem(key);
    return item ? (JSON.parse(item) as T) : defaultValue;
  } catch {
    return defaultValue;
  }
};

// Save to localStorage
const saveToStorage = <T>(key: string, value: T): void => {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(key, JSON.stringify(value));
  } catch (error) {
    console.error(`Failed to save ${key} to localStorage:`, error);
  }
};

const updateRecentlyViewed = (contactId: number) => {
  if (typeof window === "undefined" || !contactId) return;
  try {
    const raw = window.localStorage.getItem("loom_recently_viewed_ids");
    let ids: number[] = raw ? JSON.parse(raw) : [];
    if (!Array.isArray(ids)) ids = [];
    ids = [contactId, ...ids.filter((id) => id !== contactId)].slice(0, 20);
    window.localStorage.setItem("loom_recently_viewed_ids", JSON.stringify(ids));
  } catch (error) {
    console.error("Failed to save recently viewed to localStorage:", error);
  }
};

export const useAppStore = create<AppState>((set, get) => ({
  selectedContact: null,
  setSelectedContact: (contact, skipHistory = false) => {
    set({ selectedContact: contact });
    if (contact && !skipHistory) {
      get().addToHistory(contact);
      updateRecentlyViewed(contact.id);
    }
  },
  showThreads: false,
  setShowThreads: (show) => set({ showThreads: show }),
  selectedThreadId: null,
  setSelectedThreadId: (threadId) => {
    set({ selectedThreadId: threadId });
    if (threadId) {
      set({ showThreads: true });
    } else {
      set({ selectedThreadParentMessage: null });
    }
  },
  selectedThreadParentMessage: null,
  setSelectedThreadParentMessage: (msg) => set({ selectedThreadParentMessage: msg }),
  showConversationDetails: false,
  setShowConversationDetails: (show) => set({ showConversationDetails: show }),
  messageLayout: loadFromStorage<"bubble" | "irc">("messageLayout", "bubble"),
  setMessageLayout: (layout) => {
    set({ messageLayout: layout });
    saveToStorage("messageLayout", layout);
  },
  theme: loadFromStorage<Theme>("theme", "system"),
  setTheme: (theme) => {
    set({ theme });
    saveToStorage("theme", theme);
  },
  language: loadFromStorage<"en" | "fr">("language", "en"),
  setLanguage: (language) => {
    set({ language });
    saveToStorage("language", language);
  },
  fontSize: loadFromStorage<number>("fontSize", 100),
  setFontSize: (fontSize) => {
    set({ fontSize });
    saveToStorage("fontSize", fontSize);
  },
  selectedProviderFilter: loadFromStorage<string | null>("selectedProviderFilter", null),
  setSelectedProviderFilter: (providerInstanceId) => {
    set({ selectedProviderFilter: providerInstanceId });
    saveToStorage("selectedProviderFilter", providerInstanceId);
  },
  messageSearchTargetId: null,
  setMessageSearchTargetId: (messageId) => set({ messageSearchTargetId: messageId }),
  unreadNavigationTarget: null,
  setUnreadNavigationTarget: (target) => set({ unreadNavigationTarget: target }),
  contactSortBy: loadFromStorage<ContactSortOption>("contactSortBy", "last_message"),
  setContactSortBy: (sortBy) => {
    set({ contactSortBy: sortBy });
    saveToStorage("contactSortBy", sortBy);
  },
  selectedUserId: null,
  setSelectedUserId: (userId) => set({ selectedUserId: userId }),
  capabilities: {},
  setCapabilities: (instanceId, caps) => set((state) => ({
    capabilities: { ...state.capabilities, [instanceId]: caps }
  })),
  removeCapabilities: (instanceId) => set((state) => {
    const next = { ...state.capabilities };
    delete next[instanceId];
    return { capabilities: next };
  }),
  isTypingInInput: false,
  setIsTypingInInput: (isTyping: boolean) => set({ isTypingInInput: isTyping }),
  syncErrors: {},
  setSyncError: (instanceId, message) => set((state) => ({
    syncErrors: { ...state.syncErrors, [instanceId]: message }
  })),
  clearSyncError: (instanceId) => set((state) => {
    const next = { ...state.syncErrors };
    delete next[instanceId];
    return { syncErrors: next };
  }),
  selectedAvatarUrl: null,
  setSelectedAvatarUrl: (url) => set({ selectedAvatarUrl: url }),
  selectedContactProfile: null,
  setSelectedContactProfile: (target) => set({ selectedContactProfile: target }),
  metaContacts: [],
  setMetaContacts: (contacts) => set((state) => {
    let selectedContact = state.selectedContact;
    if (selectedContact) {
      const refreshed = contacts.find((c) => c.id === selectedContact!.id);
      if (refreshed) {
        const activeAccount = selectedContact.linkedAccounts[0];
        const matchingAccount = activeAccount
          ? refreshed.linkedAccounts.find((account) =>
              account.providerInstanceId === activeAccount.providerInstanceId &&
              (account.userId === activeAccount.userId || account.conversationId === activeAccount.conversationId)
            )
          : undefined;
        const preservedAccount = matchingAccount && activeAccount?.conversationId && !matchingAccount.conversationId
          ? { ...matchingAccount, conversationId: activeAccount.conversationId } as models.LinkedAccount
          : matchingAccount;
        selectedContact = preservedAccount
          ? { ...refreshed, linkedAccounts: [preservedAccount, ...refreshed.linkedAccounts.filter((account) => account !== matchingAccount)] } as models.MetaContact
          : refreshed;
      }
    }
    return { metaContacts: contacts, selectedContact };
  }),
  renameGroupConversation: (conversationId, name) => set((state) => {
    const renameContact = (contact: models.MetaContact) => {
      const matches = contact.linkedAccounts.some(
        (account) => account.conversationId === conversationId || account.userId === conversationId
      );
      if (!matches) return contact;
      return {
        ...contact,
        displayName: name,
        linkedAccounts: contact.linkedAccounts.map((account) =>
          account.conversationId === conversationId || account.userId === conversationId
            ? { ...account, username: name } as models.LinkedAccount
            : account
        ),
      } as models.MetaContact;
    };
    return {
      metaContacts: state.metaContacts.map(renameContact),
      selectedContact: state.selectedContact ? renameContact(state.selectedContact) : null,
      conversationHistory: state.conversationHistory.map(renameContact),
    };
  }),
  // Navigation history
  conversationHistory: [],
  historyIndex: -1,
  addToHistory: (contact) => {
    set((state) => {
      // Remove any future history if we're not at the end
      const newHistory = state.historyIndex >= 0 
        ? state.conversationHistory.slice(0, state.historyIndex + 1)
        : [];
      
      // Don't add if it's the same as the current contact
      if (newHistory.length > 0 && newHistory[newHistory.length - 1]?.id === contact.id) {
        return state;
      }
      
      // Add new contact to history
      newHistory.push(contact);
      // Limit history to 50 entries
      const limitedHistory = newHistory.slice(-50);
      
      return {
        conversationHistory: limitedHistory,
        historyIndex: limitedHistory.length - 1,
      };
    });
  },
  navigateHistoryBack: () => {
    let result: models.MetaContact | null = null;
    set((state) => {
      if (state.historyIndex > 0) {
        const newIndex = state.historyIndex - 1;
        result = state.conversationHistory[newIndex] || null;
        return { historyIndex: newIndex };
      }
      return state;
    });
    return result;
  },
  navigateHistoryForward: () => {
    let result: models.MetaContact | null = null;
    set((state) => {
      if (state.historyIndex < state.conversationHistory.length - 1) {
        const newIndex = state.historyIndex + 1;
        result = state.conversationHistory[newIndex] || null;
        return { historyIndex: newIndex };
      }
      return state;
    });
    return result;
  },
}));
