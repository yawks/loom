import { create } from "zustand";
import type { models } from "../../wailsjs/go/models";

interface AppState {
  selectedContact: models.MetaContact | null;
  setSelectedContact: (contact: models.MetaContact | null, skipHistory?: boolean) => void;
  showThreads: boolean;
  setShowThreads: (show: boolean) => void;
  selectedThreadId: string | null;
  setSelectedThreadId: (threadId: string | null) => void;
  showConversationDetails: boolean;
  setShowConversationDetails: (show: boolean) => void;
  messageLayout: "bubble" | "irc";
  setMessageLayout: (layout: "bubble" | "irc") => void;
  theme: "light" | "dark";
  setTheme: (theme: "light" | "dark") => void;
  language: "en" | "fr";
  setLanguage: (language: "en" | "fr") => void;
  fontSize: number;
  setFontSize: (fontSize: number) => void;
  selectedAvatarUrl: string | null;
  setSelectedAvatarUrl: (url: string | null) => void;
  metaContacts: models.MetaContact[];
  setMetaContacts: (contacts: models.MetaContact[]) => void;
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

export const useAppStore = create<AppState>((set, get) => ({
  selectedContact: null,
  setSelectedContact: (contact, skipHistory = false) => {
    set({ selectedContact: contact });
    if (contact && !skipHistory) {
      get().addToHistory(contact);
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
      // When clearing thread, don't close the panel automatically
      // The user can close it manually with the toggle button
    }
  },
  showConversationDetails: false,
  setShowConversationDetails: (show) => set({ showConversationDetails: show }),
  messageLayout: loadFromStorage<"bubble" | "irc">("messageLayout", "bubble"),
  setMessageLayout: (layout) => {
    set({ messageLayout: layout });
    saveToStorage("messageLayout", layout);
  },
  theme: loadFromStorage<"light" | "dark">("theme", "dark"),
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
  selectedAvatarUrl: null,
  setSelectedAvatarUrl: (url) => set({ selectedAvatarUrl: url }),
  metaContacts: [],
  setMetaContacts: (contacts) => set({ metaContacts: contacts }),
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
