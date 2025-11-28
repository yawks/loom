import { create } from "zustand";
import { timeToDate } from "./utils";
import type { models } from "../../wailsjs/go/models";

// Extend Window interface to include Wails runtime
declare global {
  interface Window {
    go?: {
      main?: {
        App?: {
          MarkMessageAsRead?: (conversationID: string, messageID: string) => Promise<void>;
          [key: string]: any;
        };
      };
    };
  }
}

// Helper function to call MarkMessageAsRead via Wails runtime
// This will work even if bindings haven't been regenerated
const markMessageAsReadOnServer = (conversationID: string, messageID: string): Promise<void> => {
  return new Promise((resolve, reject) => {
    console.log(`markMessageAsReadOnServer: Attempting to send read receipt for message ${messageID} in conversation ${conversationID}`);
    if (typeof window === "undefined") {
      reject(new Error("Window is undefined"));
      return;
    }
    if (!window.go) {
      console.error("markMessageAsReadOnServer: window.go is not available");
      reject(new Error("Wails runtime not available (window.go is undefined)"));
      return;
    }
    if (!window.go.main) {
      console.error("markMessageAsReadOnServer: window.go.main is not available");
      reject(new Error("Wails runtime not available (window.go.main is undefined)"));
      return;
    }
    if (!window.go.main.App) {
      console.error("markMessageAsReadOnServer: window.go.main.App is not available");
      reject(new Error("Wails runtime not available (window.go.main.App is undefined)"));
      return;
    }
    // Try to call the method even if it's not in TypeScript bindings
    // The runtime JavaScript should have access to all exported Go methods
    const markMessageAsReadFn = window.go.main.App.MarkMessageAsRead;
    if (!markMessageAsReadFn || typeof markMessageAsReadFn !== 'function') {
      console.error("markMessageAsReadOnServer: MarkMessageAsRead method is not available in Wails runtime");
      console.log("markMessageAsReadOnServer: Available methods:", Object.keys(window.go.main.App || {}));
      console.log("markMessageAsReadOnServer: Trying to access MarkMessageAsRead directly from runtime...");
      // Try accessing it directly from the Go runtime
      const directAccess = window.go?.main?.App?.MarkMessageAsRead;
      if (!directAccess || typeof directAccess !== 'function') {
        reject(new Error("MarkMessageAsRead not available in Wails runtime. Please restart the application to regenerate bindings."));
        return;
      }
      // Use direct access
      try {
        console.log(`markMessageAsReadOnServer: Calling MarkMessageAsRead via direct access (${conversationID}, ${messageID})`);
        directAccess(conversationID, messageID)
          .then(() => {
            console.log(`markMessageAsReadOnServer: Successfully called MarkMessageAsRead`);
            resolve();
          })
          .catch((error: Error) => {
            console.error(`markMessageAsReadOnServer: Error calling MarkMessageAsRead:`, error);
            reject(error);
          });
      } catch (error) {
        console.error(`markMessageAsReadOnServer: Exception calling MarkMessageAsRead:`, error);
        reject(error);
      }
      return;
    }
    try {
      console.log(`markMessageAsReadOnServer: Calling MarkMessageAsRead(${conversationID}, ${messageID})`);
      markMessageAsReadFn(conversationID, messageID)
        .then(() => {
          console.log(`markMessageAsReadOnServer: Successfully called MarkMessageAsRead`);
          resolve();
        })
        .catch((error: Error) => {
          console.error(`markMessageAsReadOnServer: Error calling MarkMessageAsRead:`, error);
          reject(error);
        });
    } catch (error) {
      console.error(`markMessageAsReadOnServer: Exception calling MarkMessageAsRead:`, error);
      reject(error);
    }
  });
};

type ConversationId = string;
type MessageId = string;
type ConversationReadState = Record<MessageId, boolean>;
type ReadStateByConversation = Record<ConversationId, ConversationReadState>;

interface MessageReadStore {
  readByConversation: ReadStateByConversation;
  syncConversation: (conversationId: ConversationId, messages: models.Message[]) => void;
  markAsRead: (conversationId: ConversationId, messageId: MessageId) => void;
  markAsReadByProtocolId: (conversationId: ConversationId, protocolMsgId: string) => void;
  registerIncomingMessage: (message: models.Message) => void;
  clearConversation: (conversationId: ConversationId) => void;
}

const STORAGE_KEY = "loom-message-read-state";

const canUseStorage = typeof window !== "undefined";

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

