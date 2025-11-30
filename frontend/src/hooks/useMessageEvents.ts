import { EventsOn } from "../../wailsjs/runtime/runtime";
import type { InfiniteData } from "@tanstack/react-query";
import { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useEffect } from "react";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useQueryClient } from "@tanstack/react-query";

interface ReceiptEvent {
  ConversationID: string;
  MessageID: string;
  ReceiptType: "delivery" | "read";
  UserID: string;
  Timestamp: number;
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
}

