import { create } from "zustand";
import type { models } from "../../wailsjs/go/models";
import { timeToDate } from "./utils";
import { sameUserId } from "./userIdentity";

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

// Message history is paginated from SQLite. This store only tracks the recent
// IDs needed by mounted views and unread interactions.
const MAX_READ_STATE_MESSAGES_PER_CONVERSATION = 500;

const boundConversationReadState = (
  state: ConversationReadState
): ConversationReadState => {
  const keys = Object.keys(state);
  const messageKeys = keys.filter((key) => !key.startsWith("_"));
  if (messageKeys.length <= MAX_READ_STATE_MESSAGES_PER_CONVERSATION) {
    return state;
  }

  const keptMessageKeys = messageKeys.slice(
    -MAX_READ_STATE_MESSAGES_PER_CONVERSATION
  );
  const keptMessages = new Set(keptMessageKeys);
  const bounded: ConversationReadState = {};
  if (state._lastReadTS !== undefined) {
    bounded._lastReadTS = state._lastReadTS;
  }
  for (const key of keptMessageKeys) {
    bounded[key] = state[key];
    const marker = `_thread:${key}`;
    if (state[marker] !== undefined && keptMessages.has(key)) {
      bounded[marker] = state[marker];
    }
  }
  return bounded;
};

interface MessageReadStore {
  readByConversation: ReadStateByConversation;
  syncConversation: (conversationId: ConversationId, messages: models.Message[], currentUserId?: string, policy?: ReadPolicy) => void;
  setLastReadTimestamp: (conversationId: ConversationId, lastReadTS: string) => void;
  markAsRead: (conversationId: ConversationId, messageId: MessageId) => void;
  markMultipleAsRead: (conversationId: ConversationId, messageIds: MessageId[]) => void;
  markAsReadByProtocolId: (conversationId: ConversationId, protocolMsgId: string) => void;
  /** Mark a message as read locally without sending a receipt back to the server.
   *  Use this for provider-originated self-read events to avoid receipt loops. */
  markAsReadSilently: (conversationId: ConversationId, messageId: MessageId) => void;
  registerIncomingMessage: (message: models.Message) => void;
  registerBatchMessages: (messages: models.Message[], isHistorical?: boolean, forceRead?: boolean, forceUnread?: boolean) => void;
  removeMessage: (conversationId: ConversationId, messageId: MessageId) => void;
  clearConversation: (conversationId: ConversationId) => void;
  clearProvider: (providerInstanceId: string) => void;
  cleanupObsoleteMessages: (conversationId: ConversationId, validMessageIds: Set<string>) => void;
  seedMockUnread: (conversationId: ConversationId, messageIds: MessageId[]) => void;
}

export interface ReadPolicy {
  cursorAuthoritativeForNewMessages: boolean;
  ownActivityAdvancesBoundary: boolean;
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
      let changed = false;
      const bounded: ReadStateByConversation = {};
      for (const [conversationId, conversationState] of Object.entries(parsed)) {
        const boundedConversation = boundConversationReadState(conversationState);
        bounded[conversationId] = boundedConversation;
        if (boundedConversation !== conversationState) changed = true;
      }
      if (changed) {
        window.localStorage.setItem(STORAGE_KEY, JSON.stringify(bounded));
      }
      return bounded;
    }
  } catch (error) {
    console.warn("Failed to load message read state:", error);
  }
  return {};
};

