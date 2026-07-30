import { EventsOn } from "../../wailsjs/runtime/runtime";
import type { InfiniteData } from "@tanstack/react-query";
import { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useEffect } from "react";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useQueryClient } from "@tanstack/react-query";
import { useTypingStore } from "@/lib/typingStore";
import { timeToDate } from "@/lib/utils";
import { emojiNameToUnicode, unicodeToEmojiName } from "@/lib/emojiMap";
import { addPendingMessage } from "@/lib/pendingMessages";

interface ReceiptEvent {
  instanceId: string;
  conversationId: string;
  messageId: string;
  receiptType: "delivery" | "read" | "self_read";
  userId: string;
  timestamp: number;
}

interface ReactionEvent {
  instanceId: string;
  conversationId: string;
  messageId: string;
  userId: string;
  emoji: string;
  added: boolean;
  timestamp: number;
}

interface TypingEvent {
  instanceId: string;
  conversationId: string;
  userId: string;
  userName: string;
  isTyping: boolean;
}

const messageTime = (message: models.Message): number =>
  timeToDate(message.timestamp).getTime();

const activityTime = (timestamp: unknown): number => {
  if (typeof timestamp === "number") {
    return timestamp > 10_000_000_000 ? timestamp : timestamp * 1000;
  }
  return timestamp ? timeToDate(timestamp as string).getTime() : 0;
};

const keepLatestMessage = (
  current: models.Message | null | undefined,
  candidate: models.Message
): models.Message => !current || messageTime(candidate) >= messageTime(current) ? candidate : current;

