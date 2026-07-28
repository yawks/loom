import { EditMessage } from "../../wailsjs/go/main/App";
import { useCallback, useEffect, useRef, useState } from "react";

import type { InfiniteData } from "@tanstack/react-query";
import { getMessageDomId } from "@/lib/messageUtils";
import { models } from "../../wailsjs/go/models";
import { useQueryClient } from "@tanstack/react-query";

interface UseMessageEditParams {
  messages: models.Message[];
  conversationId: string;
  showToast: (message: string, type: "error" | "success") => void;
  t: (key: string) => string;
}

export function useMessageEdit({ messages, conversationId, showToast, t }: UseMessageEditParams) {
  const queryClient = useQueryClient();
  const [editingMessageId, setEditingMessageId] = useState<string | null>(null);
  const [editingText, setEditingText] = useState("");
  const [originalEditText, setOriginalEditText] = useState("");
  const editingInputRef = useRef<HTMLInputElement>(null);

  const handleEditMessage = useCallback((message: models.Message) => {
    const messageId = getMessageDomId(message);
    setEditingMessageId(messageId);
    const body = message.body || "";
    setEditingText(body);
    setOriginalEditText(body);
  }, []);

  const handleCancelEdit = useCallback(() => {
    setEditingMessageId(null);
    setEditingText("");
  }, []);

  const handleSaveEdit = useCallback(async (skipValidation = false) => {
    if (!editingMessageId || typeof EditMessage !== "function") return;
    if (!skipValidation && !editingText.trim()) return;

    const message = messages.find((msg) => getMessageDomId(msg) === editingMessageId);
    if (!message || !message.protocolMsgId) return;

    const newText = editingText.trim();
    const originalText = originalEditText;
    const messageId = message.protocolMsgId;

    setEditingMessageId(null);
    setEditingText("");
    setOriginalEditText("");

    // Reflect the edit immediately. The protocol call can take long enough for
    // a refetch issued beforehand to return the old message.
    queryClient.setQueryData<InfiniteData<models.Message[]>>(
      ["messages", conversationId],
      (oldData) => {
        if (!oldData || !Array.isArray(oldData.pages)) return oldData;
        return {
          ...oldData,
          pages: oldData.pages.map((page) =>
            Array.isArray(page)
              ? page.map((msg) =>
                msg.protocolMsgId === messageId
                  ? ({ ...msg, body: newText, isEdited: true } as models.Message)
                  : msg
              )
              : page
          ),
        };
      }
    );

    try {
      await EditMessage(conversationId, messageId, newText);
      // Fetch after the protocol operation so the optimistic copy is reconciled
      // with the persisted message and any provider-side normalization.
      await Promise.all([
        queryClient.refetchQueries({ queryKey: ["messages", conversationId] }),
        queryClient.refetchQueries({ queryKey: ["threads"] }),
      ]);
    } catch (error) {
      console.error("Failed to edit message:", error);
      queryClient.setQueryData<InfiniteData<models.Message[]>>(
        ["messages", conversationId],
        (oldData) => {
          if (!oldData || !Array.isArray(oldData.pages)) return oldData;
          const pages = oldData.pages.map((page) => {
            if (!Array.isArray(page)) return page as models.Message[];
            return (page as models.Message[]).map((msg) =>
              msg.protocolMsgId === messageId ? { ...msg, body: originalText, isEdited: message.isEdited } : msg
            );
          });
          return { ...oldData, pages: pages as models.Message[][] };
        }
      );
      showToast(t("edit_failed"), "error");
    }
  }, [editingMessageId, editingText, originalEditText, conversationId, messages, queryClient, t, showToast]);

  const handleNavigateToEdit = useCallback(
    (direction: "up" | "down", returnFocusToInput?: () => void) => {
      const sentMessages = messages
        .filter((msg) => msg.isFromMe && msg.body && msg.body.trim() !== "")
        .sort((a, b) => {
          const timeA = new Date(a.timestamp as unknown as string).getTime();
          const timeB = new Date(b.timestamp as unknown as string).getTime();
          return timeB - timeA; // newest first
        });

      if (sentMessages.length === 0) return;

      let targetMessage: models.Message | null = null;

      if (editingMessageId) {
        const currentIndex = sentMessages.findIndex((msg) => getMessageDomId(msg) === editingMessageId);

        if (currentIndex !== -1) {
          if (direction === "up") {
            if (currentIndex < sentMessages.length - 1) targetMessage = sentMessages[currentIndex + 1];
          } else {
            if (currentIndex > 0) {
              targetMessage = sentMessages[currentIndex - 1];
            } else {
              setEditingMessageId(null);
              setEditingText("");
              setOriginalEditText("");
              returnFocusToInput?.();
              return;
            }
          }
        }
      } else {
        if (direction === "up") targetMessage = sentMessages[0];
      }

      if (targetMessage) {
        handleEditMessage(targetMessage);
        const messageId = getMessageDomId(targetMessage);
        const messageElement = document.getElementById(messageId);
        if (messageElement) messageElement.scrollIntoView({ behavior: "smooth", block: "center" });
      }
    },
    [messages, editingMessageId, handleEditMessage]
  );

  // Attach keyboard listener to editing input (capture phase) for arrow navigation in bubble layout
  useEffect(() => {
    if (!editingMessageId) return;

    const timeoutId = setTimeout(() => {
      const inputElement = editingInputRef.current;
      if (!inputElement) return;

      const handleKeyDown = (e: Event) => {
        const keyboardEvent = e as globalThis.KeyboardEvent;
        if (keyboardEvent.shiftKey || keyboardEvent.ctrlKey || keyboardEvent.metaKey || keyboardEvent.altKey) return;

        if (keyboardEvent.key === "ArrowUp") {
          const input = keyboardEvent.target as HTMLInputElement;
          const currentText = input?.value || editingText;
          const canNavigate = input?.selectionStart === 0 || currentText.trim() === "";
          if (canNavigate) {
            keyboardEvent.preventDefault();
            keyboardEvent.stopPropagation();
            keyboardEvent.stopImmediatePropagation();
            handleNavigateToEdit("up");
          }
          return;
        }

        if (keyboardEvent.key === "ArrowDown") {
          const input = keyboardEvent.target as HTMLInputElement;
          const currentText = input?.value || editingText;
          const canNavigate = input?.selectionStart === input?.value?.length || currentText.trim() === "";
          if (canNavigate) {
            keyboardEvent.preventDefault();
            keyboardEvent.stopPropagation();
            keyboardEvent.stopImmediatePropagation();
            handleNavigateToEdit("down", () => {
              const chatInput = document.querySelector('textarea[placeholder*="message"], textarea[placeholder*="Message"]') as HTMLTextAreaElement;
              if (chatInput) setTimeout(() => chatInput.focus(), 0);
            });
          }
          return;
        }
      };

      inputElement.addEventListener("keydown", handleKeyDown, true);
      return () => inputElement.removeEventListener("keydown", handleKeyDown, true);
    }, 100);

    return () => clearTimeout(timeoutId);
  }, [editingMessageId, editingText, handleNavigateToEdit]);

  return {
    editingMessageId,
    editingText,
    setEditingText,
    editingInputRef,
    handleEditMessage,
    handleSaveEdit,
    handleCancelEdit,
    handleNavigateToEdit,
  };
}
