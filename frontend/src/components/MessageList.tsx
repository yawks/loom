import {
  AddReaction,
  DeleteMessage,
  RemoveReaction,
} from "../../wailsjs/go/main/App";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { ToastContainer, useToast } from "@/components/ui/toast";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Virtuoso, type VirtuosoHandle } from "react-virtuoso";
import { useQueryClient } from "@tanstack/react-query";

import { ChatInput } from "./ChatInput";
import { FileUploadModal } from "./FileUploadModal";
import type { InfiniteData } from "@tanstack/react-query";
import type { MessageHandlers } from "./MessageBubbleItem";
import { MessageBubbleItem } from "./MessageBubbleItem";
import { MessageHeader } from "./MessageHeader";
import { MessageIRCItem } from "./MessageIRCItem";
import { TypingIndicator } from "./TypingIndicator";
import { cn } from "@/lib/utils";
import { getMessageDomId } from "@/lib/messageUtils";
import { models } from "../../wailsjs/go/models";
import { unicodeToEmojiName } from "@/lib/emojiMap";

import { useAppStore } from "@/lib/store";
import { useFileUpload } from "@/hooks/useFileUpload";
import { useMessageData } from "@/hooks/useMessageData";
import { useMessageEdit } from "@/hooks/useMessageEdit";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useRenderCount } from "@/hooks/useRenderCount";
import { useTranslation } from "react-i18next";