export const useMessageReadStore = create<MessageReadStore>((set) => ({
  readByConversation: loadPersistedState(),
  syncConversation: (conversationId, messages) => {
    if (!conversationId || messages.length === 0) {
      return;
    }
    set((state) => {
      const existingState = state.readByConversation[conversationId];
      const hasExisting =
        existingState && Object.keys(existingState).length > 0;
      const nextState: ConversationReadState = {
        ...(existingState || {}),
      };
      let hasChanged = false;

      messages.forEach((message) => {
        const messageId = getMessageIdentifier(message);
        if (!messageId) {
          return;
        }

        if (nextState[messageId] === undefined) {
          // If we already have a state for the conversation, new messages start as unread.
          // Otherwise we assume the existing history is read to avoid highlighting everything.
          // All messages (including isFromMe) are treated the same way
          nextState[messageId] = hasExisting ? false : true;
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
      return { readByConversation: updatedMap };
    });
  },
  markAsRead: (conversationId, messageId) => {
    if (!conversationId || !messageId) {
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
      persistState(updatedMap);
      
      // Send read receipt to server (messageId is protocolMsgId)
      markMessageAsReadOnServer(conversationId, messageId)
        .then(() => {
          console.log(`messageReadStore: Successfully sent read receipt for message ${messageId}`);
        })
        .catch((error) => {
          console.error(`messageReadStore: Failed to send read receipt for message ${messageId}:`, error);
        });
      
      return { readByConversation: updatedMap };
    });
  },
  markAsReadByProtocolId: (conversationId, protocolMsgId) => {
    if (!conversationId || !protocolMsgId) {
      console.warn("messageReadStore: markAsReadByProtocolId - missing conversationId or protocolMsgId");
      return;
    }
    console.log(`messageReadStore: markAsReadByProtocolId - conversationId: ${conversationId}, protocolMsgId: ${protocolMsgId}`);
    set((state) => {
      const conversationState = state.readByConversation[conversationId];
      if (!conversationState) {
        console.log(`messageReadStore: No conversation state found for ${conversationId}, available conversations:`, Object.keys(state.readByConversation));
        return state;
      }
      console.log(`messageReadStore: Conversation state found, checking for message ${protocolMsgId}`);
      console.log(`messageReadStore: Available message IDs in conversation:`, Object.keys(conversationState).slice(0, 10));
      if (conversationState[protocolMsgId] === true) {
        console.log(`messageReadStore: Message ${protocolMsgId} is already marked as read`);
        return state;
      }
      if (conversationState[protocolMsgId] === undefined) {
        console.log(`messageReadStore: WARNING - Message ${protocolMsgId} not found in conversation state`);
        console.log(`messageReadStore: This might mean the message ID in the receipt doesn't match the stored protocolMsgId`);
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
      const unreadCount = Object.values(updatedConversation).filter(r => !r).length;
      console.log(`messageReadStore: Marked message ${protocolMsgId} as read, unread count: ${unreadCount}`);
      
      // Send read receipt to server
      markMessageAsReadOnServer(conversationId, protocolMsgId)
        .then(() => {
          console.log(`messageReadStore: Successfully sent read receipt for message ${protocolMsgId}`);
        })
        .catch((error) => {
          console.error(`messageReadStore: Failed to send read receipt for message ${protocolMsgId}:`, error);
        });
      
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
    // Check if conversation already has messages (to determine if this is a new message or existing history)
    set((state) => {
      const existingState = state.readByConversation[conversationId] || {};
      const hasExisting = existingState && Object.keys(existingState).length > 0;
      
      if (existingState[messageId] !== undefined) {
        console.log(`messageReadStore: Message ${messageId} already exists in store`);
        return state;
      }
      
      // New messages are unread if conversation already exists, otherwise assume read (existing history)
      // All messages are treated the same way, regardless of isFromMe
      const isRead = hasExisting ? false : true;
      console.log(`messageReadStore: registerIncomingMessage - conversationId: ${conversationId}, messageId: ${messageId}, isFromMe: ${message.isFromMe}, hasExisting: ${hasExisting}, will be marked as read: ${isRead}`);
      
      const updatedConversation: ConversationReadState = {
        ...existingState,
        [messageId]: isRead,
      };
      const updatedMap = {
        ...state.readByConversation,
        [conversationId]: updatedConversation,
      };
      persistState(updatedMap);
      const unreadCount = Object.values(updatedConversation).filter(r => !r).length;
      console.log(`messageReadStore: Updated conversation ${conversationId}, unread count: ${unreadCount}`);
      console.log(`messageReadStore: Conversation state:`, Object.entries(updatedConversation).slice(0, 5).map(([msgId, isRead]) => `${msgId}: ${isRead ? 'read' : 'unread'}`));
      return { readByConversation: updatedMap };
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
}));

