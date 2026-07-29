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
import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Virtuoso, type VirtuosoHandle } from "react-virtuoso";
import { useQueryClient } from "@tanstack/react-query";

import { ChatInput } from "./ChatInput";
import { FileUploadModal } from "./FileUploadModal";
import { ForwardMessageModal } from "./ForwardMessageModal";
import type { InfiniteData } from "@tanstack/react-query";
import type { MessageHandlers } from "./MessageBubbleItem";
import { MessageBubbleItem } from "./MessageBubbleItem";
import { MessageHeader } from "./MessageHeader";
import { MessageIRCItem } from "./MessageIRCItem";
import { TypingIndicator } from "./TypingIndicator";
import { ChevronDown, Loader2 } from "lucide-react";
import { Progress } from "@/components/ui/progress";
import { cn } from "@/lib/utils";
import { getMessageDomId } from "@/lib/messageUtils";
import { models } from "../../wailsjs/go/models";
import { unicodeEmojiMap, unicodeToEmojiName } from "@/lib/emojiMap";


import { useAppStore } from "@/lib/store";
import { useFileUpload } from "@/hooks/useFileUpload";
import { useMessageData } from "@/hooks/useMessageData";
import { useMessageEdit } from "@/hooks/useMessageEdit";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useRenderCount } from "@/hooks/useRenderCount";
import { useTranslation } from "react-i18next";

// Module-level context so VirtuosoHeader can read isFetchingNextPage without
// being defined inside MessageList. All three Virtuoso sub-components live
// outside the component, which means VIRTUOSO_COMPONENTS is a true constant —
// it never changes between renders and Virtuoso never has to remount wrappers.
const VirtuosoFetchingContext = React.createContext(false);

const VirtuosoHeader = () => {
  const isFetchingNextPage = React.useContext(VirtuosoFetchingContext);
  const { t } = useTranslation();
  if (!isFetchingNextPage) return null;
  return (
    <div className="flex justify-center items-center h-16 w-full bg-muted/30">
      <div className="flex items-center gap-2">
        <div className="h-4 w-4 border-2 border-primary border-t-transparent rounded-full animate-spin" />
        <span className="text-sm text-muted-foreground">{t("loading")}</span>
      </div>
    </div>
  );
};

const VirtuosoFooter = () => <div className="h-4" />;

const VirtuosoItem = (props: React.ComponentPropsWithRef<"div">) => (
  <div
    {...props}
    style={{ ...props.style, overflowX: "clip", paddingLeft: "1rem", paddingRight: "1rem" }}
  />
);

const VIRTUOSO_COMPONENTS = {
  Header: VirtuosoHeader,
  Footer: VirtuosoFooter,
  Item: VirtuosoItem,
};

