import { create } from "zustand";
import type { models } from "../../wailsjs/go/models";
import { timeToDate } from "./utils";

// Extend Window interface to include Wails runtime
declare global {
  interface Window {
    go?: {
      main?: {
        App?: {
          MarkMessageAsRead?: (conversationID: string, messageID: string) => Promise<void>;
          [key: string]: unknown;
        };
      };
    };
  }
}

// Helper function to call MarkMessageAsRead via Wails runtime
// This will work even if bindings haven't been regenerated
const markMessageAsReadOnServer = (conversationID: string, messageID: string): Promise<void> => {
  return new Promise((resolve, reject) => {
    if (typeof window === "undefined") {
      reject(new Error("Window is undefined"));
      return;
    }
    if (!window.go?.main?.App) {
      reject(new Error("Wails runtime not available"));
      return;
    }
    
    const markMessageAsReadFn = window.go.main.App.MarkMessageAsRead;
    if (!markMessageAsReadFn || typeof markMessageAsReadFn !== 'function') {
      reject(new Error("MarkMessageAsRead not available"));
      return;
    }
      markMessageAsReadFn(conversationID, messageID)
      .then(resolve)
      .catch(reject);
  });
};

type ConversationId = string;
type MessageId = string;
type ConversationReadState = Record<MessageId, boolean>;
type ReadStateByConversation = Record<ConversationId, ConversationReadState>;

interface MessageReadStore {
  readByConversation: ReadStateByConversation;
  syncConversation: (conversationId: ConversationId, messages: models.Message[]) => void;
  setLastReadTimestamp: (conversationId: ConversationId, lastReadTS: string) => void;
  markAsRead: (conversationId: ConversationId, messageId: MessageId) => void;
  markMultipleAsRead: (conversationId: ConversationId, messageIds: MessageId[]) => void;
  markAsReadByProtocolId: (conversationId: ConversationId, protocolMsgId: string) => void;
  /** Mark a message as read locally without sending a receipt back to the server.
   *  Use this for self-read events (e.g. WhatsApp ReceiptTypeReadSelf) to avoid receipt loops. */
  markAsReadSilently: (conversationId: ConversationId, messageId: MessageId) => void;
  registerIncomingMessage: (message: models.Message) => void;
  registerBatchMessages: (messages: models.Message[]) => void;
  clearConversation: (conversationId: ConversationId) => void;
  cleanupObsoleteMessages: (conversationId: ConversationId, validMessageIds: Set<string>) => void;
}

const STORAGE_KEY = "loom-message-read-state";

const canUseStorage = typeof window !== "undefined";

// Debug function to inspect the stored state - can be called from browser console
// Usage: window.debugMessageReadStore()
if (typeof window !== "undefined") {
  (window as any).debugMessageReadStore = () => {
    try {
      const raw = window.localStorage.getItem(STORAGE_KEY);
      if (!raw) {
        console.log("No stored message read state found");
        return;
      }
      const parsed = JSON.parse(raw);
      console.log("=== Message Read Store Debug ===");
      console.log(`Total conversations: ${Object.keys(parsed).length}`);
      
      Object.entries(parsed).forEach(([convId, state]: [string, any]) => {
        const allKeys = Object.keys(state);
        const messageKeys = allKeys.filter(k => !k.startsWith("_"));
        const unreadCount = Object.entries(state).filter(
          ([key, isRead]) => !key.startsWith("_") && !isRead
        ).length;
        const lastReadTS = state._lastReadTS;
        
        console.log(`\nConversation: ${convId}`);
        console.log(`  Total messages: ${messageKeys.length}`);
        console.log(`  Unread: ${unreadCount}`);
        console.log(`  LastReadTS: ${lastReadTS || 'none'}`);
        
        if (unreadCount > 0) {
          const unreadIds = Object.entries(state)
            .filter(([key, isRead]) => !key.startsWith("_") && !isRead)
            .map(([key]) => key)
            .slice(0, 5);
          console.log(`  Unread IDs:`, unreadIds);
        }
      });
      
      console.log("\n=== Raw State ===");
      console.log(parsed);
    } catch (error) {
      console.error("Failed to debug message read store:", error);
    }
  };
  
  // Function to clear the stored state
  (window as any).clearMessageReadStore = () => {
    window.localStorage.removeItem(STORAGE_KEY);
    console.log("Message read store cleared. Reload the page to reset.");
  };
}