export function useMessageEvents() {
  const queryClient = useQueryClient();
  const selectedContact = useAppStore((state) => state.selectedContact);
  const registerIncomingMessage = useMessageReadStore(
    (state) => state.registerIncomingMessage
  );
  const registerBatchMessages = useMessageReadStore(
    (state) => state.registerBatchMessages
  );
  const markAsReadSilently = useMessageReadStore(
    (state) => state.markAsReadSilently
  );
  const setLastReadTimestamp = useMessageReadStore(
    (state) => state.setLastReadTimestamp
  );
  const removeMessage = useMessageReadStore((state) => state.removeMessage);
  const setTyping = useTypingStore((state) => state.setTyping);
  const setNotTyping = useTypingStore((state) => state.setNotTyping);

  useEffect(() => {
    console.log("useMessageEvents: Setting up event listener for 'new-message'");
    
    if (typeof window === "undefined" || !window.runtime) return;
    
    let isMounted = true;
    const unsubscribe = EventsOn("new-message", (eventJSON: string) => {
      if (!isMounted) return;
      console.log("useMessageEvents: Received new-message event");
      
      try {
        const event: { instanceId: string; message: models.Message } = JSON.parse(eventJSON);
        const message = event.message;

        if (!message) {
          console.error("useMessageEvents: Received null message in event", event);
          return;
        }

        const conversationId = message.protocolConvId;

        registerIncomingMessage(message);

        // Update last message in cache directly for immediate UI update
        // Note: ContactList.tsx handles allLastMessages/Timestamps/activeCalls/allMessageCounts
        // invalidation via its own new-message EventsOn handler with a 300ms debounce.
        // Doing it here too (without debounce) would cause redundant refetches during bulk sync.
        if (conversationId) {
          queryClient.setQueryData<Record<string, models.Message | null>>(["allLastMessages"], (old) => ({
            ...(old || {}),
            [conversationId]: keepLatestMessage(old?.[conversationId], message),
          }));
          queryClient.setQueryData<Record<string, any>>(["allLastMessageTimestamps"], (old) => ({
            ...(old || {}),
            [conversationId]: Math.max(
              activityTime(old?.[conversationId]),
              messageTime(message)
            ),
          }));
        }

        // Optimistically inject the message into the messages cache
        const state = { isNewMessage: false };
        if (conversationId) {
          // Register in pending store so any concurrent background refetch also
          // includes this message even if the DB write hasn't committed yet.
          addPendingMessage(conversationId, message);
          queryClient.setQueryData<InfiniteData<models.Message[]>>(
            ["messages", conversationId],
            (oldData) => {
              const safeData: InfiniteData<models.Message[]> = oldData && Array.isArray(oldData.pages)
                ? {
                    pages: oldData.pages.map((p) => (Array.isArray(p) ? [...p] : [])),
                    pageParams: Array.isArray(oldData.pageParams) ? [...oldData.pageParams] : [],
                  }
                : { pages: [[]], pageParams: [] };

              const messageId = message.protocolMsgId || "";

              // Check for duplicates
              const existsInCache = messageId && safeData.pages.some(page =>
                page.some(m => m.protocolMsgId === messageId)
              );
              if (existsInCache) {
                // The message is already in the cache but the backend may have just enriched
                // it (e.g. a file_share RTM event adding the real attachment URL after SendFile
                // stored a URL-less placeholder). Invalidate so the DB copy is fetched.
                queryClient.invalidateQueries({ queryKey: ["messages", conversationId] });
                return safeData;
              }

              // Handle outgoing messages replacing temp placeholders
              if (message.isFromMe) {
                let replacedTemp = false;
                const updatedPages = safeData.pages.map(page =>
                  page.map(m => {
                    if (!replacedTemp && m.isFromMe && m.protocolMsgId?.startsWith("temp-")) {
                      replacedTemp = true;
                      return { ...message, isPending: false, sendFailed: false } as unknown as models.Message;
                    }
                    return m;
                  })
                );
                if (replacedTemp) {
                  safeData.pages = updatedPages;
                  return safeData;
                }
              }

              state.isNewMessage = true;
              if (!safeData.pages[0]) safeData.pages[0] = [];
              safeData.pages[0] = [...safeData.pages[0], message];
              return safeData;
            }
          );

          if (state.isNewMessage) {
            queryClient.invalidateQueries({ queryKey: ["messages", conversationId] });
          }
        }
      } catch (error) {
        console.error("useMessageEvents: Failed to parse message event:", error);
      }
    });
    
    return () => {
      isMounted = false;
      if (unsubscribe) unsubscribe();
    };
  }, [queryClient, registerIncomingMessage]);

  // Listen for batch message events (emitted during incremental sync instead of N individual events)
  useEffect(() => {
    if (typeof window === "undefined" || !window.runtime) return;

    let isMounted = true;
    const unsubscribeBatch = EventsOn("new-messages-batch", (batchJSON: string) => {
      if (!isMounted) return;
      try {
        const batch: { instanceId: string; conversationId: string; messages: models.Message[] } = JSON.parse(batchJSON);
        if (!batch.messages?.length) return;

        registerBatchMessages(batch.messages);

        // Update last-message and timestamp caches in one pass per conversation
        const lastMessageByConv: Record<string, models.Message> = {};
        const lastTSByConv: Record<string, number> = {};
        for (const message of batch.messages) {
          const convId = message.protocolConvId;
          if (convId) {
            lastMessageByConv[convId] = keepLatestMessage(lastMessageByConv[convId], message);
            lastTSByConv[convId] = Math.max(lastTSByConv[convId] || 0, messageTime(message));
          }
        }
        queryClient.setQueryData<Record<string, models.Message | null>>(["allLastMessages"], (old) => {
          const updated = { ...(old || {}) };
          for (const [convId, message] of Object.entries(lastMessageByConv)) {
            updated[convId] = keepLatestMessage(updated[convId], message);
          }
          return updated;
        });
        queryClient.setQueryData<Record<string, unknown>>(["allLastMessageTimestamps"], (old) => {
          const updated = { ...(old || {}) };
          for (const [convId, timestamp] of Object.entries(lastTSByConv)) {
            updated[convId] = Math.max(activityTime(updated[convId]), timestamp);
          }
          return updated;
        });
      } catch (error) {
        console.error("useMessageEvents: Failed to parse batch message event:", error);
      }
    });

    return () => {
      isMounted = false;
      if (unsubscribeBatch) unsubscribeBatch();
    };
  }, [queryClient, registerBatchMessages]);

  // Listen for receipt events (read/delivery confirmations)
  useEffect(() => {
    if (typeof window === "undefined" || !window.runtime) return;
    
    let isMounted = true;
    const unsubscribeReceipt = EventsOn("receipt", (receiptJSON: string) => {
      if (!isMounted) return;
      
      try {
        const receipt: ReceiptEvent = JSON.parse(receiptJSON);
        if (receipt.receiptType === "self_read") {
          // Read on another device — mark locally without sending a receipt back.
          markAsReadSilently(receipt.conversationId, receipt.messageId);
        }

        if (selectedContact) {
          const conversationId = selectedContact.linkedAccounts[0]?.conversationId ?? selectedContact.linkedAccounts[0]?.userId;
          if (conversationId) {
            // WhatsApp can emit the receipt chat as a LID while Loom's selected
            // conversation uses the equivalent phone-number JID. The protocol
            // message ID is the stable identity, so update the selected cache
            // whenever that message is actually present in it.
            queryClient.setQueryData<InfiniteData<models.Message[]>>(
              ["messages", conversationId],
              (oldData) => {
                if (!oldData || !oldData.pages) return oldData;
                
                const updatedPages = oldData.pages.map((page) => {
                  if (!Array.isArray(page)) return page;
                  return page.map((msg) => {
                    if (msg.protocolMsgId === receipt.messageId) {
                      const existingReceipt = msg.receipts?.find(
                        (r) => r.userId === receipt.userId && r.receiptType === receipt.receiptType
                      );
                      
                      if (!existingReceipt) {
                        const receiptTimestamp = new Date(receipt.timestamp * 1000);
                        const newReceipt = models.MessageReceipt.createFrom({
                          id: 0,
                          messageId: msg.id,
                          userId: receipt.userId,
                          receiptType: receipt.receiptType,
                          timestamp: receiptTimestamp.toISOString(),
                          createdAt: new Date().toISOString(),
                          updatedAt: new Date().toISOString(),
                        });
                        return models.Message.createFrom({ ...msg, receipts: [...(msg.receipts || []), newReceipt] });
                      } else {
                        const receiptTimestamp = new Date(receipt.timestamp * 1000);
                        const existingTimestamp = new Date(String(existingReceipt.timestamp));
                        if (receiptTimestamp > existingTimestamp) {
                          const updatedReceipts = msg.receipts?.map((r) =>
                            r.userId === receipt.userId && r.receiptType === receipt.receiptType
                              ? models.MessageReceipt.createFrom({ ...r, timestamp: receiptTimestamp.toISOString() })
                              : r
                          );
                          return models.Message.createFrom({ ...msg, receipts: updatedReceipts });
                        }
                      }
                    }
                    return msg;
                  });
                });
                return { ...oldData, pages: updatedPages };
              }
            );
          }
        }
        // Note: Stop invalidating metaContacts here as it's expensive and rarely needed for a receipt
        // Note: Stop invalidating metaContacts here as it's expensive and rarely needed for a reaction
      } catch (error) {
        console.error("useMessageEvents: Failed to parse receipt event:", error);
      }
    });
    
    return () => {
      isMounted = false;
      if (unsubscribeReceipt) unsubscribeReceipt();
    };
  }, [queryClient, markAsReadSilently, selectedContact]);

  // Listen for reaction events
  useEffect(() => {
    if (typeof window === "undefined" || !window.runtime) return;
    
    let isMounted = true;
    const unsubscribeReaction = EventsOn("reaction", (reactionJSON: string) => {
      if (!isMounted) return;
      
      try {
        const reaction: ReactionEvent = JSON.parse(reactionJSON);
        const normalizeEmoji = (e: string) => {
          const clean = e.startsWith(":") && e.endsWith(":") ? e.slice(1, -1) : e.replace(/^:/, "").replace(/:$/, "");
          const unicode = emojiNameToUnicode(clean) || clean;
          const name = unicodeToEmojiName(unicode);
          return name || clean;
        };
        const normalizedReactionEmoji = normalizeEmoji(reaction.emoji);

        queryClient.setQueriesData<InfiniteData<models.Message[]>>(
          { queryKey: ["messages"] },
          (oldData) => {
            if (!oldData || !oldData.pages) return oldData;

            const updatedPages = oldData.pages.map((page) => {
              if (!Array.isArray(page)) return page;
              return page.map((msg) => {
                if (msg.protocolMsgId === reaction.messageId) {
                  const currentReactions = msg.reactions || [];
                  if (reaction.added) {
                    const exists = currentReactions.some(
                      (r) => r.userId === reaction.userId && normalizeEmoji(r.emoji) === normalizedReactionEmoji
                    );
                    if (!exists) {
                      const reactionTimestamp = new Date(reaction.timestamp * 1000);
                      const newReaction = models.Reaction.createFrom({
                        id: 0,
                        messageId: msg.id,
                        userId: reaction.userId,
                        emoji: reaction.emoji,
                        createdAt: reactionTimestamp.toISOString(),
                        updatedAt: reactionTimestamp.toISOString(),
                      });
                      return models.Message.createFrom({ ...msg, reactions: [...currentReactions, newReaction] });
                    }
                  } else {
                    const filteredReactions = currentReactions.filter(
                      (r) => !(r.userId === reaction.userId && normalizeEmoji(r.emoji) === normalizedReactionEmoji)
                    );
                    return models.Message.createFrom({ ...msg, reactions: filteredReactions });
                  }
                }
                return msg;
              });
            });
            return { ...oldData, pages: updatedPages };
          }
        );
        if (reaction.added && reaction.conversationId) {
          const reactionTime = reaction.timestamp * 1000;
          queryClient.setQueryData<Record<string, unknown>>(
            ["allLastMessageTimestamps"],
            (old) => ({
              ...(old || {}),
              [reaction.conversationId]: Math.max(
                activityTime(old?.[reaction.conversationId]),
                reactionTime
              ),
            })
          );
        } else {
          // Removing the latest reaction can reveal an older activity date.
          queryClient.invalidateQueries({ queryKey: ["allLastMessageTimestamps"] });
        }
        queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
      } catch (error) {
        console.error("useMessageEvents: Failed to parse reaction event:", error);
      }
    });
    
    return () => {
      isMounted = false;
      if (unsubscribeReaction) unsubscribeReaction();
    };
  }, [queryClient]);

  // Listen for typing events
  useEffect(() => {
    if (typeof window === "undefined" || !window.runtime) return;
    
    let isMounted = true;
    const unsubscribeTyping = EventsOn("typing", async (typingJSON: string) => {
      if (!isMounted) return;
      
      try {
        const typing: TypingEvent = JSON.parse(typingJSON);
        
        if (typing.isTyping) {
          setTyping(typing.conversationId, typing.userId, typing.userName);
        } else {
          setNotTyping(typing.conversationId, typing.userId);
        }
      } catch (error) {
        console.error("useMessageEvents: Failed to parse typing event:", error);
      }
    });
    
    return () => {
      isMounted = false;
      if (unsubscribeTyping) unsubscribeTyping();
    };
  }, [setTyping, setNotTyping]);

  // Listen for message deleted events
  useEffect(() => {
    if (typeof window === "undefined" || !window.runtime) return;

    let isMounted = true;
    const unsubscribeDeleted = EventsOn("message-deleted", (deletedJSON: string) => {
      if (!isMounted) return;
      try {
        const { conversationId, messageId } = JSON.parse(deletedJSON);
        removeMessage(conversationId, messageId);
        queryClient.setQueriesData<InfiniteData<models.Message[]>>(
          { queryKey: ["messages"] },
          (oldData) => {
            if (!oldData || !Array.isArray(oldData.pages)) return oldData;
            return {
              ...oldData,
              pages: oldData.pages.map((page) => {
                if (!Array.isArray(page)) return page;
                return page.filter((msg) => !(msg.protocolMsgId === messageId && msg.protocolConvId === conversationId));
              }),
            };
          }
        );
        queryClient.setQueryData<Record<string, models.Message | null>>(
          ["allLastMessages"],
          (old) => {
            if (!old || old[conversationId]?.protocolMsgId !== messageId) return old;
            return { ...old, [conversationId]: null };
          }
        );
        queryClient.invalidateQueries({ queryKey: ["allLastMessages"] });
        queryClient.invalidateQueries({ queryKey: ["allLastMessageTimestamps"] });
        queryClient.invalidateQueries({ queryKey: ["allMessageCounts"] });
      } catch (error) {
        console.error("useMessageEvents: Failed to parse message-deleted event:", error);
      }
    });

    return () => {
      isMounted = false;
      if (unsubscribeDeleted) unsubscribeDeleted();
    };
  }, [queryClient, removeMessage]);

  // Listen for conversation read status events
  useEffect(() => {
    if (typeof window === "undefined" || !window.runtime) return;
    
    let isMounted = true;
    const unsubscribeReadStatus = EventsOn("conversation-read-status", (readStatusJSON: string) => {
      if (!isMounted) return;
      try {
        const readStatus: { instanceId: string; conversationId: string; lastReadTs: string } = JSON.parse(readStatusJSON);
        if (readStatus.conversationId && readStatus.lastReadTs) {
          setLastReadTimestamp(readStatus.conversationId, readStatus.lastReadTs);
        }
      } catch (error) {
        console.error("useMessageEvents: Failed to parse conversation-read-status event:", error);
      }
    });
    
    return () => {
      isMounted = false;
      if (unsubscribeReadStatus) unsubscribeReadStatus();
    };
  }, [setLastReadTimestamp]);
}