export function MessageList({
  selectedConversation,
}: {
  selectedConversation: models.MetaContact;
}) {
  useRenderCount("MessageList", { conversationId: selectedConversation?.id });

  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { toasts, showToast, closeToast } = useToast();

  const selectedProviderFilter = useAppStore((state) => state.selectedProviderFilter);

  // When a provider filter is active, prefer the linked account from that provider.
  // Falls back to [0] when no filter is set or the contact has a single account.
  const activeAccount = useMemo(() => {
    const accounts = selectedConversation.linkedAccounts;
    if (selectedProviderFilter && accounts.length > 1) {
      return accounts.find((a) => a.providerInstanceId === selectedProviderFilter) ?? accounts[0];
    }
    return accounts[0];
  }, [selectedConversation.linkedAccounts, selectedProviderFilter]);

  const conversationId =
    activeAccount?.conversationId ??
    activeAccount?.userId ??
    "";

  const providerInstanceId = activeAccount?.providerInstanceId;
  const protocol = activeAccount?.protocol;
  const isGroupFromProvider = !!activeAccount?.isGroup;

  // State
  const virtuosoRef = useRef<VirtuosoHandle>(null);
  const scrollInitializedRef = useRef<string>('');
  const atBottomRef = useRef(true);
  const [atBottom, setAtBottom] = useState(true);
  // True for ~600ms after a conversation opens. Used both as a ref (for the
  // scroll re-anchor logic in atBottomStateChange) and as state (to suppress
  // the scroll-to-bottom button during the measurement oscillation phase).
  const isStabilizingRef = useRef(false);
  const [isStabilizing, setIsStabilizing] = useState(false);
  // Direct reference to Virtuoso's scroller DOM element — used for instant
  // scrollTop corrections that bypass Virtuoso's animation system.
  const scrollerElementRef = useRef<HTMLElement | null>(null);
  const [hasWindowFocus, setHasWindowFocus] = useState<boolean>(() =>
    typeof document === "undefined" ? true : document.hasFocus()
  );
  const focusStateRef = useRef<boolean>(hasWindowFocus);
  const [separatorDismissed, setSeparatorDismissed] = useState(false);
  const [openActionsMessageId, setOpenActionsMessageId] = useState<string | null>(null);
  const [replyingToMessage, setReplyingToMessage] = useState<models.Message | null>(null);
  const [forwardingMessage, setForwardingMessage] = useState<models.Message | null>(null);
  const [forwardModalOpen, setForwardModalOpen] = useState(false);
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
    uploadState,
    handleDragEnter,
    handleDragLeave,
    handleDragOver,
    handleDrop,
    handleFileUpload,
    handleRetrySend,
    handleDeleteLocalMessage,
  } = useFileUpload(conversationId, showToast);

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
  const setSelectedThreadParentMessage = useAppStore((state) => state.setSelectedThreadParentMessage);
  const isTypingInInput = useAppStore((state) => state.isTypingInInput);
  const messageLayout = useAppStore((state) => state.messageLayout);
  const showConversationDetails = useAppStore((state) => state.showConversationDetails);
  const setShowConversationDetails = useAppStore((state) => state.setShowConversationDetails);
  const setSelectedAvatarUrl = useAppStore((state) => state.setSelectedAvatarUrl);

  const markMessageAsRead = useMessageReadStore((state) => state.markAsRead);
  const markMultipleAsRead = useMessageReadStore((state) => state.markMultipleAsRead);

  // IDs of all thread reply messages — excluded from the conversation-level read marking
  // because thread replies are only considered "read" when the thread panel has been shown.
  const threadReplyIds = useMemo(() => {
    const ids = new Set<string>();
    Object.values(threadsByParent).forEach((msgs) =>
      msgs.forEach((msg) => {
        ids.add(getMessageDomId(msg));
        if (msg.protocolMsgId) ids.add(msg.protocolMsgId);
      })
    );
    return ids;
  }, [threadsByParent]);

  // Effects
  useEffect(() => { setIsTypingInInput(false); }, [selectedConversation?.id, setIsTypingInInput]);
  useEffect(() => { setRevealedDeletedMessages(new Set()); }, [conversationId]);
  useEffect(() => { setSeparatorDismissed(false); }, [conversationId]);
  useEffect(() => { focusStateRef.current = hasWindowFocus; }, [hasWindowFocus]);

  useEffect(() => {
    const handleFocus = () => setHasWindowFocus(true);
    const handleBlur = () => setHasWindowFocus(false);
    window.addEventListener("focus", handleFocus);
    window.addEventListener("blur", handleBlur);
    return () => { window.removeEventListener("focus", handleFocus); window.removeEventListener("blur", handleBlur); };
  }, []);

  // Mark conversation as read — only for main messages, not thread replies.
  // Thread reply messages are only marked read when the thread panel is displayed.
  useEffect(() => {
    if (!conversationId) return;
    const unreadMessages = Object.entries(conversationReadState)
      .filter(([msgId, isRead]) => !isRead && !msgId.startsWith("_") && !threadReplyIds.has(msgId))
      .map(([msgId]) => msgId);
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
  }, [conversationId, mainMessages, markMessageAsRead, conversationReadState, selectedConversation, threadReplyIds]);

  // Snapshot the first unread message ID once per conversation (when messages first arrive).
  // A live useMemo on conversationReadState would recompute every time a message is
  // marked read (on every scroll via rangeChanged), causing all Virtuoso items to
  // re-render via commonItemProps and the "new messages" divider to disappear from
  // one item — changing its height and triggering scroll drift.
  // Using a plain ref avoids any extra renders; the value is stable until the user
  // switches to a different conversation.
  const firstUnreadSnapshotRef = useRef<{ convId: string; unreadId: string | null } | null>(null);
  if (mainMessages.length > 0 && firstUnreadSnapshotRef.current?.convId !== conversationId) {
    let firstUnread: string | null = null;
    for (const message of mainMessages) {
      const domId = getMessageDomId(message);
      if (conversationReadState[domId] === false) { firstUnread = domId; break; }
    }
    firstUnreadSnapshotRef.current = { convId: conversationId, unreadId: firstUnread };
  }
  const firstUnreadMessageId = firstUnreadSnapshotRef.current?.convId === conversationId
    ? (firstUnreadSnapshotRef.current.unreadId ?? null)
    : null;

  useEffect(() => {
    if (hasWindowFocus) { setSeparatorDismissed(false); return; }
    if (!firstUnreadMessageId || separatorDismissed) return;
    const timer = setTimeout(() => setSeparatorDismissed(true), 10000);
    return () => clearTimeout(timer);
  }, [firstUnreadMessageId, hasWindowFocus, separatorDismissed]);

  // Correct post-measurement scroll drift: Virtuoso positions based on estimated heights then
  // Scroll to bottom (or first unread) on conversation open, then defend against
  // the drift that happens as Virtuoso measures overscan items' actual heights.
  useEffect(() => {
    if (!conversationId || !mainMessages.length) return;
    if (scrollInitializedRef.current === conversationId) return;
    scrollInitializedRef.current = conversationId;
    console.log(`[Scroll] Init scroll for conv ${conversationId.slice(-8)}: ${mainMessages.length} msgs, firstUnread=${firstUnreadMessageId?.slice(-8) ?? "none"}`);

    // Only stabilize (auto-re-anchor) when we're targeting the bottom.
    // If there's a firstUnreadMessageId the user is intentionally NOT at the bottom.
    if (!firstUnreadMessageId) {
      isStabilizingRef.current = true;
      setIsStabilizing(true);
      setTimeout(() => { isStabilizingRef.current = false; setIsStabilizing(false); }, 600);
    }

    // Double rAF: first lets React commit, second lets Virtuoso measure item heights.
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        if (!virtuosoRef.current) return;
        if (firstUnreadMessageId) {
          const idx = mainMessages.findIndex((m) => getMessageDomId(m) === firstUnreadMessageId);
          if (idx >= 0) {
            console.log(`[Scroll] → scrollToIndex(${idx}) [first unread]`);
            virtuosoRef.current.scrollToIndex({ index: idx, align: 'start', behavior: 'auto' });
            return;
          }
        }
        console.log(`[Scroll] → scrollToIndex(${mainMessages.length - 1}) [last msg]`);
        virtuosoRef.current.scrollToIndex({ index: mainMessages.length - 1, align: 'end', behavior: 'auto' });
      });
    });
  }, [conversationId, mainMessages, firstUnreadMessageId]);

  // Re-anchor to bottom when a pending message is added or confirmed.
  // followOutput fires on item-count change but cannot account for height changes
  // that occur during initial measurement or during isPending→confirmed transition.
  const hasPendingMessage = useMemo(
    () => mainMessages.some((m) => (m as any).isPending),
    [mainMessages]
  );
  const prevHasPendingRef = useRef(false);
  useEffect(() => {
    const wasPending = prevHasPendingRef.current;
    prevHasPendingRef.current = hasPendingMessage;
    if (wasPending === hasPendingMessage) return;
    if (scrollInitializedRef.current !== conversationId) return;
    const lastIndex = mainMessages.length - 1;
    if (lastIndex < 0) return;
    // Double rAF: first lets React commit, second lets Virtuoso measure item heights.
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        virtuosoRef.current?.scrollToIndex({ index: lastIndex, align: 'end', behavior: 'auto' });
      });
    });
  }, [hasPendingMessage, conversationId, mainMessages.length]);

  // Handlers
  const handleStartReached = useCallback(() => {
    if (!hasNextPage || isFetchingNextPage) return;
    fetchNextPage();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);

  const handleToggleThreads = () => {
    if (showThreads) {
      setSelectedThreadId(null);
      setShowThreads(false);
    } else {
      setShowThreads(true);
    }
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

  const handleForwardClick = useCallback((message: models.Message) => {
    setForwardingMessage(message);
    setForwardModalOpen(true);
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
    const useNativeEmoji = protocol === "googlechat" || protocol === "googlemessages" || protocol === "whatsapp";

    // Helper to get canonical name without colons (e.g. "thumbsup")
    const getCleanName = (emojiStr: string): string => {
      const clean = emojiStr.startsWith(":") && emojiStr.endsWith(":") ? emojiStr.slice(1, -1) : emojiStr;
      const unicode = unicodeEmojiMap[clean] || clean;
      const name = unicodeToEmojiName(unicode);
      return name || clean;
    };

    const targetName = getCleanName(emoji);
    const emojiName = targetName;

    let apiEmoji: string;
    if (useNativeEmoji) {
      // Provider expects raw Unicode (e.g. Google Chat, WhatsApp)
      const clean = emoji.startsWith(":") && emoji.endsWith(":") ? emoji.slice(1, -1) : emoji;
      const resolvedUnicode = unicodeEmojiMap[clean];
      if (resolvedUnicode) {
        apiEmoji = resolvedUnicode;
      } else {
        apiEmoji = clean;
      }
    } else {
      // Non-native emoji providers expect the emoji name (without colons)
      apiEmoji = targetName;
    }

    const normalizedEmoji = `:${emojiName}:`;

    const hasReaction = messageReactions.some((r) => {
      return getCleanName(r.emoji) === targetName && r.userId === currentUserId;
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
                    return !(getCleanName(r.emoji) === targetName && r.userId === currentUserId);
                  })
                : [...(msg.reactions || []), models.Reaction.createFrom({ id: 0, messageId: msg.id, userId: currentUserId || "", emoji: normalizedEmoji, createdAt: new Date(), updatedAt: new Date() })];
              return models.Message.createFrom({ ...msg, reactions: updatedReactions });
            })
          ),
        };
      }
    );

    console.log("[Loom Debug] handleReaction parameters:", {
      emoji,
      targetName,
      apiEmoji,
      hasReaction,
      useNativeEmoji
    });

    try {
      if (hasReaction) await RemoveReaction(conversationId, protocolMsgId, apiEmoji);
      else await AddReaction(conversationId, protocolMsgId, apiEmoji);
    } catch (error) {
      console.error("Failed to handle reaction:", error);
      showToast(t("error"), "error");
      queryClient.invalidateQueries({ queryKey: ["messages", conversationId] });
      queryClient.refetchQueries({ queryKey: ["messages", conversationId] });
    }
  }, [conversationId, currentUserId, providerInstanceId, protocol, queryClient, t, showToast]);


  const handlers: MessageHandlers = useMemo(() => ({
    onToggleDeletedMessage: toggleDeletedMessage,
    onEditMessage: handleEditMessage,
    onDeleteClick: handleDeleteClick,
    onReplyClick: handleReplyClick,
    onForwardClick: handleForwardClick,
    onReaction: handleReaction,
    onRetrySend: handleRetrySend,
    onDeleteLocalMessage: handleDeleteLocalMessage,
    onSaveEdit: handleSaveEdit,
    onCancelEdit: handleCancelEdit,
    onThreadClick: (parentMsgId: string, message: models.Message) => { setSelectedThreadId(parentMsgId); setShowThreads(true); setSelectedThreadParentMessage(message); },
    onAvatarClick: handleAvatarClick,
    onNavigateToEdit: handleNavigateToEdit,
    setOpenActionsMessageId,
    showToast,
  }), [toggleDeletedMessage, handleEditMessage, handleDeleteClick, handleReplyClick, handleForwardClick, handleReaction, handleRetrySend, handleDeleteLocalMessage, handleSaveEdit, handleCancelEdit, handleAvatarClick, handleNavigateToEdit, setSelectedThreadId, setShowThreads, showToast]);

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
          avatarUrl={selectedConversation.avatarUrl}
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
          <div className={cn("message-list__scroll-wrapper relative flex-1 min-h-0 transition-opacity duration-200", showThreads && "opacity-20")}>
          <VirtuosoFetchingContext.Provider value={isFetchingNextPage}>
          <Virtuoso
            key={conversationId}
            ref={virtuosoRef}
            className="h-full scroll-area bg-background"
            style={{ paddingTop: "1rem", paddingBottom: "1rem" }}
            data={mainMessages}
            alignToBottom
            followOutput={(isAtBottom) => {
              const lastMsg = mainMessages.at(-1) as unknown as Record<string, unknown>;
              const result = lastMsg?.isFromMe === true ? "auto" : (isAtBottom ? "smooth" : false);
              if (result !== false) console.log(`[Scroll] followOutput → "${result}" (isAtBottom=${isAtBottom}, lastIsFromMe=${lastMsg?.isFromMe})`);
              return result;
            }}
            startReached={handleStartReached}
            atBottomStateChange={(isAtBottom) => {
              console.log(`[Scroll] atBottomStateChange → ${isAtBottom} stabilizing=${isStabilizingRef.current}`);
              atBottomRef.current = isAtBottom;
              setAtBottom(isAtBottom);
              // During the stabilization window, Virtuoso's overscan measurements
              // temporarily push us off the bottom. Correct via direct DOM scrollTop
              // (no Virtuoso animation → no visible flicker).
              if (!isAtBottom && isStabilizingRef.current && scrollerElementRef.current) {
                const el = scrollerElementRef.current;
                requestAnimationFrame(() => {
                  el.scrollTop = el.scrollHeight - el.clientHeight;
                });
              }
            }}
            scrollerRef={(ref) => {
              scrollerElementRef.current = ref instanceof HTMLElement ? ref : null;
              if (ref instanceof HTMLElement) {
                // Stop stabilization as soon as the user intentionally scrolls
                const stopStabilizing = () => { isStabilizingRef.current = false; setIsStabilizing(false); };
                ref.addEventListener("wheel", stopStabilizing, { passive: true });
                ref.addEventListener("touchstart", stopStabilizing, { passive: true });
              }
            }}
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
            components={VIRTUOSO_COMPONENTS}
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
          </VirtuosoFetchingContext.Provider>
          {!atBottom && !isStabilizing && (
            <button
              className="message-list__scroll-to-bottom absolute bottom-4 right-4 z-10 flex h-9 w-9 items-center justify-center rounded-full bg-primary text-primary-foreground shadow-md transition-opacity hover:opacity-90"
              title={t("scroll_to_bottom")}
              onClick={() => virtuosoRef.current?.scrollToIndex({ index: mainMessages.length - 1, behavior: "smooth" })}
            >
              <ChevronDown className="h-5 w-5" />
            </button>
          )}
          </div>
        )}
        <div className="shrink-0">
          {uploadState.isUploading && (
            <div className="mx-4 mb-2 p-2.5 rounded-lg border bg-background/95 backdrop-blur shadow-sm space-y-1.5 transition-all">
              <div className="flex items-center justify-between text-xs text-muted-foreground font-medium">
                <span className="flex items-center gap-2 min-w-0 truncate">
                  <Loader2 className="h-3.5 w-3.5 animate-spin text-primary shrink-0" />
                  <span className="truncate">
                    {uploadState.statusText === "compressing"
                      ? t("compressing_image")
                      : t("uploading_file_progress", {
                          current: uploadState.currentFileIndex,
                          total: uploadState.totalFiles,
                        })}
                  </span>
                </span>
                <span className="shrink-0 font-mono text-[11px] font-semibold ml-2">{uploadState.progressPercent}%</span>
              </div>
              <Progress value={uploadState.progressPercent} className="h-1.5" />
              {uploadState.currentFileName && (
                <p className="text-[11px] text-muted-foreground/70 truncate" title={uploadState.currentFileName}>
                  {uploadState.currentFileName}
                </p>
              )}
            </div>
          )}
          <TypingIndicator conversationId={conversationId} />
          <ChatInput
            key={`main-${conversationId}`}
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
          uploadState={uploadState}
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
        <ForwardMessageModal
          open={forwardModalOpen}
          onOpenChange={setForwardModalOpen}
          message={forwardingMessage}
          providerInstanceId={providerInstanceId}
        />
      </div>
    </>
  );
}
