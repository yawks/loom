import { EventsOn } from "../../wailsjs/runtime/runtime";
import type { InfiniteData } from "@tanstack/react-query";
import { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useEffect } from "react";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useTypingStore } from "@/lib/typingStore";
import { useQueryClient } from "@tanstack/react-query";

interface ReceiptEvent {
  ConversationID: string;
  MessageID: string;
  ReceiptType: "delivery" | "read";
  UserID: string;
  Timestamp: number;
}

interface ReactionEvent {
  ConversationID: string;
  MessageID: string;
  UserID: string;
  Emoji: string;
  Added: boolean;
  Timestamp: number;
}

interface TypingEvent {
  ConversationID: string;
  UserID: string;
  UserName: string;
  IsTyping: boolean;
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
    const unsubscribe = EventsOn("new-message", (messageJSON: string) => {
      if (!isMounted) {
        console.warn("useMessageEvents: Component unmounted, ignoring event");
        return;
      }
      console.log("useMessageEvents: Received new-message event:", messageJSON?.substring?.(0, 200) || messageJSON);
      
      // Verify the listener was registered
      if (typeof window !== "undefined" && window.runtime?.listeners) {
        console.log("useMessageEvents: Registered listeners for 'new-message':", window.runtime.listeners["new-message"]?.length || 0);
      }
      
      try {
        const message: models.Message = JSON.parse(messageJSON);
        console.log("useMessageEvents: Parsed message:", {
          id: message.protocolMsgId,
          conversationId: message.protocolConvId,
          body: message.body?.substring(0, 50),
          isFromMe: message.isFromMe,
        });

        registerIncomingMessage(message);
        console.log("useMessageEvents: Registered incoming message in read store");
        
        queryClient.invalidateQueries({ queryKey: ["metaContacts"] });
        queryClient.refetchQueries({ queryKey: ["metaContacts"], type: "active" });
        console.log("useMessageEvents: Invalidated and refetched metaContacts");
        
        // Update the cache for the conversation that received the message
        if (selectedContact) {
          const conversationId = selectedContact.linkedAccounts[0]?.userId;
          console.log("useMessageEvents: Selected contact conversation ID:", conversationId, "Message conversation ID:", message.protocolConvId);
          
          // Check if this message belongs to the currently selected conversation
          if (message.protocolConvId === conversationId && conversationId) {
            console.log("useMessageEvents: Message belongs to selected conversation, invalidating messages");
            // Invalidate and refetch messages for this conversation
            queryClient.invalidateQueries({
              queryKey: ["messages", conversationId],
            });
            // Force a refetch to ensure the new message appears immediately
            queryClient.refetchQueries({
              queryKey: ["messages", conversationId],
            });
            console.log("useMessageEvents: Invalidated and refetched messages for selected conversation");
          }
        } else {
          console.log("useMessageEvents: No selected contact, skipping message list update");
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
          conversationId: receipt.ConversationID,
          messageId: receipt.MessageID,
          receiptType: receipt.ReceiptType,
          userId: receipt.UserID,
        });

        // Handle both read and delivery receipts
        console.log("useMessageEvents: Processing receipt for message", receipt.MessageID);
        console.log("useMessageEvents: Conversation ID:", receipt.ConversationID);
        console.log("useMessageEvents: Receipt type:", receipt.ReceiptType);
        
        if (receipt.ReceiptType === "read") {
          markAsReadByProtocolId(receipt.ConversationID, receipt.MessageID);
        }

        // Update messages cache directly without refetching to avoid scroll
        if (selectedContact) {
          const conversationId = selectedContact.linkedAccounts[0]?.userId;
          if (receipt.ConversationID === conversationId && conversationId) {
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
                    if (msg.protocolMsgId === receipt.MessageID) {
                      // Check if receipt already exists
                      const existingReceipt = msg.receipts?.find(
                        (r) => r.userId === receipt.UserID && r.receiptType === receipt.ReceiptType
                      );
                      
                      if (!existingReceipt) {
                        // Add new receipt - create a new MessageReceipt instance
                        const receiptTimestamp = new Date(receipt.Timestamp * 1000);
                        const newReceipt = models.MessageReceipt.createFrom({
                          id: 0, // Will be set by backend
                          messageId: msg.id,
                          userId: receipt.UserID,
                          receiptType: receipt.ReceiptType,
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
                        const receiptTimestamp = new Date(receipt.Timestamp * 1000);
                        const existingTimestamp = new Date(String(existingReceipt.timestamp));
                        if (receiptTimestamp > existingTimestamp) {
                          const updatedReceipts = msg.receipts?.map((r) =>
                            r.userId === receipt.UserID && r.receiptType === receipt.ReceiptType
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
          conversationId: reaction.ConversationID,
          messageId: reaction.MessageID,
          userId: reaction.UserID,
          emoji: reaction.Emoji,
          added: reaction.Added,
        });
        
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
                if (msg.protocolMsgId === reaction.MessageID && msg.protocolConvId === reaction.ConversationID) {
                  found = true;
                  const currentReactions = msg.reactions || [];
                  
                  if (reaction.Added) {
                    // Add reaction if it doesn't exist
                    const exists = currentReactions.some(
                      (r) => r.userId === reaction.UserID && r.emoji === reaction.Emoji
                    );
                    if (!exists) {
                      console.log("useMessageEvents: Adding reaction to message", reaction.MessageID);
                      const reactionTimestamp = new Date(reaction.Timestamp * 1000);
                      const newReaction = models.Reaction.createFrom({
                        id: 0,
                        messageId: msg.id,
                        userId: reaction.UserID,
                        emoji: reaction.Emoji,
                        createdAt: reactionTimestamp.toISOString(),
                        updatedAt: reactionTimestamp.toISOString(),
                      });
                      return models.Message.createFrom({
                        ...msg,
                        reactions: [...currentReactions, newReaction],
                      });
                    } else {
                      console.log("useMessageEvents: Reaction already exists for message", reaction.MessageID);
                    }
                  } else {
                    // Remove reaction
                    console.log("useMessageEvents: Removing reaction from message", reaction.MessageID);
                    const filteredReactions = currentReactions.filter(
                      (r) => !(r.userId === reaction.UserID && r.emoji === reaction.Emoji)
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
              console.log("useMessageEvents: Message not found in cache for reaction:", reaction.MessageID, "in conversation:", reaction.ConversationID);
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
          conversationId: typing.ConversationID,
          userId: typing.UserID,
          isTyping: typing.IsTyping,
        });

        // Resolve LID to actual conversation ID
        // LID format: "176188215558395@lid", standard format: "33123456789@s.whatsapp.net"
        let resolvedConversationId = typing.ConversationID;
        
        // If the conversation ID is a LID, ask the backend to resolve it
        if (typing.ConversationID.includes("@lid")) {
          console.log("useMessageEvents: ConversationID is a LID, asking backend to resolve...");
          
          try {
            // Call the backend API to resolve the LID
            if (window.go?.main?.App?.ResolveLID) {
              const resolved = await window.go.main.App.ResolveLID(typing.ConversationID);
              if (resolved && resolved !== typing.ConversationID) {
                resolvedConversationId = resolved;
                console.log("useMessageEvents: Backend resolved LID", typing.ConversationID, "to", resolvedConversationId);
              } else {
                console.warn("useMessageEvents: Backend could not resolve LID", typing.ConversationID);
                
                // Fallback: if UserID is a phone number, use it
                if (typing.UserID.includes("@s.whatsapp.net")) {
                  resolvedConversationId = typing.UserID;
                  console.log("useMessageEvents: Using UserID as conversation ID (fallback):", resolvedConversationId);
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
            
            // Fallback: if UserID is a phone number, use it
            if (typing.UserID.includes("@s.whatsapp.net")) {
              resolvedConversationId = typing.UserID;
              console.log("useMessageEvents: Using UserID as conversation ID (error fallback):", resolvedConversationId);
            } else {
              console.warn("useMessageEvents: Could not resolve LID and error occurred. This typing indicator will be ignored.");
              return; // Don't process this event
            }
          }
        }
        
        console.log("useMessageEvents: Final conversation ID:", resolvedConversationId);
        console.log("useMessageEvents: User name:", typing.UserName);

        if (typing.IsTyping) {
          setTyping(resolvedConversationId, typing.UserID, typing.UserName);
          console.log("useMessageEvents: Set typing for conversation", resolvedConversationId, "with userName", typing.UserName);
        } else {
          setNotTyping(resolvedConversationId, typing.UserID);
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
}