const loadPersistedState = (): ReadStateByConversation => {
  if (!canUseStorage) {
    return {};
  }
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) {
      return {};
    }
    const parsed = JSON.parse(raw) as ReadStateByConversation;
    if (parsed && typeof parsed === "object") {
      return parsed;
    }
  } catch (error) {
    console.warn("Failed to load message read state:", error);
  }
  return {};
};

const persistState = (state: ReadStateByConversation) => {
  if (!canUseStorage) {
    return;
  }
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
  } catch (error) {
    console.warn("Failed to persist message read state:", error);
  }
};

// Debounced persistence: avoid blocking the main thread with localStorage writes during scroll.
// markAsRead is called on every IntersectionObserver entry; batching writes prevents jank.
let _persistTimer: ReturnType<typeof setTimeout> | null = null;
let _latestState: ReadStateByConversation | null = null;
const persistStateDebounced = (state: ReadStateByConversation) => {
  _latestState = state;
  if (_persistTimer === null) {
    _persistTimer = setTimeout(() => {
      if (_latestState !== null) persistState(_latestState);
      _latestState = null;
      _persistTimer = null;
    }, 500);
  }
};

const getMessageIdentifier = (message: models.Message): MessageId | null => {
  if (message.protocolMsgId && message.protocolMsgId.trim().length > 0) {
    return message.protocolMsgId;
  }
  if (message.id) {
    return `message-${message.id}`;
  }
  const timestamp = timeToDate(message.timestamp).getTime();
  return Number.isNaN(timestamp) ? null : `ts-${timestamp}`;
};

