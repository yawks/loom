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
    console.log("useMessageEvents: EventsOn function:", typeof EventsOn);
    
    // Check if runtime is available
    if (typeof window !== "undefined" && window.runtime) {
      console.log("useMessageEvents: window.runtime is available");
      console.log("useMessageEvents: window.runtime.listeners:", window.runtime.listeners);
    } else {
      console.error("useMessageEvents: window.runtime is NOT available!");
      return;
    }
    
    let isMounted = true;
    const unsubscribe = EventsOn("new-message", (eventJSON: string) => {
      if (!isMounted) {
        console.warn("useMessageEvents: Component unmounted, ignoring event");
        return;
      }
      console.log("useMessageEvents: Received new-message event:", eventJSON?.substring?.(0, 200) || eventJSON);
      
      // Verify the listener was registered
      if (typeof window !== "undefined" && window.runtime?.listeners) {
        console.log("useMessageEvents: Registered listeners for 'new-message':", window.runtime.listeners["new-message"]?.length || 0);
      }
      
      try {
        const event: { instanceId: string; message: models.Message } = JSON.parse(eventJSON);
        const message = event.message;
        console.log("useMessageEvents: Parsed message:", {
          id: message.protocolMsgId,
          conversationId: message.protocolConvId,
          body: message.body?.substring(0, 50),
          isFromMe: message.isFromMe,
          instanceId: event.instanceId,
        });

        registerIncomingMessage(message);
        console.log("useMessageEvents: Registered incoming message in read store");
        
        // Always ensure the conversation appears in recent list
        queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
        queryClient.refetchQueries({ queryKey: ["metaContacts"], type: "active" });
        console.log("useMessageEvents: Invalidated and refetched metaContacts");

        // Optimistically inject the message into the messages cache so it shows up instantly,
        // even if the conversation was not yet synced/loaded.
        const conversationId = message.protocolConvId;

        // Invalidate sort-order queries so the Recent tab reorders immediately
        // (covers both incoming and outgoing messages)
        queryClient.invalidateQueries({ queryKey: ["allLastMessageTimestamps"] });
        queryClient.invalidateQueries({ queryKey: ["allLastMessages"] });

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
        // Track whether the setQueryData updater actually added a genuinely new message.
        // Using an object so TypeScript's control-flow narrowing doesn't prevent mutation
        // inside the setQueryData callback from being visible outside.
        const state = { isNewMessage: false };
        if (conversationId) {

          queryClient.setQueryData<InfiniteData<models.Message[]>>(
            ["messages", conversationId],
            (oldData) => {
              // Initialize structure if absent
              const safeData: InfiniteData<models.Message[]> = oldData && Array.isArray(oldData.pages)
                ? {
                    pages: oldData.pages.map((p) => (Array.isArray(p) ? [...p] : [])),
                    pageParams: Array.isArray(oldData.pageParams) ? [...oldData.pageParams] : [],
                  }
                : { pages: [], pageParams: [] };

              // Ensure at least one page exists
              if (safeData.pages.length === 0) {
                safeData.pages.push([]);
              }

              const messageId = message.protocolMsgId || "";

              // Check if this exact message already exists in any page (avoid duplicates)
              const existsInCache = messageId && safeData.pages.some(page =>
                page.some(m => m.protocolMsgId === messageId)
              );
              if (existsInCache) {
                // state.isNewMessage stays false
                return safeData;
              }

              // For outgoing messages: replace the optimistic temp message if one exists
              // This prevents a double message (temp + real) from appearing simultaneously
              if (message.isFromMe) {
                let replacedTemp = false;
                const updatedPages = safeData.pages.map(page =>
                  page.map(m => {
                    if (!replacedTemp && m.isFromMe && typeof m.protocolMsgId === "string" && m.protocolMsgId.startsWith("temp-")) {
                      replacedTemp = true;
                      return { ...message, isPending: false, sendFailed: false } as unknown as models.Message;
                    }
                    return m;
                  })
                );
                if (replacedTemp) {
                  // state.isNewMessage stays false – the temp was already tracked by onSuccess
                  safeData.pages = updatedPages;
                  return safeData;
                }
              }

              // No temp message to replace and not already in cache: add to first page
              state.isNewMessage = true;
              const firstPage = safeData.pages[0] || [];
              safeData.pages[0] = [...firstPage, message];
              return safeData;
            }
          );

          // Only refetch from the server when we received a genuinely new message
          // (i.e. an incoming message appended to the cache). For outgoing messages that
          // replaced a temp placeholder, or for duplicates that were already in cache,
          // the local state is already correct – a refetch would just trigger unnecessary
          // re-renders that can cause a brief visual glitch on past messages.
          if (state.isNewMessage) {
            queryClient.invalidateQueries({ queryKey: ["messages", conversationId] });
            queryClient.refetchQueries({
              queryKey: ["messages", conversationId],
              type: "active",
            });
          }
        }

        // Invalidate sort-order queries so the Recent tab reorders immediately
        // (covers both incoming and outgoing messages)
        queryClient.invalidateQueries({ queryKey: ["allLastMessageTimestamps"] });
        queryClient.invalidateQueries({ queryKey: ["allLastMessages"] });

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

        // Safety-net: if the selected conversation uses a different query key than
        // message.protocolConvId (e.g. Slack DMs where linkedAccount.userId ≠ channel ID),
        // refetch any active ["messages", ...] query so the currently open chat always
        // reflects the new message. Only do this when the message was genuinely new.
        if (state.isNewMessage && selectedContact) {
          const selectedConversationId =
            selectedContact.linkedAccounts[0]?.conversationId ??
            selectedContact.linkedAccounts[0]?.userId;
          console.log("useMessageEvents: Selected contact conversation ID:", selectedConversationId, "Message conversation ID:", message.protocolConvId);

          if (selectedConversationId && selectedConversationId !== conversationId) {
            // IDs differ (e.g. Slack DM: user ID vs channel ID).  Invalidate and
            // refetch using the key the MessageList is actually subscribed to.
            console.log("useMessageEvents: ID mismatch – refetching selected conversation", selectedConversationId);
            queryClient.invalidateQueries({ queryKey: ["messages", selectedConversationId] });
            queryClient.refetchQueries({
              queryKey: ["messages", selectedConversationId],
              type: "active",
            });
          }
        }
      } catch (error) {
        console.error("useMessageEvents: Failed to parse message event:", error);
      }
    });
    
    // Verify the listener was registered
    if (typeof window !== "undefined" && window.runtime?.listeners) {
      console.log("useMessageEvents: After registration, listeners for 'new-message':", window.runtime.listeners["new-message"]?.length || 0);
    }

    return () => {
      console.log("useMessageEvents: Cleaning up event listener");
      isMounted = false;
      // Cleanup: unsubscribe from events when component unmounts
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, [queryClient, registerIncomingMessage, selectedContact]);

  // Listen for receipt events (read/delivery confirmations)
  useEffect(() => {
    console.log("useMessageEvents: Setting up event listener for 'receipt'");
    console.log("useMessageEvents: EventsOn function for receipt:", typeof EventsOn);
    
    if (typeof window !== "undefined" && !window.runtime) {
      console.error("useMessageEvents: window.runtime is NOT available for receipt events!");
      return;
    }
    
    if (typeof window !== "undefined" && window.runtime?.listeners) {
      console.log("useMessageEvents: window.runtime.listeners available for receipt setup");
    }
    
    let isMounted = true;
    const unsubscribeReceipt = EventsOn("receipt", (receiptJSON: string) => {
      console.log("useMessageEvents: *** RECEIPT EVENT RECEIVED ***");
      console.log("useMessageEvents: Received receipt event (raw):", receiptJSON?.substring?.(0, 200) || receiptJSON);
      if (!isMounted) {
        console.warn("useMessageEvents: Component unmounted, ignoring receipt event");
        return;
      }
      
      try {
        const receipt: ReceiptEvent = JSON.parse(receiptJSON);
        console.log("useMessageEvents: Received receipt event:", {
          conversationId: receipt.conversationId,
          messageId: receipt.messageId,
          receiptType: receipt.receiptType,
          userId: receipt.userId,
        });

        // Handle both read and delivery receipts
        console.log("useMessageEvents: Processing receipt for message", receipt.messageId);
        console.log("useMessageEvents: Conversation ID:", receipt.conversationId);
        console.log("useMessageEvents: Receipt type:", receipt.receiptType);
        
        if (receipt.receiptType === "read") {
          markAsReadByProtocolId(receipt.conversationId, receipt.messageId);
        }

        // Update messages cache directly without refetching to avoid scroll
        if (selectedContact) {
          const conversationId =
            selectedContact.linkedAccounts[0]?.conversationId ??
            selectedContact.linkedAccounts[0]?.userId;
          if (receipt.conversationId === conversationId && conversationId) {
            // Update the message in the cache directly
            // Note: useInfiniteQuery uses InfiniteData structure { pages: [...], pageParams: [...] }
            queryClient.setQueryData<InfiniteData<models.Message[]>>(
              ["messages", conversationId],
              (oldData) => {
                if (!oldData) {
                  return { pages: [], pageParams: [] };
                }
                if (!oldData.pages || !Array.isArray(oldData.pages)) {
                  return oldData;
                }
                
                // Update each page
                const updatedPages = oldData.pages.map((page) => {
                  if (!Array.isArray(page)) return page;
                  
                  return page.map((msg) => {
                    // Find message by protocolMsgId
                    if (msg.protocolMsgId === receipt.messageId) {
                      // Check if receipt already exists
                      const existingReceipt = msg.receipts?.find(
                        (r) => r.userId === receipt.userId && r.receiptType === receipt.receiptType
                      );
                      
                      if (!existingReceipt) {
                        // Add new receipt - create a new MessageReceipt instance
                        const receiptTimestamp = new Date(receipt.timestamp * 1000);
                        const newReceipt = models.MessageReceipt.createFrom({
                          id: 0, // Will be set by backend
                          messageId: msg.id,
                          userId: receipt.userId,
                          receiptType: receipt.receiptType,
                          timestamp: receiptTimestamp.toISOString(),
                          createdAt: new Date().toISOString(),
                          updatedAt: new Date().toISOString(),
                        });
                        
                        return models.Message.createFrom({
                          ...msg,
                          receipts: [...(msg.receipts || []), newReceipt],
                        });
                      } else {
                        // Update existing receipt timestamp if newer
                        const receiptTimestamp = new Date(receipt.timestamp * 1000);
                        const existingTimestamp = new Date(String(existingReceipt.timestamp));
                        if (receiptTimestamp > existingTimestamp) {
                          const updatedReceipts = msg.receipts?.map((r) =>
                            r.userId === receipt.userId && r.receiptType === receipt.receiptType
                              ? models.MessageReceipt.createFrom({
                                  ...r,
                                  timestamp: receiptTimestamp.toISOString(),
                                })
                              : r
                          );
                          return models.Message.createFrom({
                            ...msg,
                            receipts: updatedReceipts,
                          });
                        }
                      }
                    }
                    return msg;
                  });
                });
                
                return {
                  ...oldData,
                  pages: updatedPages,
                };
              }
            );
            console.log("useMessageEvents: Updated messages cache for selected conversation");
          }
        }
        
        // Invalidate metaContacts to update unread counts
        queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
      } catch (error) {
        console.error("useMessageEvents: Failed to parse receipt event:", error);
      }
    });
    
    // Verify the listener was registered
    if (typeof window !== "undefined" && window.runtime?.listeners) {
      console.log("useMessageEvents: After registration, listeners for 'receipt':", window.runtime.listeners["receipt"]?.length || 0);
    }

    return () => {
      console.log("useMessageEvents: Cleaning up receipt event listener");
      isMounted = false;
      if (unsubscribeReceipt) {
        unsubscribeReceipt();
      }
    };
  }, [queryClient, markAsReadByProtocolId, selectedContact]);

  // Listen for reaction events
  useEffect(() => {
    if (typeof window !== "undefined" && !window.runtime) {
      return;
    }
    
    let isMounted = true;
    const unsubscribeReaction = EventsOn("reaction", (reactionJSON: string) => {
      console.log("useMessageEvents: *** REACTION EVENT RECEIVED ***");
      console.log("useMessageEvents: Received reaction event (raw):", reactionJSON?.substring?.(0, 200) || reactionJSON);
      if (!isMounted) {
        return;
      }
      
      try {
        const reaction: ReactionEvent = JSON.parse(reactionJSON);
        console.log("useMessageEvents: Parsed reaction event:", {
          conversationId: reaction.conversationId,
          messageId: reaction.messageId,
          userId: reaction.userId,
          emoji: reaction.emoji,
          added: reaction.added,
        });
        
        // Strip leading/trailing colons for emoji comparison.
        // The DB stores emojis without colons ("+1") but optimistic updates use ":+1:".
        const normalizeEmoji = (e: string) => e.replace(/^:/, "").replace(/:$/, "");
        const normalizedReactionEmoji = normalizeEmoji(reaction.emoji);

        // Update messages cache directly for all conversations, not just selected one
        // This ensures reactions are updated even if the conversation is not currently selected
        queryClient.setQueriesData<InfiniteData<models.Message[]>>(
          { queryKey: ["messages"] },
          (oldData) => {
            if (!oldData || !oldData.pages || !Array.isArray(oldData.pages)) {
              return oldData;
            }

            let found = false;
            const updatedPages = oldData.pages.map((page) => {
              if (!Array.isArray(page)) return page;

              return page.map((msg) => {
                // Match by message ID only — ConversationID can differ for DMs (U... vs D...)
                if (msg.protocolMsgId === reaction.messageId) {
                  found = true;
                  const currentReactions = msg.reactions || [];

                  if (reaction.added) {
                    // Add reaction if it doesn't exist (normalize emoji format for comparison)
                    const exists = currentReactions.some(
                      (r) => r.userId === reaction.userId && normalizeEmoji(r.emoji) === normalizedReactionEmoji
                    );
                    if (!exists) {
                      console.log("useMessageEvents: Adding reaction to message", reaction.messageId);
                      const reactionTimestamp = new Date(reaction.timestamp * 1000);
                      const newReaction = models.Reaction.createFrom({
                        id: 0,
                        messageId: msg.id,
                        userId: reaction.userId,
                        emoji: reaction.emoji,
                        createdAt: reactionTimestamp.toISOString(),
                        updatedAt: reactionTimestamp.toISOString(),
                      });
                      return models.Message.createFrom({
                        ...msg,
                        reactions: [...currentReactions, newReaction],
                      });
                    } else {
                      console.log("useMessageEvents: Reaction already exists for message", reaction.messageId);
                    }
                  } else {
                    // Remove reaction (normalize emoji format for comparison)
                    console.log("useMessageEvents: Removing reaction from message", reaction.messageId);
                    const filteredReactions = currentReactions.filter(
                      (r) => !(r.userId === reaction.userId && normalizeEmoji(r.emoji) === normalizedReactionEmoji)
                    );
                    return models.Message.createFrom({
                      ...msg,
                      reactions: filteredReactions,
                    });
                  }
                }
                return msg;
              });
            });

            if (!found) {
              console.log("useMessageEvents: Message not found in cache for reaction:", reaction.messageId, "in conversation:", reaction.conversationId);
            }

            return {
              ...oldData,
              pages: updatedPages,
            };
          }
        );
        
        // Also invalidate metaContacts to ensure unread counts are updated
        queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
      } catch (error) {
        console.error("useMessageEvents: Failed to parse reaction event:", error);
      }
    });
    
    return () => {
      isMounted = false;
      if (unsubscribeReaction) {
        unsubscribeReaction();
      }
    };
  }, [queryClient, selectedContact]);

  // Listen for typing events
  useEffect(() => {
    console.log("useMessageEvents: Setting up event listener for 'typing'");
    
    if (typeof window !== "undefined" && !window.runtime) {
      console.error("useMessageEvents: window.runtime is NOT available for typing events!");
      return;
    }
    
    let isMounted = true;
    const unsubscribeTyping = EventsOn("typing", async (typingJSON: string) => {
      console.log("useMessageEvents: *** TYPING EVENT RECEIVED ***");
      console.log("useMessageEvents: Received typing event (raw):", typingJSON?.substring?.(0, 200) || typingJSON);
      if (!isMounted) {
        console.warn("useMessageEvents: Component unmounted, ignoring typing event");
        return;
      }
      
      try {
        const typing: TypingEvent = JSON.parse(typingJSON);
        console.log("useMessageEvents: Received typing event:", {
          conversationId: typing.conversationId,
          userId: typing.userId,
          isTyping: typing.isTyping,
        });

        // Resolve LID to actual conversation ID
        // LID format: "176188215558395@lid", standard format: "33123456789@s.whatsapp.net"
        let resolvedConversationId = typing.conversationId;
        
        // If the conversation ID is a LID, ask the backend to resolve it
        if (typing.conversationId.includes("@lid")) {
          console.log("useMessageEvents: ConversationId is a LID, asking backend to resolve...");
          
          try {
            // Call the backend API to resolve the LID
            // Use dynamic access since ResolveLID may not be in TypeScript bindings
            const resolveLIDFn = window.go?.main?.App?.ResolveLID as ((lid: string) => Promise<string>) | undefined;
            if (resolveLIDFn && typeof resolveLIDFn === "function") {
              const resolved = await resolveLIDFn(typing.conversationId);
              if (resolved && resolved !== typing.conversationId) {
                resolvedConversationId = resolved;
                console.log("useMessageEvents: Backend resolved LID", typing.conversationId, "to", resolvedConversationId);
              } else {
                console.warn("useMessageEvents: Backend could not resolve LID", typing.conversationId);
                
                // Fallback: if userId is a phone number, use it
                if (typing.userId.includes("@s.whatsapp.net")) {
                  resolvedConversationId = typing.userId;
                  console.log("useMessageEvents: Using userId as conversation ID (fallback):", resolvedConversationId);
                } else {
                  console.warn("useMessageEvents: Could not resolve LID to any known conversation. This typing indicator will be ignored.");
                  return; // Don't process this event
                }
              }
            } else {
              console.error("useMessageEvents: ResolveLID API not available");
              return;
            }
          } catch (error) {
            console.error("useMessageEvents: Error calling ResolveLID:", error);
            
            // Fallback: if userId is a phone number, use it
            if (typing.userId.includes("@s.whatsapp.net")) {
              resolvedConversationId = typing.userId;
              console.log("useMessageEvents: Using userId as conversation ID (error fallback):", resolvedConversationId);
            } else {
              console.warn("useMessageEvents: Could not resolve LID and error occurred. This typing indicator will be ignored.");
              return; // Don't process this event
            }
          }
        }
        
        console.log("useMessageEvents: Final conversation ID:", resolvedConversationId);
        console.log("useMessageEvents: User name:", typing.userName);

        if (typing.isTyping) {
          setTyping(resolvedConversationId, typing.userId, typing.userName);
          console.log("useMessageEvents: Set typing for conversation", resolvedConversationId, "with userName", typing.userName);
        } else {
          setNotTyping(resolvedConversationId, typing.userId);
          console.log("useMessageEvents: Set not typing for conversation", resolvedConversationId);
        }
      } catch (error) {
        console.error("useMessageEvents: Failed to parse typing event:", error);
      }
    });
    
    // Verify the listener was registered
    if (typeof window !== "undefined" && window.runtime?.listeners) {
      console.log("useMessageEvents: After registration, listeners for 'typing':", window.runtime.listeners["typing"]?.length || 0);
    }

    return () => {
      console.log("useMessageEvents: Cleaning up typing event listener");
      isMounted = false;
      if (unsubscribeTyping) {
        unsubscribeTyping();
      }
    };
  }, [setTyping, setNotTyping]);

  // Listen for message deleted events
  useEffect(() => {
    if (typeof window !== "undefined" && !window.runtime) {
      return;
    }

    let isMounted = true;
    const unsubscribeDeleted = EventsOn("message-deleted", (deletedJSON: string) => {
      if (!isMounted) return;

      try {
        const { ConversationID, MessageID } = JSON.parse(deletedJSON);

        // Remove the message from all cached query pages
        queryClient.setQueriesData<InfiniteData<models.Message[]>>(
          { queryKey: ["messages"] },
          (oldData) => {
            if (!oldData || !Array.isArray(oldData.pages)) return oldData;
            return {
              ...oldData,
              pages: oldData.pages.map((page) => {
                if (!Array.isArray(page)) return page;
                return page.filter(
                  (msg) =>
                    !(msg.protocolMsgId === MessageID && msg.protocolConvId === ConversationID)
                );
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
      if (unsubscribeDeleted) {
        unsubscribeDeleted();
      }
    };
  }, [queryClient]);

  // Listen for conversation read status events (last_read timestamp from Slack)
  useEffect(() => {
    console.log("useMessageEvents: Setting up event listener for 'conversation-read-status'");
    
    if (typeof window !== "undefined" && !window.runtime) {
      console.error("useMessageEvents: window.runtime is NOT available for conversation-read-status events!");
      return;
    }
    
    let isMounted = true;
    const unsubscribeReadStatus = EventsOn("conversation-read-status", (readStatusJSON: string) => {
      if (!isMounted) {
        console.warn("useMessageEvents: Component unmounted, ignoring conversation-read-status event");
        return;
      }
      
      try {
        const readStatus: { instanceId: string; conversationId: string; lastReadTs: string } = JSON.parse(readStatusJSON);
        console.log("useMessageEvents: Received conversation-read-status event:", readStatus);
        
        if (readStatus.conversationId && readStatus.lastReadTs) {
          setLastReadTimestamp(readStatus.conversationId, readStatus.lastReadTs);
          console.log(`useMessageEvents: Set lastReadTs for conversation ${readStatus.conversationId}: ${readStatus.lastReadTs}`);
        }
      } catch (error) {
        console.error("useMessageEvents: Failed to parse conversation-read-status event:", error);
      }
    });
    
    return () => {
      console.log("useMessageEvents: Cleaning up conversation-read-status event listener");
      isMounted = false;
      if (unsubscribeReadStatus) {
        unsubscribeReadStatus();
      }
    };
  }, [setLastReadTimestamp]);
}

