import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SendMessage } from "../../wailsjs/go/main/App";
import { useAppStore } from "@/lib/store";

export function ChatInput() {
  const { t } = useTranslation();
  const [message, setMessage] = useState("");
  const selectedContact = useAppStore((state) => state.selectedContact);
  const queryClient = useQueryClient();

  const sendMessageMutation = useMutation({
    mutationFn: async ({ conversationId, text }: { conversationId: string; text: string }) => {
      return await SendMessage(conversationId, text);
    },
    onSuccess: () => {
      // Invalidate and refetch messages after sending
      if (selectedContact) {
        queryClient.invalidateQueries({
          queryKey: ["messages", selectedContact.id],
        });
        // Force a refetch to ensure the new message appears
        queryClient.refetchQueries({
          queryKey: ["messages", selectedContact.id],
        });
      }
    },
    onError: () => {
      // If sending fails, also invalidate to ensure we have the latest state
      if (selectedContact) {
        queryClient.invalidateQueries({
          queryKey: ["messages", selectedContact.id],
        });
      }
    },
  });

  const handleSendMessage = async () => {
    if (message.trim() && selectedContact) {
      const text = message.trim();
      setMessage("");
      try {
        await sendMessageMutation.mutateAsync({
          conversationId: selectedContact.linkedAccounts[0].userId,
          text,
        });
      } catch (error) {
        // Error handling is done in onError
        console.error("Failed to send message:", error);
      }
    }
  };

  return (
    <div className="p-4 border-t flex items-center space-x-2">
      <Input
        value={message}
        onChange={(e) => setMessage(e.target.value)}
        placeholder={t("type_a_message")}
        onKeyDown={(e) => e.key === "Enter" && handleSendMessage()}
      />
      <Button onClick={handleSendMessage}>{t("send")}</Button>
    </div>
  );
}