export const useMessageReadStore = create<MessageReadStore>((set) => {
  const initialState = loadPersistedState();
  console.log(`messageReadStore: Loaded persisted state, ${Object.keys(initialState).length} conversations`);
  
  // Debug: Log any conversations with unread messages from persisted state
  Object.entries(initialState).forEach(([convId, state]) => {
    const unreadCount = Object.entries(state).filter(
      ([key, isRead]) => !key.startsWith("_") && !isRead
    ).length;
    if (unreadCount > 0) {
      console.warn(`⚠️ messageReadStore: Loaded conversation ${convId} with ${unreadCount} unread messages from localStorage`);
      const unreadIds = Object.entries(state)
        .filter(([key, isRead]) => !key.startsWith("_") && !isRead)
        .map(([key]) => key)
        .slice(0, 5);
      console.log(`   Unread IDs:`, unreadIds);
    }
  });
  
  return {
    readByConversation: initialState,
    syncConversation: (conversationId, messages) => {
      if (!conversationId) {
        return;
      }
      set((state) => {
        const existingState = state.readByConversation[conversationId];
        // hasExisting should only count actual messages, not special keys like _lastReadTS
        const existingMessageCount = existingState 
          ? Object.keys(existingState).filter(key => !key.startsWith("_")).length
          : 0;
        const hasExisting = existingMessageCount > 0;
        
        const lastReadTS = existingState
          ? ((existingState as any)["_lastReadTS"] as string | undefined)
          : undefined;
        
        console.log(`messageReadStore: syncConversation - conversationId: ${conversationId}, hasExisting: ${hasExisting} (${existingMessageCount} messages), lastReadTS: ${lastReadTS || 'none'}, messages to sync: ${messages.length}`);
      
      // Create a set of message IDs that actually exist in the conversation
      const existingMessageIds = new Set<string>();
      messages.forEach((message) => {
        const messageId = getMessageIdentifier(message);
        if (messageId) {
          existingMessageIds.add(messageId);
        }
      });

      // Start with existing state, but only keep messages that still exist
      const nextState: ConversationReadState = {};
      let hasChanged = false;
      let removedCount = 0;

      // Only keep messages that exist in the current conversation.
      // Special keys (prefixed with "_", e.g. _lastReadTS) are always preserved.
      if (existingState) {
        Object.keys(existingState).forEach((messageId) => {
          if (messageId.startsWith("_") || existingMessageIds.has(messageId)) {
            nextState[messageId] = existingState[messageId];
          } else {
            removedCount++;
            hasChanged = true;
          }
        });
      }

      // Add new messages that aren't in the store yet
      messages.forEach((message) => {
        const messageId = getMessageIdentifier(message);
        if (!messageId) {
          return;
        }

        if (nextState[messageId] === undefined) {
          const isCallMessage = message.callType && message.callType.trim() !== "";
          if (isCallMessage || message.isFromMe) {
            nextState[messageId] = true;
          } else {
            // Always check lastReadTS from provider to determine read state
            // This ensures we use provider's own read markers rather than guessing
            if (lastReadTS) {
              // Parse provider timestamp and compare with message timestamp
              const lastReadTimestamp = parseFloat(lastReadTS);
              if (!isNaN(lastReadTimestamp)) {
                const lastReadDate = new Date(lastReadTimestamp * 1000);
                const messageDate = timeToDate(message.timestamp);
                // Mark as read if message timestamp <= lastReadTimestamp
                const isRead = messageDate <= lastReadDate;
                nextState[messageId] = isRead;
                if (!isRead) {
                  console.log(`messageReadStore: Message ${messageId} marked as UNREAD (msg: ${messageDate.toISOString()}, lastRead: ${lastReadDate.toISOString()})`);
                }
              } else {
                // Invalid lastReadTS, fall back to marking as read
                console.warn(`messageReadStore: Invalid lastReadTS format: ${lastReadTS}, marking all as read`);
                nextState[messageId] = true;
              }
            } else {
              // No lastReadTS: mark all messages as read by default on initial sync
              // This prevents showing old messages as unread when we don't have provider's read marker yet
              nextState[messageId] = true;
              console.log(`messageReadStore: No lastReadTS for ${conversationId}, marking message ${messageId} as read by default`);
            }
          }
          hasChanged = true;
        }
      });

      // If we removed messages or if there are no messages left, we need to update the store
      if (removedCount > 0 || (messages.length === 0 && existingState && Object.keys(existingState).length > 0)) {
        hasChanged = true;
      }

      if (!hasChanged) {
        return state;
      }

      const updatedMap = {
        ...state.readByConversation,
        [conversationId]: nextState,
      };
      persistState(updatedMap);
      
      // Count only actual messages (exclude special keys like _lastReadTS)
      const unreadCount = Object.entries(nextState).filter(
        ([key, isRead]) => !key.startsWith("_") && !isRead
      ).length;
      const unreadMessageIds = Object.entries(nextState)
        .filter(([key, isRead]) => !key.startsWith("_") && !isRead)
        .map(([msgId]) => msgId);
      
      const totalMessages = Object.keys(nextState).filter(k => !k.startsWith("_")).length;
      console.log(`messageReadStore: syncConversation COMPLETE - ${conversationId}: ${totalMessages} messages, ${unreadCount} unread (lastReadTS: ${lastReadTS || 'none'})`);
      if (unreadCount > 0) {
        console.warn(`⚠️ messageReadStore: ${conversationId} has ${unreadCount} UNREAD messages:`, unreadMessageIds.slice(0, 5));
        // Log the actual state for debugging
        const unreadDetails = Object.entries(nextState)
          .filter(([key, isRead]) => !key.startsWith("_") && !isRead)
          .slice(0, 5)
          .map(([msgId, isRead]) => ({ msgId, isRead }));
        console.table(unreadDetails);
      }
      
      return { readByConversation: updatedMap };
    });
  },
  markAsRead: (conversationId, messageId) => {
    if (!conversationId || !messageId || messageId.startsWith("temp-")) {
      return;
    }
    set((state) => {
      const conversationState = state.readByConversation[conversationId];
      if (!conversationState || conversationState[messageId] === true) {
        return state;
      }
      const updatedConversation = {
        ...conversationState,
        [messageId]: true,
      };
      const updatedMap = {
        ...state.readByConversation,
        [conversationId]: updatedConversation,
      };
      persistStateDebounced(updatedMap);

      markMessageAsReadOnServer(conversationId, messageId).catch((error) => {
        console.error(`messageReadStore: Failed to send read receipt for message ${messageId}:`, error);
      });

      return { readByConversation: updatedMap };
    });
  },
  markMultipleAsRead: (conversationId, messageIds) => {
    if (!conversationId || messageIds.length === 0) return;
    set((state) => {
      const conversationState = state.readByConversation[conversationId];
      if (!conversationState) return state;

      const toMark = messageIds.filter((id) => conversationState[id] !== true && !id.startsWith("temp-"));
      if (toMark.length === 0) return state;

      const updatedConversation = { ...conversationState };
      toMark.forEach((id) => { updatedConversation[id] = true; });
      const updatedMap = { ...state.readByConversation, [conversationId]: updatedConversation };
      persistStateDebounced(updatedMap);

      toMark.forEach((id) => {
        markMessageAsReadOnServer(conversationId, id).catch((error) => {
          console.error(`messageReadStore: Failed to send read receipt for message ${id}:`, error);
        });
      });

      return { readByConversation: updatedMap };
    });
  },
  markAsReadByProtocolId: (conversationId, protocolMsgId) => {
    if (!conversationId || !protocolMsgId) {
      return;
    }
    set((state) => {
      const conversationState = state.readByConversation[conversationId];
      if (!conversationState) {
        return state;
      }
      // If message is already marked as read, don't do anything (including server call)
      if (conversationState[protocolMsgId] === true) {
        return state;
      }
      if (conversationState[protocolMsgId] === undefined) {
        // Still mark it as read in case it's a new message we haven't seen yet
      }
      const updatedConversation = {
        ...conversationState,
        [protocolMsgId]: true,
      };
      const updatedMap = {
        ...state.readByConversation,
        [conversationId]: updatedConversation,
      };
      persistState(updatedMap);

      // Send read receipt to server (only when actually marking as read for the first time)
      markMessageAsReadOnServer(conversationId, protocolMsgId).catch(() => {
        // Silently ignore errors for individual message receipts
        // Errors are already logged by the server call itself if needed
        });
      
      return { readByConversation: updatedMap };
    });
  },
  setLastReadTimestamp: (conversationId, lastReadTS) => {
    if (!conversationId || !lastReadTS) {
      return;
    }
    
    // Parse provider timestamp (format: "1502126650.000003")
    const lastReadTimestamp = parseFloat(lastReadTS);
    if (isNaN(lastReadTimestamp)) {
      console.warn(`messageReadStore: Invalid lastReadTS format: ${lastReadTS}`);
      return;
    }
    
    set((state) => {
      const conversationState = state.readByConversation[conversationId];
      if (!conversationState) {
        // No messages in this conversation yet, create empty state with lastReadTS
        const newState: ConversationReadState = {};
        (newState as any)["_lastReadTS"] = lastReadTS;
        const updatedMap = {
          ...state.readByConversation,
          [conversationId]: newState,
        };
        persistState(updatedMap);
        console.log(`messageReadStore: Created conversation state with lastReadTS for ${conversationId}: ${lastReadTS}`);
        return { readByConversation: updatedMap };
      }
      
      const updatedConversation = { ...conversationState };
      let hasChanged = false;
      
      // Store the lastReadTimestamp
      (updatedConversation as any)["_lastReadTS"] = lastReadTS;
      hasChanged = true;
      
      // IMPORTANT: Re-evaluate all messages' read state based on the new lastReadTS
      // This ensures that when we receive the lastReadTS from provider, we update the state
      const lastReadDate = new Date(lastReadTimestamp * 1000);
      
      Object.keys(updatedConversation).forEach((messageId) => {
        // Skip special keys like _lastReadTS
        if (messageId.startsWith("_")) {
          return;
        }
        
        // Parse message timestamp from the messageId
        // messageId format can be either "protocolMsgId" (e.g., "1766067993.591559")
        // or "message-{id}" or "ts-{timestamp}"
        let messageTimestamp: number | null = null;
        
        if (messageId.startsWith("message-") || messageId.startsWith("ts-")) {
          // Extract timestamp from ID
          const parts = messageId.split("-");
          if (parts.length === 2) {
            messageTimestamp = parseFloat(parts[1]);
          }
        } else {
          // Assume it's a provider protocol message ID (timestamp format)
          messageTimestamp = parseFloat(messageId);
        }
        
        if (messageTimestamp && !isNaN(messageTimestamp)) {
          // If messageId is in seconds (> 10 digits), convert to milliseconds
          const msgDate = messageTimestamp > 10000000000
            ? new Date(messageTimestamp)
            : new Date(messageTimestamp * 1000);
          
          // Mark as read if message timestamp <= lastReadTimestamp
          const wasRead = updatedConversation[messageId];
          const shouldBeRead = msgDate <= lastReadDate;
          
          if (wasRead !== shouldBeRead) {
            updatedConversation[messageId] = shouldBeRead;
            hasChanged = true;
          }
        }
      });
      
      if (!hasChanged) {
        return state;
      }
      
      const updatedMap = {
        ...state.readByConversation,
        [conversationId]: updatedConversation,
      };
      persistState(updatedMap);
      
      const unreadCount = Object.keys(updatedConversation).filter(
        (key) => !key.startsWith("_") && !updatedConversation[key]
      ).length;
      
      console.log(`messageReadStore: Updated lastReadTS for ${conversationId}: ${lastReadTS} (${lastReadDate.toISOString()}), ${unreadCount} unread messages`);
      
      return { readByConversation: updatedMap };
    });
  },
  markAsReadSilently: (conversationId, messageId) => {
    if (!conversationId || !messageId) return;
    set((state) => {
      const conversationState = state.readByConversation[conversationId];
      if (!conversationState || conversationState[messageId] === true) return state;
      const updatedConversation = { ...conversationState, [messageId]: true };
      const updatedMap = { ...state.readByConversation, [conversationId]: updatedConversation };
      persistStateDebounced(updatedMap);
      return { readByConversation: updatedMap };
    });
  },
  registerIncomingMessage: (message) => {
    const conversationId = message.protocolConvId;
    if (!conversationId) {
      console.warn("messageReadStore: registerIncomingMessage - no conversationId");
      return;
    }
    const messageId = getMessageIdentifier(message);
    if (!messageId) {
      console.warn("messageReadStore: registerIncomingMessage - no messageId");
      return;
    }
    // Thread replies are not tracked at the conversation level — they're only
    // considered read when the thread panel has been opened. Skip them here so
    // they don't trigger a conversation-level "mark as read" prematurely.
    const isThreadReply = message.threadId && message.threadId !== message.protocolMsgId;
    if (isThreadReply) return;

    // Check if conversation already has messages (to determine if this is a new message or existing history)
    set((state) => {
      const existingState = state.readByConversation[conversationId] || {};
      
      if (existingState[messageId] !== undefined) {
        return state;
      }
      
      // Use lastReadTS if available to determine read state
      const lastReadTS = (existingState as any)["_lastReadTS"] as string | undefined;
      let isRead: boolean;

      // Call messages are always marked as read (they don't count as unread messages)
      // They have their own badge indicator. Messages sent by the current user are always marked as read.
      const isCallMessage = message.callType && message.callType.trim() !== "";

      if (isCallMessage || message.isFromMe) {
        isRead = true;
      } else if (lastReadTS) {
        // Compare message timestamp with lastReadTS
        const lastReadTimestamp = parseFloat(lastReadTS);
        const messageDate = timeToDate(message.timestamp);
        if (!isNaN(lastReadTimestamp)) {
          const lastReadDate = new Date(lastReadTimestamp * 1000);
          isRead = messageDate <= lastReadDate;
        } else {
          isRead = true;
        }
      } else {
        // Fallback: unread if conversation already exists in store, otherwise read (history)
        const hasExisting = existingState && Object.keys(existingState).filter(k => !k.startsWith("_")).length > 0;
        isRead = !hasExisting;
      }

      const updatedConversation: ConversationReadState = {
        ...existingState,
        [messageId]: isRead,
      };
      const updatedMap = {
        ...state.readByConversation,
        [conversationId]: updatedConversation,
      };
      persistStateDebounced(updatedMap);
      return { readByConversation: updatedMap };
    });
  },
  registerBatchMessages: (messages: models.Message[]) => {
    if (messages.length === 0) return;
    set((state) => {
      const updatedReadByConversation = { ...state.readByConversation };
      for (const message of messages) {
        const conversationId = message.protocolConvId;
        if (!conversationId) continue;
        const messageId = getMessageIdentifier(message);
        if (!messageId) continue;

        // Thread replies are not tracked at the conversation level.
        const isThreadReply = message.threadId && message.threadId !== message.protocolMsgId;
        if (isThreadReply) continue;

        const existingState = updatedReadByConversation[conversationId] || {};
        if (existingState[messageId] !== undefined) continue;

        const lastReadTS = (existingState as any)["_lastReadTS"] as string | undefined;
        let isRead: boolean;
        const isCallMessage = message.callType && message.callType.trim() !== "";
        if (isCallMessage || message.isFromMe) {
          isRead = true;
        } else if (lastReadTS) {
          const lastReadTimestamp = parseFloat(lastReadTS);
          const messageDate = timeToDate(message.timestamp);
          isRead = !isNaN(lastReadTimestamp) ? messageDate <= new Date(lastReadTimestamp * 1000) : true;
        } else {
          const hasExisting = Object.keys(existingState).filter(k => !k.startsWith("_")).length > 0;
          isRead = !hasExisting;
        }

        updatedReadByConversation[conversationId] = {
          ...existingState,
          [messageId]: isRead,
        };
      }
      persistStateDebounced(updatedReadByConversation);
      return { readByConversation: updatedReadByConversation };
    });
  },
  clearConversation: (conversationId) => {
    if (!conversationId) {
      return;
    }
    set((state) => {
      if (!state.readByConversation[conversationId]) {
        return state;
      }
      const updatedMap = { ...state.readByConversation };
      delete updatedMap[conversationId];
      persistState(updatedMap);
      return { readByConversation: updatedMap };
    });
  },
  cleanupObsoleteMessages: (conversationId, validMessageIds) => {
    if (!conversationId) {
      return;
    }
    set((state) => {
      const existingState = state.readByConversation[conversationId];
      if (!existingState || Object.keys(existingState).length === 0) {
        return state;
      }
      
      const nextState: ConversationReadState = {};
      let hasChanged = false;
      let removedCount = 0;
      
      // Only keep messages that are in the valid set. Special keys (_lastReadTS etc.) are always preserved.
      Object.keys(existingState).forEach((messageId) => {
        if (messageId.startsWith("_") || validMessageIds.has(messageId)) {
          nextState[messageId] = existingState[messageId];
        } else {
          removedCount++;
          hasChanged = true;
        }
      });
      
      if (!hasChanged) {
        return state;
      }
      
      const updatedMap = {
        ...state.readByConversation,
        [conversationId]: nextState,
      };
      persistState(updatedMap);
      
      const unreadCount = Object.values(nextState).filter(r => !r).length;
      console.log(`messageReadStore: cleanupObsoleteMessages - conversationId: ${conversationId}, removed: ${removedCount}, remaining: ${Object.keys(nextState).length}, unread count: ${unreadCount}`);
      
      return { readByConversation: updatedMap };
    });
  },
  };
});

