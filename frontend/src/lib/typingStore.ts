import { create } from "zustand";

/**
 * Represents a user typing in a conversation
 */
interface TypingUser {
  userId: string;
  userName?: string;
  timestamp: number;
}

/**
 * State for managing typing indicators across conversations
 */
interface TypingState {
  // Map of conversationId -> array of users currently typing
  typingByConversation: Record<string, TypingUser[]>;
  
  /**
   * Set a user as typing in a conversation
   */
  setTyping: (conversationId: string, userId: string, userName?: string) => void;
  
  /**
   * Set a user as not typing in a conversation
   */
  setNotTyping: (conversationId: string, userId: string) => void;
  
  /**
   * Clear expired typing indicators (older than 5 seconds)
   */
  clearExpired: () => void;
}

// Timeout for typing indicators (5 seconds)
const TYPING_TIMEOUT_MS = 5000;

export const useTypingStore = create<TypingState>((set) => ({
  typingByConversation: {},
  
  setTyping: (conversationId, userId, userName) => {
    set((state) => {
      const currentTyping = state.typingByConversation[conversationId] || [];
      
      // Check if user is already in the list
      const existingIndex = currentTyping.findIndex((u) => u.userId === userId);
      
      const newTypingUser: TypingUser = {
        userId,
        userName,
        timestamp: Date.now(),
      };
      
      let updatedTyping: TypingUser[];
      if (existingIndex >= 0) {
        // Update existing entry
        updatedTyping = [...currentTyping];
        updatedTyping[existingIndex] = newTypingUser;
      } else {
        // Add new entry
        updatedTyping = [...currentTyping, newTypingUser];
      }
      
      return {
        typingByConversation: {
          ...state.typingByConversation,
          [conversationId]: updatedTyping,
        },
      };
    });
  },
  
  setNotTyping: (conversationId, userId) => {
    set((state) => {
      const currentTyping = state.typingByConversation[conversationId] || [];
      if (!currentTyping.some((user) => user.userId === userId)) return state;
      const filteredTyping = currentTyping.filter((u) => u.userId !== userId);
      
      if (filteredTyping.length === 0) {
        // Remove the conversation from the map if no one is typing
        const { [conversationId]: _, ...rest } = state.typingByConversation;
        return { typingByConversation: rest };
      }
      
      return {
        typingByConversation: {
          ...state.typingByConversation,
          [conversationId]: filteredTyping,
        },
      };
    });
  },
  
  clearExpired: () => {
    set((state) => {
      const now = Date.now();
      const updated: Record<string, TypingUser[]> = {};
      let hasChanges = false;
      
      Object.entries(state.typingByConversation).forEach(([conversationId, users]) => {
        const validUsers = users.filter((u) => now - u.timestamp < TYPING_TIMEOUT_MS);
        if (validUsers.length !== users.length) {
          hasChanges = true;
        }
        if (validUsers.length > 0) {
          updated[conversationId] = validUsers;
        } else if (users.length > 0) {
          hasChanges = true;
        }
      });
      
      // Only update if there are actual changes
      if (!hasChanges) {
        return state;
      }
      
      return { typingByConversation: updated };
    });
  },
}));

// No permanent interval at idle: one timer targets the earliest actual expiry.
// A new typing event extends that user's deadline; removals cancel unused work.
export function startTypingExpiryTimer() {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const schedule = () => {
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
    let next = Infinity;
    for (const users of Object.values(useTypingStore.getState().typingByConversation)) {
      for (const user of users) next = Math.min(next, user.timestamp + TYPING_TIMEOUT_MS);
    }
    if (next === Infinity) return;
    timer = setTimeout(() => {
      timer = undefined;
      useTypingStore.getState().clearExpired();
      schedule();
    }, Math.max(0, next - Date.now()));
  };
  const unsubscribe = useTypingStore.subscribe(schedule);
  schedule();
  return () => {
    unsubscribe();
    if (timer !== undefined) clearTimeout(timer);
  };
}

if (typeof window !== "undefined") {
  startTypingExpiryTimer();
}