export function MessageList({
  selectedConversation,
}: {
  selectedConversation: models.MetaContact;
}) {
  useRenderCount("MessageList", { conversationId: selectedConversation?.id });

  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { toasts, showToast, closeToast } = useToast();

  const conversationId =
    selectedConversation.linkedAccounts[0]?.conversationId ??
    selectedConversation.linkedAccounts[0]?.userId ??
    "";

  const providerInstanceId = selectedConversation.linkedAccounts[0]?.providerInstanceId;
  const protocol = selectedConversation.linkedAccounts[0]?.protocol;
  const isGroupFromProvider = !!selectedConversation.linkedAccounts[0]?.isGroup;

  // State
  const virtuosoRef = useRef<VirtuosoHandle>(null);
  const atBottomRef = useRef(true);
  const prevMainMessagesLengthRef = useRef(0);
  const [hasWindowFocus, setHasWindowFocus] = useState<boolean>(() =>
    typeof document === "undefined" ? true : document.hasFocus()
  );
  const focusStateRef = useRef<boolean>(hasWindowFocus);
  const [separatorDismissed, setSeparatorDismissed] = useState(false);
  const [openActionsMessageId, setOpenActionsMessageId] = useState<string | null>(null);
  const [replyingToMessage, setReplyingToMessage] = useState<models.Message | null>(null);
  const [revealedDeletedMessages, setRevealedDeletedMessages] = useState<Set<string>>(() => new Set());
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [messageToDelete, setMessageToDelete] = useState<{ conversationID: string; messageID: string } | null>(null);

  // Hooks
  const {
    messages,
    mainMessages,
    threadsByParent,
    isGroupConversation,
    currentUserId,
    participantNames,
    conversationReadState,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
    isLoading,
    isFetching,
    data,
  } = useMessageData(conversationId, isGroupFromProvider);

  const currentUserName = useMemo(() => {
    if (currentUserId && participantNames.get(currentUserId)) return participantNames.get(currentUserId);
    for (const msg of messages) {
      if (msg.isFromMe && msg.senderName) return msg.senderName;
    }
    return undefined;
  }, [currentUserId, participantNames, messages]);

  const currentUserAvatarUrl = useMemo(() => {
    for (const msg of messages) {
      if (msg.isFromMe && msg.senderAvatarUrl) return msg.senderAvatarUrl;
    }
    return undefined;
  }, [messages]);

  const {
    isDragging,
    isFileUploadModalOpen,
    setIsFileUploadModalOpen,
    pendingFiles,
    setPendingFiles,
    pendingFilePaths,
    setPendingFilePaths,
    handleDragEnter,
    handleDragLeave,
    handleDragOver,
    handleDrop,
    handleFileUpload,
    handleRetrySend,
    handleDeleteLocalMessage,
  } = useFileUpload(conversationId);

  const {
    editingMessageId,
    editingText,
    setEditingText,
    editingInputRef,
    handleEditMessage,
    handleSaveEdit,
    handleCancelEdit,
    handleNavigateToEdit,
  } = useMessageEdit({ messages, conversationId, showToast, t });

  // Store
  const setIsTypingInInput = useAppStore((state) => state.setIsTypingInInput);
  const showThreads = useAppStore((state) => state.showThreads);
  const setShowThreads = useAppStore((state) => state.setShowThreads);
  const setSelectedThreadId = useAppStore((state) => state.setSelectedThreadId);
  const isTypingInInput = useAppStore((state) => state.isTypingInInput);
  const messageLayout = useAppStore((state) => state.messageLayout);
  const showConversationDetails = useAppStore((state) => state.showConversationDetails);
  const setShowConversationDetails = useAppStore((state) => state.setShowConversationDetails);
  const setSelectedAvatarUrl = useAppStore((state) => state.setSelectedAvatarUrl);

  const markMessageAsRead = useMessageReadStore((state) => state.markAsRead);
  const markMultipleAsRead = useMessageReadStore((state) => state.markMultipleAsRead);

  // Effects
  useEffect(() => { setIsTypingInInput(false); }, [selectedConversation?.id, setIsTypingInInput]);
  useEffect(() => { setRevealedDeletedMessages(new Set()); }, [conversationId]);
  useEffect(() => { setSeparatorDismissed(false); }, [conversationId]);
  useEffect(() => { focusStateRef.current = hasWindowFocus; }, [hasWindowFocus]);

  // Scroll to bottom when our own optimistic message appears
  useEffect(() => {
    const prevLen = prevMainMessagesLengthRef.current;
    prevMainMessagesLengthRef.current = mainMessages.length;
    if (mainMessages.length > prevLen && mainMessages.length > 0) {
      const lastMsg = mainMessages.at(-1) as unknown as Record<string, unknown>;
      if (lastMsg?.isPending === true) {
        virtuosoRef.current?.scrollToIndex({ index: mainMessages.length - 1, behavior: "smooth" });
      }
    }
  }, [mainMessages]);

  useEffect(() => {
    const handleFocus = () => setHasWindowFocus(true);
    const handleBlur = () => setHasWindowFocus(false);
    window.addEventListener("focus", handleFocus);
    window.addEventListener("blur", handleBlur);
    return () => { window.removeEventListener("focus", handleFocus); window.removeEventListener("blur", handleBlur); };
  }, []);

  // Mark conversation as read
  useEffect(() => {
    if (!conversationId) return;
    const unreadMessages = Object.entries(conversationReadState).filter(([, isRead]) => !isRead).map(([msgId]) => msgId);
    if (unreadMessages.length === 0) return;

    const markConversationAsReadOnServer = async (convId: string): Promise<void> => {
      return new Promise((resolve, reject) => {
        if (typeof window === "undefined" || !window.go?.main?.App) { reject(new Error("Wails runtime not available")); return; }
        const fn = (window.go.main.App as Record<string, unknown>)["MarkConversationAsRead"] as ((id: string) => Promise<void>) | undefined;
        if (!fn || typeof fn !== "function") { reject(new Error("MarkConversationAsRead not available")); return; }
        fn(convId).then(resolve).catch(reject);
      });
    };

    markConversationAsReadOnServer(conversationId)
      .then(() => unreadMessages.forEach((msgId) => markMessageAsRead(conversationId, msgId)))
      .catch(() => unreadMessages.forEach((msgId) => markMessageAsRead(conversationId, msgId)));
  }, [conversationId, mainMessages, markMessageAsRead, conversationReadState, selectedConversation]);

  // Auto-dismiss "new messages" separator
  const firstUnreadMessageId = useMemo(() => {
    for (const message of mainMessages) {
      const domId = getMessageDomId(message);
      if (conversationReadState[domId] === false) return domId;
    }
    return null;
  }, [conversationReadState, mainMessages]);

  useEffect(() => {
    if (hasWindowFocus) { setSeparatorDismissed(false); return; }
    if (!firstUnreadMessageId || separatorDismissed) return;
    const timer = setTimeout(() => setSeparatorDismissed(true), 10000);
    return () => clearTimeout(timer);
  }, [firstUnreadMessageId, hasWindowFocus, separatorDismissed]);

  // Handlers
  const handleStartReached = useCallback(() => {
    if (!hasNextPage || isFetchingNextPage) return;
    fetchNextPage();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);

  const handleToggleThreads = () => {
    if (showThreads) setSelectedThreadId(null);
    setShowThreads(!showThreads);
  };

  const handleToggleDetails = () => setShowConversationDetails(!showConversationDetails);

  const handleAvatarClick = useCallback((avatarUrl: string | undefined, displayName?: string) => {
    const urlToShow = avatarUrl || (displayName ? `https://api.dicebear.com/7.x/initials/svg?seed=${encodeURIComponent(displayName)}` : null);
    if (urlToShow) setSelectedAvatarUrl(urlToShow);
  }, [setSelectedAvatarUrl]);

  const toggleDeletedMessage = useCallback((messageId: string) => {
    setRevealedDeletedMessages((prev) => {
      const next = new Set(prev);
      if (next.has(messageId)) next.delete(messageId); else next.add(messageId);
      return next;
    });
  }, []);

  const handleDeleteClick = useCallback((message: models.Message) => {
    setMessageToDelete({ conversationID: conversationId, messageID: message.protocolMsgId || getMessageDomId(message) });
    setDeleteConfirmOpen(true);
  }, [conversationId]);

  const handleReplyClick = useCallback((message: models.Message) => {
    setReplyingToMessage(message);
  }, []);

  const handleConfirmDelete = useCallback(async () => {
    if (!messageToDelete || typeof DeleteMessage !== "function") return;
    const { conversationID, messageID } = messageToDelete;
    try {
      await DeleteMessage(conversationID, messageID);
      setDeleteConfirmOpen(false);
      setMessageToDelete(null);
      queryClient.invalidateQueries({ queryKey: ["messages", conversationID] });
      queryClient.refetchQueries({ queryKey: ["messages", conversationID] });
    } catch (error) {
      console.error("Failed to delete message:", error);
      showToast(t("delete_failed"), "error");
    }
  }, [messageToDelete, queryClient, showToast, t]);

  const handleReaction = useCallback(async (message: models.Message, emoji: string) => {
    const protocolMsgId = message.protocolMsgId || getMessageDomId(message);
    const messageReactions = message.reactions || [];

    let emojiName: string;
    if (emoji.startsWith(":") && emoji.endsWith(":")) {
      emojiName = emoji.slice(1, -1);
    } else {
      const converted = unicodeToEmojiName(emoji);
      emojiName = converted || emoji;
    }

    const normalizedEmoji = emojiName.startsWith(":") ? emojiName : (emojiName.includes(":") ? emojiName : `:${emojiName}:`);

    const hasReaction = messageReactions.some((r) => {
      let re = r.emoji;
      if (!re.startsWith(":")) re = `:${re}`;
      if (!re.endsWith(":")) re = `${re}:`;
      return re === normalizedEmoji && r.userId === currentUserId;
    });

    queryClient.setQueryData<InfiniteData<models.Message[]>>(
      ["messages", conversationId],
      (oldData) => {
        if (!oldData) return oldData;
        return {
          ...oldData,
          pages: oldData.pages.map((page) =>
            page.map((msg) => {
              if (msg.protocolMsgId !== protocolMsgId && getMessageDomId(msg) !== protocolMsgId) return msg;
              const updatedReactions = hasReaction
                ? (msg.reactions || []).filter((r) => {
                    let re = r.emoji;
                    if (!re.startsWith(":")) re = `:${re}`;
                    if (!re.endsWith(":")) re = `${re}:`;
                    return !(re === normalizedEmoji && r.userId === currentUserId);
                  })
                : [...(msg.reactions || []), models.Reaction.createFrom({ id: 0, messageId: msg.id, userId: currentUserId || "", emoji: normalizedEmoji, createdAt: new Date(), updatedAt: new Date() })];
              return models.Message.createFrom({ ...msg, reactions: updatedReactions });
            })
          ),
        };
      }
    );

    try {
      if (hasReaction) await RemoveReaction(conversationId, protocolMsgId, emojiName);
      else await AddReaction(conversationId, protocolMsgId, emojiName);
    } catch (error) {
      console.error("Failed to handle reaction:", error);
      showToast(t("error"), "error");
      queryClient.invalidateQueries({ queryKey: ["messages", conversationId] });
      queryClient.refetchQueries({ queryKey: ["messages", conversationId] });
    }
  }, [conversationId, currentUserId, queryClient, t, showToast]);

  const handlers: MessageHandlers = useMemo(() => ({
    onToggleDeletedMessage: toggleDeletedMessage,
    onEditMessage: handleEditMessage,
    onDeleteClick: handleDeleteClick,
    onReplyClick: handleReplyClick,
    onReaction: handleReaction,
    onRetrySend: handleRetrySend,
    onDeleteLocalMessage: handleDeleteLocalMessage,
    onSaveEdit: handleSaveEdit,
    onCancelEdit: handleCancelEdit,
    onThreadClick: (parentMsgId: string) => { setSelectedThreadId(parentMsgId); setShowThreads(true); },
    onAvatarClick: handleAvatarClick,
    onNavigateToEdit: handleNavigateToEdit,
    setOpenActionsMessageId,
  }), [toggleDeletedMessage, handleEditMessage, handleDeleteClick, handleReplyClick, handleReaction, handleRetrySend, handleDeleteLocalMessage, handleSaveEdit, handleCancelEdit, handleAvatarClick, handleNavigateToEdit, setSelectedThreadId, setShowThreads]);

  const commonItemProps = {
    mainMessages,
    conversationId,
    providerInstanceId,
    protocol,
    isGroupConversation,
    conversationReadState,
    firstUnreadMessageId,
    isTypingInInput,
    separatorDismissed,
    revealedDeletedMessages,
    editingMessageId,
    editingText,
    setEditingText,
    openActionsMessageId,
    currentUserId,
    participantNames,
    threadsByParent,
    virtuosoRef,
    handlers,
  };

  return (
    <>
      <ToastContainer toasts={toasts} onClose={closeToast} />
      <div
        className={cn("flex flex-col h-full overflow-hidden transition-colors", isDragging && "bg-muted/50")}
        onDragEnter={handleDragEnter}
        onDragLeave={handleDragLeave}
        onDragOver={handleDragOver}
        onDrop={handleDrop}
      >
        <MessageHeader
          displayName={selectedConversation.displayName}
          linkedAccounts={selectedConversation.linkedAccounts}
          onToggleThreads={handleToggleThreads}
          onToggleDetails={handleToggleDetails}
        />
        {(isLoading || (isFetching && (!data?.pages || data.pages.length === 0 || messages.length === 0))) ? (
          <div className="flex-1 flex items-center justify-center bg-background">
            <div className="flex flex-col items-center gap-4">
              <div className="relative">
                <svg
                  className="w-16 h-16 text-primary"
                  fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
                  viewBox="0 0 24 24"
                  style={{ animation: "shimmer 2s ease-in-out infinite", filter: "drop-shadow(0 0 8px currentColor)" }}
                >
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path>
                  <polyline points="7 10 12 15 17 10"></polyline>
                  <line x1="12" y1="15" x2="12" y2="3"></line>
                </svg>
              </div>
              <p className="text-muted-foreground text-sm" style={{ animation: "shimmer 2s ease-in-out infinite" }}>
                {t("fetching_messages") || "Récupération des messages"}
              </p>
            </div>
          </div>
        ) : (
          <Virtuoso
            key={conversationId}
            ref={virtuosoRef}
            className="flex-1 min-h-0 scroll-area bg-background"
            style={{ paddingTop: "1rem", paddingBottom: "1rem" }}
            data={mainMessages}
            alignToBottom
            followOutput={(atBottom) => (atBottom ? "smooth" : false)}
            startReached={handleStartReached}
            atBottomStateChange={(atBottom) => { atBottomRef.current = atBottom; }}
            rangeChanged={({ startIndex, endIndex }) => {
              if (!focusStateRef.current || !conversationId) return;
              const ids: string[] = [];
              for (let i = startIndex; i <= endIndex; i++) {
                if (mainMessages[i]) ids.push(getMessageDomId(mainMessages[i]));
              }
              if (ids.length > 0) markMultipleAsRead(conversationId, ids);
            }}
            initialTopMostItemIndex={(() => {
              if (!firstUnreadMessageId) return Math.max(0, mainMessages.length - 1);
              const idx = mainMessages.findIndex((m) => getMessageDomId(m) === firstUnreadMessageId);
              return idx >= 0 ? idx : Math.max(0, mainMessages.length - 1);
            })()}
            overscan={600}
            components={{
              Header: () => isFetchingNextPage ? (
                <div className="flex justify-center items-center h-16 w-full bg-muted/30">
                  <div className="flex items-center gap-2">
                    <div className="h-4 w-4 border-2 border-primary border-t-transparent rounded-full animate-spin" />
                    <span className="text-sm text-muted-foreground">{t("loading")}</span>
                  </div>
                </div>
              ) : null,
              Footer: () => <div className="h-4" />,
              Item: (props) => <div {...props} style={{ ...props.style, overflowX: "clip", paddingLeft: "1rem", paddingRight: "1rem" }} />,
            }}
            itemContent={(index, message) => {
              if (messageLayout === "bubble") {
                return (
                  <MessageBubbleItem
                    key={message.protocolMsgId}
                    message={message}
                    index={index}
                    editingInputRef={editingInputRef}
                    {...commonItemProps}
                  />
                );
              }
              return (
                <MessageIRCItem
                  key={message.protocolMsgId}
                  message={message}
                  index={index}
                  messageLayout={messageLayout}
                  {...commonItemProps}
                />
              );
            }}
          />
        )}
        <div className="shrink-0">
          <TypingIndicator conversationId={conversationId} />
          <ChatInput
            onFileUploadRequest={(files, filePaths) => {
              setPendingFiles(files);
              setPendingFilePaths(filePaths || []);
              setIsFileUploadModalOpen(true);
            }}
            replyingToMessage={replyingToMessage}
            onCancelReply={() => setReplyingToMessage(null)}
            onNavigateToEdit={handleNavigateToEdit}
            currentUserName={currentUserName}
            currentUserAvatarUrl={currentUserAvatarUrl}
          />
        </div>
        <FileUploadModal
          open={isFileUploadModalOpen}
          onOpenChange={setIsFileUploadModalOpen}
          files={pendingFiles}
          filePaths={pendingFilePaths.length > 0 ? pendingFilePaths : undefined}
          onConfirm={handleFileUpload}
        />
        <AlertDialog open={deleteConfirmOpen} onOpenChange={setDeleteConfirmOpen}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{t("delete_message_title")}</AlertDialogTitle>
              <AlertDialogDescription>{t("delete_message_description")}</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel onClick={() => setDeleteConfirmOpen(false)}>{t("cancel")}</AlertDialogCancel>
              <AlertDialogAction onClick={handleConfirmDelete} className="bg-destructive text-destructive-foreground hover:bg-destructive/90">
                {t("delete_message")}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </div>
    </>
  );
}
