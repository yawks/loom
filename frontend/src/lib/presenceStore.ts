import { create } from "zustand";
import { EventsOn } from "../../wailsjs/runtime/runtime";

interface PresenceState {
  // Object of userID -> isOnline (using object instead of Map for better Zustand reactivity)
  presenceMap: Record<string, boolean>;
  // Object of userID -> lastSeen timestamp
  lastSeenMap: Record<string, number>;
  
  setPresence: (userID: string, isOnline: boolean, lastSeen: number) => void;
  isOnline: (userID: string) => boolean;
  getLastSeen: (userID: string) => number | null;
}

export const usePresenceStore = create<PresenceState>((set, get) => ({
  presenceMap: {},
  lastSeenMap: {},

  setPresence: (userID: string, isOnline: boolean, lastSeen: number) => {
    set((state) => {
      // Only update if the value actually changed
      const currentOnline = state.presenceMap[userID];
      const currentLastSeen = state.lastSeenMap[userID];
      
      if (currentOnline === isOnline && currentLastSeen === (lastSeen > 0 ? lastSeen : currentLastSeen)) {
        // No change, return state as-is to prevent unnecessary re-renders
        return state;
      }
      
      const newPresenceMap = { ...state.presenceMap };
      const newLastSeenMap = { ...state.lastSeenMap };
      
      newPresenceMap[userID] = isOnline;
      if (lastSeen > 0) {
        newLastSeenMap[userID] = lastSeen;
      }
      
      return {
        presenceMap: newPresenceMap,
        lastSeenMap: newLastSeenMap,
      };
    });
  },

  isOnline: (userID: string) => {
    return get().presenceMap[userID] ?? false;
  },

  getLastSeen: (userID: string) => {
    return get().lastSeenMap[userID] ?? null;
  },
}));

// Listen to presence events from backend
if (typeof window !== "undefined") {
  EventsOn("presence", (eventData: string) => {
    try {
      const event = JSON.parse(eventData) as { userId: string; isOnline: boolean; lastSeen: number };
      usePresenceStore.getState().setPresence(event.userId, event.isOnline, event.lastSeen);
    } catch (error) {
      console.error("Failed to parse presence event:", error, eventData);
    }
  });
}
