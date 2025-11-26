import { create } from "zustand";
import type { models } from "../../wailsjs/go/models";

interface AppState {
  selectedContact: models.MetaContact | null;
  setSelectedContact: (contact: models.MetaContact | null) => void;
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
  selectedAvatarUrl: string | null;
  setSelectedAvatarUrl: (url: string | null) => void;
  metaContacts: models.MetaContact[];
  setMetaContacts: (contacts: models.MetaContact[]) => void;
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

export const useAppStore = create<AppState>((set) => ({
  selectedContact: null,
  setSelectedContact: (contact) => set({ selectedContact: contact }),
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
  selectedAvatarUrl: null,
  setSelectedAvatarUrl: (url) => set({ selectedAvatarUrl: url }),
  metaContacts: [],
  setMetaContacts: (contacts) => set({ metaContacts: contacts }),
}));
