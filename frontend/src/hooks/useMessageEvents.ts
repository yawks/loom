import { EventsOn } from "../../wailsjs/runtime/runtime";
import type { InfiniteData } from "@tanstack/react-query";
import { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useEffect } from "react";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useQueryClient } from "@tanstack/react-query";
import { useTypingStore } from "@/lib/typingStore";
import { timeToDate } from "@/lib/utils";

interface ReceiptEvent {
  instanceId: string;
  conversationId: string;
  messageId: string;
  receiptType: "delivery" | "read";
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

export function useMessageEvents() {
  const queryClient = useQueryClient();
  const selectedContact = useAppStore((state) => state.selectedContact);
  const registerIncomingMessage = useMessageReadStore(
    (state) => state.registerIncomingMessage
  );
  const markAsReadByProtocolId = useMessageReadStore(
    (state) => state.markAsReadByProtocolId
  );
  const setLastReadTimestamp = useMessageReadStore(
    (state) => state.setLastReadTimestamp
  );
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

        // Invalidate sort-order queries
        // Note: Stop invalidating metaContacts here as it's expensive and rarely needed for a new message
        queryClient.invalidateQueries({ queryKey: ["allLastMessageTimestamps"] });
        queryClient.invalidateQueries({ queryKey: ["allLastMessages"] });
        queryClient.invalidateQueries({ queryKey: ["activeCalls"] });
        queryClient.invalidateQueries({ queryKey: ["allMessageCounts"] });

        // Update last message in cache directly for immediate UI update
        if (conversationId) {
          queryClient.setQueryData<Record<string, models.Message | null>>(["allLastMessages"], (old) => ({
            ...(old || {}),
            [conversationId]: message,
          }));
          queryClient.setQueryData<Record<string, any>>(["allLastMessageTimestamps"], (old) => ({
            ...(old || {}),
            [conversationId]: Math.floor(timeToDate(message.timestamp).getTime() / 1000),
          }));
        }

        // Optimistically inject the message into the messages cache
        const state = { isNewMessage: false };
        if (conversationId) {
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
              if (existsInCache) return safeData;

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

        // Ensure selected chat reflects the new message if IDs differ (e.g. DMs)
        if (state.isNewMessage && selectedContact) {
          const selectedConvId = selectedContact.linkedAccounts[0]?.conversationId ?? selectedContact.linkedAccounts[0]?.userId;
          if (selectedConvId && selectedConvId !== conversationId) {
            queryClient.invalidateQueries({ queryKey: ["messages", selectedConvId] });
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
  }, [queryClient, registerIncomingMessage, selectedContact]);

  // Listen for receipt events (read/delivery confirmations)
  useEffect(() => {
    if (typeof window === "undefined" || !window.runtime) return;
    
    let isMounted = true;
    const unsubscribeReceipt = EventsOn("receipt", (receiptJSON: string) => {
      if (!isMounted) return;
      
      try {
        const receipt: ReceiptEvent = JSON.parse(receiptJSON);
        if (receipt.receiptType === "read") {
          markAsReadByProtocolId(receipt.conversationId, receipt.messageId);
        }

        if (selectedContact) {
          const conversationId = selectedContact.linkedAccounts[0]?.conversationId ?? selectedContact.linkedAccounts[0]?.userId;
          if (receipt.conversationId === conversationId && conversationId) {
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
  }, [queryClient, markAsReadByProtocolId, selectedContact]);

  // Listen for reaction events
  useEffect(() => {
    if (typeof window === "undefined" || !window.runtime) return;
    
    let isMounted = true;
    const unsubscribeReaction = EventsOn("reaction", (reactionJSON: string) => {
      if (!isMounted) return;
      
      try {
        const reaction: ReactionEvent = JSON.parse(reactionJSON);
        const normalizeEmoji = (e: string) => e.replace(/^:/, "").replace(/:$/, "");
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
        let resolvedConvId = typing.conversationId;
        
        if (typing.conversationId.includes("@lid")) {
          const resolveLIDFn = window.go?.main?.App?.ResolveLID as ((lid: string) => Promise<string>) | undefined;
          if (resolveLIDFn) {
            const resolved = await resolveLIDFn(typing.conversationId);
            if (resolved && resolved !== typing.conversationId) {
              resolvedConvId = resolved;
            } else if (typing.userId.includes("@s.whatsapp.net")) {
              resolvedConvId = typing.userId;
            } else {
              return;
            }
          } else {
            return;
          }
        }
        
        if (typing.isTyping) {
          setTyping(resolvedConvId, typing.userId, typing.userName);
        } else {
          setNotTyping(resolvedConvId, typing.userId);
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
      } catch (error) {
        console.error("useMessageEvents: Failed to parse message-deleted event:", error);
      }
    });

    return () => {
      isMounted = false;
      if (unsubscribeDeleted) unsubscribeDeleted();
    };
  }, [queryClient]);

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
