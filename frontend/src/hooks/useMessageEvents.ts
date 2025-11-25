import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";

export function useMessageEvents() {
  const queryClient = useQueryClient();
  const selectedContact = useAppStore((state) => state.selectedContact);

  useEffect(() => {
    const unsubscribe = EventsOn("new-message", (messageJSON: string) => {
      try {
        const message: models.Message = JSON.parse(messageJSON);
        
        // Update the cache for the conversation that received the message
        if (selectedContact) {
          const conversationId = selectedContact.linkedAccounts[0]?.userId;
          
          // Check if this message belongs to the currently selected conversation
          if (message.protocolConvId === conversationId) {
            // Invalidate and refetch messages for this conversation
            queryClient.invalidateQueries({
              queryKey: ["messages", selectedContact.id],
            });
            // Force a refetch to ensure the new message appears immediately
            queryClient.refetchQueries({
              queryKey: ["messages", selectedContact.id],
            });
          }
        }
      } catch (error) {
        console.error("Failed to parse message event:", error);
      }
    });

    return () => {
      // Cleanup: unsubscribe from events when component unmounts
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, [queryClient, selectedContact]);
}

