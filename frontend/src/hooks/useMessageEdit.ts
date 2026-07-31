import { EditMessage } from "../../wailsjs/go/main/App";
import { useCallback, useRef, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent } from "react";

import type { InfiniteData } from "@tanstack/react-query";
import { getMessageDomId } from "@/lib/messageUtils";
import { models } from "../../wailsjs/go/models";
import { useQueryClient } from "@tanstack/react-query";

interface UseMessageEditParams {
  messages: models.Message[];
  conversationId: string;
  showToast: (message: string, type: "error" | "success") => void;
  t: (key: string) => string;
  focusComposer?: () => void;
}

export function useMessageEdit({ messages, conversationId, showToast, t, focusComposer }: UseMessageEditParams) {
  const queryClient = useQueryClient();
  const [editingMessageId, setEditingMessageId] = useState<string | null>(null);
  const [editingText, setEditingText] = useState("");
  const [originalEditText, setOriginalEditText] = useState("");
  const editingInputRef = useRef<HTMLInputElement>(null);
  const isNavigatingEditRef = useRef(false);

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
    setOriginalEditText("");
    setTimeout(() => focusComposer?.(), 0);
  }, [focusComposer]);

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
    setTimeout(() => focusComposer?.(), 0);

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
  }, [editingMessageId, editingText, originalEditText, conversationId, messages, queryClient, t, showToast, focusComposer]);

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
              setTimeout(() => (returnFocusToInput || focusComposer)?.(), 0);
              return;
            }
          }
        } else if (direction === "up") {
          // A provider refresh can replace the message object while keeping an
          // obsolete editing id for one render. Recover from that stale state.
          targetMessage = sentMessages[0];
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
    [messages, editingMessageId, handleEditMessage, focusComposer]
  );

  const handleEditKeyDown = useCallback((e: ReactKeyboardEvent<HTMLInputElement>) => {
    if (e.shiftKey || e.ctrlKey || e.metaKey || e.altKey) return;

    if (e.key === "ArrowUp" && (e.currentTarget.selectionStart === 0 || editingText.trim() === "")) {
      e.preventDefault();
      e.stopPropagation();
      isNavigatingEditRef.current = true;
      handleNavigateToEdit("up");
      setTimeout(() => { isNavigatingEditRef.current = false; }, 0);
    } else if (e.key === "ArrowDown" && (e.currentTarget.selectionStart === e.currentTarget.value.length || editingText.trim() === "")) {
      e.preventDefault();
      e.stopPropagation();
      isNavigatingEditRef.current = true;
      handleNavigateToEdit("down");
      setTimeout(() => { isNavigatingEditRef.current = false; }, 0);
    }
  }, [editingText, handleNavigateToEdit]);

  const handleEditBlur = useCallback((relatedTarget: EventTarget | null) => {
    if (isNavigatingEditRef.current) return;
    const target = relatedTarget as HTMLElement | null;
    if (!target || (!target.closest("button") && !target.closest('[role="button"]'))) {
      void handleSaveEdit(false);
    }
  }, [handleSaveEdit]);

  return {
    editingMessageId,
    editingText,
    setEditingText,
    editingInputRef,
    handleEditMessage,
    handleSaveEdit,
    handleCancelEdit,
    handleNavigateToEdit,
    handleEditKeyDown,
    handleEditBlur,
  };
}