const persistStateNow = (state: ReadStateByConversation) => {
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
const persistState = (state: ReadStateByConversation) => {
  // An immediate write supersedes any older debounced snapshot. Without this,
  // a pending mark-as-read write can restore stale data after a newer update.
  if (_persistTimer !== null) {
    clearTimeout(_persistTimer);
    _persistTimer = null;
  }
  _latestState = null;
  persistStateNow(state);
};
const persistStateDebounced = (state: ReadStateByConversation) => {
  _latestState = state;
  if (_persistTimer === null) {
    _persistTimer = setTimeout(() => {
      if (_latestState !== null) persistStateNow(_latestState);
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

const threadMarker = (messageId: MessageId) => `_thread:${messageId}`;
const isThreadReply = (message: models.Message) =>
  Boolean(
    message.threadId &&
    message.threadId.trim() !== "" &&
    message.threadId !== message.protocolMsgId
  );

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
    seedMockUnread: (conversationId, messageIds) => {
      const unreadState = Object.fromEntries(messageIds.map((messageId) => [messageId, false]));
      set((state) => {
        const updatedMap = {
          ...state.readByConversation,
          [conversationId]: unreadState,
        };
        persistState(updatedMap);
        return { readByConversation: updatedMap };
      });
    },
    syncConversation: (conversationId, messages, currentUserId, policy) => {
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

        // Some services expose own activity as an authoritative read-through
        // signal. The provider declares this behavior through its capabilities.
        let ownActivityReadThrough: number | undefined;
        if (policy?.ownActivityAdvancesBoundary) {
          messages.forEach((message) => {
            const hasOwnReaction = Boolean(currentUserId) &&
              (message.reactions ?? []).some((reaction) =>
                sameUserId(reaction.userId, currentUserId)
              );
            if (message.isFromMe || hasOwnReaction) {
              const timestamp = timeToDate(message.timestamp).getTime();
              if (Number.isFinite(timestamp)) {
                ownActivityReadThrough = Math.max(ownActivityReadThrough ?? timestamp, timestamp);
              }
            }
          });
        }

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

      // Replies are deliberately absent from the paginated main timeline. Keep
      // an unread reply (and its marker) until ThreadView explicitly reads it;
      // otherwise refreshing the channel would immediately erase its badge.
      if (existingState) {
        Object.keys(existingState).forEach((messageId) => {
          const markedUnreadThreadReply = !messageId.startsWith("_") &&
            existingState[messageId] === false &&
            existingState[threadMarker(messageId)] === true;
          const unreadThreadMarker = messageId.startsWith("_thread:") &&
            existingState[messageId.slice("_thread:".length)] === false;
          if (
            messageId === "_lastReadTS" ||
            unreadThreadMarker ||
            (!messageId.startsWith("_") && (existingMessageIds.has(messageId) || markedUnreadThreadReply))
          ) {
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

        const isReadThroughOwnActivity = ownActivityReadThrough !== undefined &&
          timeToDate(message.timestamp).getTime() <= ownActivityReadThrough;
        if (isReadThroughOwnActivity && nextState[messageId] !== true) {
          nextState[messageId] = true;
          hasChanged = true;
        }

        if (message.isFromMe && nextState[messageId] === false) {
          // Provider echoes can arrive before self identity is fully resolved.
          // Once the persisted message confirms ownership, it must not remain
          // an unread navigation target.
          nextState[messageId] = true;
          hasChanged = true;
        }

        if (nextState[messageId] === undefined) {
          const isCallMessage = message.callType && message.callType.trim() !== "";
          if (isCallMessage || message.isFromMe) {
            nextState[messageId] = true;
      } else {
            // Always check lastReadTS from provider to determine read state
            // This ensures we use provider's own read markers rather than guessing
            if (lastReadTS && policy?.cursorAuthoritativeForNewMessages !== false) {
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
                // With an existing local timeline, an unknown ID is recovered
                // activity and must not silently become read because a provider
                // cursor is malformed.
                console.warn(`messageReadStore: Invalid lastReadTS format: ${lastReadTS}`);
                nextState[messageId] = !hasExisting;
              }
            } else {
              // With no provider cursor, only the first local import is assumed
              // historical. Once the conversation already exists, IDs that
              // appear later are recovered incoming messages and stay unread.
              nextState[messageId] = !hasExisting;
              console.log(`messageReadStore: No lastReadTS for ${conversationId}, marking new message ${messageId} as ${hasExisting ? "unread" : "historical/read"}`);
            }
          }
          hasChanged = true;
        }
        if (isThreadReply(message)) {
          const marker = threadMarker(messageId);
          if (nextState[marker] !== true) {
            nextState[marker] = true;
            hasChanged = true;
          }
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

      // Read markers are monotonic. A provider can deliver an older marker a few
      // seconds after Loom has already marked the conversation as read.
      const previousLastReadTS = parseFloat(
        String((updatedConversation as any)["_lastReadTS"] ?? "")
      );
      const effectiveLastReadTimestamp =
        !isNaN(previousLastReadTS) && previousLastReadTS > lastReadTimestamp
          ? previousLastReadTS
          : lastReadTimestamp;
      const effectiveLastReadTS = String(effectiveLastReadTimestamp);
      if ((updatedConversation as any)["_lastReadTS"] !== effectiveLastReadTS) {
        (updatedConversation as any)["_lastReadTS"] = effectiveLastReadTS;
        hasChanged = true;
      }

      // IMPORTANT: Re-evaluate all messages' read state based on the new lastReadTS
      // This ensures that when we receive the lastReadTS from provider, we update the state
      const lastReadDate = new Date(effectiveLastReadTimestamp * 1000);

      Object.keys(updatedConversation).forEach((messageId) => {
        // Skip special keys like _lastReadTS
        if (messageId.startsWith("_")) {
          return;
        }
        // A channel/conversation cursor must not consume thread replies. They
        // remain unread until ThreadView explicitly marks them.
        if (updatedConversation[threadMarker(messageId)] === true) {
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
          // A read message must never become unread because of a delayed or
          // incomplete provider marker. New messages are classified when they
          // are registered; this pass only advances unread messages to read.
          const shouldBeRead = wasRead || msgDate <= lastReadDate;

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
      let resolvedConversationId = conversationId;
      let conversationState = state.readByConversation[resolvedConversationId];

      // A provider can report an aliased conversation key. Protocol message IDs
      // remain stable, so resolve within the same instance when necessary.
      if (!conversationState || conversationState[messageId] === undefined) {
        const separator = conversationId.indexOf("::");
        const instancePrefix = separator >= 0 ? conversationId.slice(0, separator + 2) : "";
        const matchingConversation = Object.entries(state.readByConversation).find(
          ([candidateId, candidateState]) =>
            (!instancePrefix || candidateId.startsWith(instancePrefix)) &&
            candidateState[messageId] !== undefined
        );
        if (matchingConversation) {
          [resolvedConversationId, conversationState] = matchingConversation;
        }
      }

      if (!conversationState || conversationState[messageId] === true) return state;
      const updatedConversation = { ...conversationState, [messageId]: true };
      const updatedMap = { ...state.readByConversation, [resolvedConversationId]: updatedConversation };
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
    // Check if conversation already has messages (to determine if this is a new message or existing history)
    set((state) => {
      const existingState = state.readByConversation[conversationId] || {};
      const marker = threadMarker(messageId);
      const needsThreadMarker =
        isThreadReply(message) && existingState[marker] !== true;
      const repairsOwnMessage = message.isFromMe && existingState[messageId] === false;

      if (existingState[messageId] !== undefined && !needsThreadMarker && !repairsOwnMessage) {
        return state;
      }

      let isRead = existingState[messageId];

      // Call messages are always marked as read (they don't count as unread messages)
      // They have their own badge indicator. Messages sent by the current user are always marked as read.
      const isCallMessage = message.callType && message.callType.trim() !== "";

      if (message.isFromMe && isRead === false) {
        // Repair an earlier provider echo that was temporarily classified as
        // incoming before the sender identity was resolved.
        isRead = true;
      } else if (isRead !== undefined) {
        // Preserve the existing read state when this pass only adds legacy
        // thread metadata.
      } else if (isCallMessage || message.isFromMe) {
        isRead = true;
      } else {
        // A live incoming event has not been consumed by Loom yet. Remote
        // conversation cursors are applied only while synchronizing history.
        isRead = false;
      }

      const updatedConversation: ConversationReadState = {
        ...existingState,
        [messageId]: isRead,
      };
      if (isThreadReply(message)) {
        updatedConversation[marker] = true;
      }
      const updatedMap = {
        ...state.readByConversation,
        [conversationId]: updatedConversation,
      };
      persistStateDebounced(updatedMap);
      return { readByConversation: updatedMap };
    });
  },
  registerBatchMessages: (messages: models.Message[], _isHistorical = false, forceRead = false, forceUnread = false) => {
    if (messages.length === 0) return;
    set((state) => {
      const updatedReadByConversation = { ...state.readByConversation };
      const changedConversations = new Map<ConversationId, ConversationReadState>();
      let hasChanges = false;

      for (const message of messages) {
        const conversationId = message.protocolConvId;
        if (!conversationId) continue;
        const messageId = getMessageIdentifier(message);
        if (!messageId) continue;

        // Clone a conversation at most once per incoming batch. Cloning it for
        // every message makes a 2,000-message sync quadratic and causes WebKit
        // to retain gigabytes of allocator pages after the temporary objects die.
        const existingState =
          changedConversations.get(conversationId) ??
          updatedReadByConversation[conversationId] ??
          {};
        const marker = threadMarker(messageId);
        const needsThreadMarker =
          isThreadReply(message) && existingState[marker] !== true;
        // A duplicate history batch is not proof that a message was read. In
        // A provider may deliver a live MessageEvent and repeat the
        // same message in a HistorySync seconds later. Only an explicit
        // provider read-through signal may overwrite an existing unread state.
        const repairsUnreadState =
          forceRead && existingState[messageId] === false;
        const repairsOwnMessage = message.isFromMe && existingState[messageId] === false;
        if (existingState[messageId] !== undefined && !needsThreadMarker && !repairsUnreadState && !repairsOwnMessage) continue;

        const lastReadTS = (existingState as any)["_lastReadTS"] as string | undefined;
        let isRead = existingState[messageId];
        const isCallMessage = message.callType && message.callType.trim() !== "";
        if (repairsUnreadState) {
          // An action by the current user proves this history prefix was seen
          // on another linked client. This updates local state only.
          isRead = true;
        } else if (forceUnread) {
          // Only an authoritative provider-side unread marker may turn a
          // recovered message into a new local unread item.
          isRead = false;
        } else if (message.isFromMe && isRead === false) {
          // Batch enrichment can be the first payload that reliably identifies
          // a provider echo as ours.
          isRead = true;
        } else if (isRead !== undefined) {
          // Keep the existing value when only adding the thread marker.
        } else if (isCallMessage || message.isFromMe) {
          isRead = true;
        } else if (lastReadTS) {
          const lastReadTimestamp = parseFloat(lastReadTS);
          const messageDate = timeToDate(message.timestamp);
          isRead = !isNaN(lastReadTimestamp) ? messageDate <= new Date(lastReadTimestamp * 1000) : true;
        } else {
          // A recovered batch is not evidence that the message is unread on
          // the native service. Providers with an authoritative unread signal
          // opt in through forceUnread; live MessageEvents remain unread.
          isRead = true;
        }

        let updatedConversation = changedConversations.get(conversationId);
        if (!updatedConversation) {
          updatedConversation = { ...existingState };
          changedConversations.set(conversationId, updatedConversation);
          updatedReadByConversation[conversationId] = updatedConversation;
        }
        updatedConversation[messageId] = isRead;
        if (isThreadReply(message)) {
          updatedConversation[marker] = true;
        }
        const boundedConversation =
          boundConversationReadState(updatedConversation);
        if (boundedConversation !== updatedConversation) {
          updatedConversation = boundedConversation;
          changedConversations.set(conversationId, updatedConversation);
          updatedReadByConversation[conversationId] = updatedConversation;
        }
        hasChanges = true;
      }

      if (!hasChanges) return state;
      persistStateDebounced(updatedReadByConversation);
      return { readByConversation: updatedReadByConversation };
    });
  },
  removeMessage: (conversationId, messageId) => {
    if (!conversationId || !messageId) return;
    set((state) => {
      const conversationState = state.readByConversation[conversationId];
      if (!conversationState || conversationState[messageId] === undefined) {
        return state;
      }
      const updatedConversation = { ...conversationState };
      delete updatedConversation[messageId];
      delete updatedConversation[threadMarker(messageId)];
      const updatedMap = {
        ...state.readByConversation,
        [conversationId]: updatedConversation,
      };
      persistState(updatedMap);
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
  clearProvider: (providerInstanceId) => {
    if (!providerInstanceId) return;
    const prefix = `${providerInstanceId}::`;
    set((state) => {
      const updatedMap = Object.fromEntries(
        Object.entries(state.readByConversation).filter(
          ([conversationId]) => !conversationId.startsWith(prefix)
        )
      );
      if (Object.keys(updatedMap).length === Object.keys(state.readByConversation).length) {
        return state;
      }
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

      // Thread replies are not part of the main-message page represented by
      // validMessageIds. Preserve unread replies until the thread is opened.
      Object.keys(existingState).forEach((messageId) => {
        const isValidThreadMarker =
          messageId.startsWith("_thread:") &&
          validMessageIds.has(messageId.slice("_thread:".length));
        const unreadThreadMarker = messageId.startsWith("_thread:") &&
          existingState[messageId.slice("_thread:".length)] === false;
        const markedUnreadThreadReply = !messageId.startsWith("_") &&
          existingState[messageId] === false &&
          existingState[threadMarker(messageId)] === true;
        if (
          messageId === "_lastReadTS" ||
          isValidThreadMarker ||
          unreadThreadMarker ||
          (!messageId.startsWith("_") && (validMessageIds.has(messageId) || markedUnreadThreadReply))
        ) {
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
