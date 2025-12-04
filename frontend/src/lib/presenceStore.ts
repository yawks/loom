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
    console.log(`[PresenceStore] Setting presence for ${userID}: online=${isOnline}, lastSeen=${lastSeen}`);
    set((state) => {
      // Only update if the value actually changed
      const currentOnline = state.presenceMap[userID];
      const currentLastSeen = state.lastSeenMap[userID];
      
      if (currentOnline === isOnline && currentLastSeen === (lastSeen > 0 ? lastSeen : currentLastSeen)) {
        // No change, return state as-is to prevent unnecessary re-renders
        console.log(`[PresenceStore] No change detected for ${userID}, skipping update`);
        return state;
      }
      
      const newPresenceMap = { ...state.presenceMap };
      const newLastSeenMap = { ...state.lastSeenMap };
      
      newPresenceMap[userID] = isOnline;
      if (lastSeen > 0) {
        newLastSeenMap[userID] = lastSeen;
      }
      
      console.log(`[PresenceStore] Updated presenceMap:`, Object.entries(newPresenceMap));
      console.log(`[PresenceStore] New presenceMap object reference created`);
      
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
    console.log("Presence event received:", eventData);
    try {
      const event = JSON.parse(eventData) as { UserID: string; IsOnline: boolean; LastSeen: number };
      console.log("Parsed presence event:", event);
      usePresenceStore.getState().setPresence(event.UserID, event.IsOnline, event.LastSeen);
    } catch (error) {
      console.error("Failed to parse presence event:", error, eventData);
    }
  });
}
