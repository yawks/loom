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
import { ChevronDown, UploadCloud } from "lucide-react";
import { cn } from "@/lib/utils";
import { getMessageDomId } from "@/lib/messageUtils";
import { models } from "../../wailsjs/go/models";
import { normalizeReaction, reactionMatches } from "@/lib/reactionUtils";


import { useAppStore } from "@/lib/store";
import { usePresenceStore } from "@/lib/presenceStore";
import { useFileUpload } from "@/hooks/useFileUpload";
import { useMessageData } from "@/hooks/useMessageData";
import { useMessageEdit } from "@/hooks/useMessageEdit";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useRenderCount } from "@/hooks/useRenderCount";
import { useTranslation } from "react-i18next";

const VirtuosoFooter = () => <div className="h-4" />;

const VirtuosoItem = (props: React.ComponentPropsWithRef<"div">) => (
  <div
    {...props}
    style={{ ...props.style, overflowX: "clip", paddingLeft: "1rem", paddingRight: "1rem" }}
  />
);

const VIRTUOSO_COMPONENTS = {
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
  const messageSearchTargetId = useAppStore((state) => state.messageSearchTargetId);
  const setMessageSearchTargetId = useAppStore((state) => state.setMessageSearchTargetId);

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
  const capabilities = useAppStore((state) => state.capabilities);
  const isGroupFromProvider = !!activeAccount?.isGroup;

  // State
  const virtuosoRef = useRef<VirtuosoHandle>(null);
  const scrollInitializedRef = useRef<string>('');
  const atBottomRef = useRef(true);
  const [atBottom, setAtBottom] = useState(true);
  // Whether the list should remain anchored to the bottom while rendered
  // messages change height (images, audio players, link previews, etc.).
  // This is an intent, not a timer: it stays true until the user deliberately
  // moves away from the bottom.
  const isStabilizingRef = useRef(false);
  const [isStabilizing, setIsStabilizing] = useState(false);
  // Direct reference to Virtuoso's scroller DOM element — used for instant
  // scrollTop corrections that bypass Virtuoso's animation system.
  const scrollerElementRef = useRef<HTMLElement | null>(null);
  const scrollerCleanupRef = useRef<(() => void) | null>(null);
  const [hasWindowFocus, setHasWindowFocus] = useState<boolean>(() =>
    typeof document === "undefined" ? true : document.hasFocus()
  );
  const focusStateRef = useRef<boolean>(hasWindowFocus);
  // True for 5 seconds after regaining focus: prevents marking messages as read
  // before the user has had a chance to see them.
  const [isInFocusGracePeriod, setIsInFocusGracePeriod] = useState(false);
  const focusGraceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [separatorDismissed, setSeparatorDismissed] = useState(false);
  const [openActionsMessageId, setOpenActionsMessageId] = useState<string | null>(null);
  const [replyingToMessage, setReplyingToMessage] = useState<models.Message | null>(null);
  const [forwardingMessage, setForwardingMessage] = useState<models.Message | null>(null);
  const [forwardModalOpen, setForwardModalOpen] = useState(false);
  const [revealedDeletedMessages, setRevealedDeletedMessages] = useState<Set<string>>(() => new Set());
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [messageToDelete, setMessageToDelete] = useState<{ conversationID: string; messageID: string } | null>(null);

  // Hooks
  const composerRef = useRef<HTMLTextAreaElement | null>(null);
  const focusComposer = useCallback(() => composerRef.current?.focus(), []);

  useEffect(() => {
    const handleFocusMainComposer = () => {
      // The conversation update remounts ChatInput. Wait for that commit before
      // focusing so the thread composer cannot retain focus.
      requestAnimationFrame(() => composerRef.current?.focus());
    };
    window.addEventListener("focus-main-composer", handleFocusMainComposer);
    return () => window.removeEventListener("focus-main-composer", handleFocusMainComposer);
  }, []);

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

  useEffect(() => {
    if (!messageSearchTargetId || isFetchingNextPage) return;
    const index = mainMessages.findIndex((message) => message.protocolMsgId === messageSearchTargetId);
    if (index >= 0) {
      requestAnimationFrame(() => {
        virtuosoRef.current?.scrollToIndex({ index, align: "center", behavior: "auto" });
        setMessageSearchTargetId(null);
      });
    } else if (hasNextPage) {
      void fetchNextPage();
    } else {
      setMessageSearchTargetId(null);
    }
  }, [messageSearchTargetId, mainMessages, hasNextPage, isFetchingNextPage, fetchNextPage, setMessageSearchTargetId]);

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
    handleEditKeyDown,
    handleEditBlur,
  } = useMessageEdit({ messages, conversationId, showToast, t, focusComposer });

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
  const setSelectedContactProfile = useAppStore((state) => state.setSelectedContactProfile);
  const presenceMap = usePresenceStore((state) => state.presenceMap);

  const markMessageAsRead = useMessageReadStore((state) => state.markAsRead);
  const markAsReadSilently = useMessageReadStore((state) => state.markAsReadSilently);
  const markMultipleAsRead = useMessageReadStore((state) => state.markMultipleAsRead);

  // Effects
  useEffect(() => { setIsTypingInInput(false); }, [selectedConversation?.id, setIsTypingInInput]);
  useEffect(() => { setRevealedDeletedMessages(new Set()); }, [conversationId]);
  useEffect(() => { setSeparatorDismissed(false); }, [conversationId]);
  useEffect(() => { focusStateRef.current = hasWindowFocus; }, [hasWindowFocus]);

  useEffect(() => {
    const handleFocus = () => {
      setHasWindowFocus(true);
      setIsInFocusGracePeriod(true);
      if (focusGraceTimerRef.current) clearTimeout(focusGraceTimerRef.current);
      focusGraceTimerRef.current = setTimeout(() => {
        setIsInFocusGracePeriod(false);
        focusGraceTimerRef.current = null;
      }, 5000);
    };
    const handleBlur = () => {
      setHasWindowFocus(false);
      setIsInFocusGracePeriod(false);
      if (focusGraceTimerRef.current) {
        clearTimeout(focusGraceTimerRef.current);
        focusGraceTimerRef.current = null;
      }
    };
    window.addEventListener("focus", handleFocus);
    window.addEventListener("blur", handleBlur);
    return () => { window.removeEventListener("focus", handleFocus); window.removeEventListener("blur", handleBlur); };
  }, []);

  // When the conversation changes, cancel any pending grace period so the newly
  // selected conversation is marked read immediately (the user actively chose it).
  // The previous conversation stays unread since the mark-as-read effect never
  // fired for it while isInFocusGracePeriod was true.
  useEffect(() => {
    if (focusGraceTimerRef.current) {
      clearTimeout(focusGraceTimerRef.current);
      focusGraceTimerRef.current = null;
    }
    setIsInFocusGracePeriod(false);
  }, [conversationId]);

  // Mark conversation as read — only for main messages, not thread replies.
  // Thread reply messages are only marked read when the thread panel is displayed.
  // Skipped when the window lacks focus or is in the 5-second grace period after
  // regaining focus, so messages received while Loom was in the background are
  // not silently marked as read before the user has seen them.
  useEffect(() => {
    if (!conversationId) return;
    if (!hasWindowFocus || isInFocusGracePeriod) return;
    // Only messages already classified as main messages may be consumed here.
    // A new thread reply reaches the read store just before threadsByParent is
    // recomputed; scanning every store entry would mark it read in that gap.
    const unreadMessages = mainMessages
      .map((message) => getMessageDomId(message))
      .filter((msgId) => conversationReadState[msgId] === false);
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
  }, [conversationId, mainMessages, markMessageAsRead, conversationReadState, selectedConversation, hasWindowFocus, isInFocusGracePeriod]);

  // Older provider versions could persist an unsupported WhatsApp wrapper as an
  // empty message. Such a message is deliberately absent from mainMessages, so
  // the normal "mark visible messages as read" effect can never consume it.
  // Clear those orphaned unread entries when the conversation is loaded.
  useEffect(() => {
    if (!conversationId) return;
    messages.forEach((message) => {
      const hasVisibleContent =
        Boolean(message.body?.trim()) ||
        Boolean(message.attachments?.trim()) ||
        Boolean(message.callType?.trim());
      if (hasVisibleContent || message.isFromMe) return;
      const messageId = getMessageDomId(message);
      if (conversationReadState[messageId] === false) {
        markAsReadSilently(conversationId, messageId);
      }
    });
  }, [conversationId, messages, conversationReadState, markAsReadSilently]);

  // Snapshot the first unread message ID once per conversation (when messages first arrive).
  // A live useMemo on conversationReadState would recompute every time a message is
  // marked read (on every scroll via rangeChanged), causing all Virtuoso items to
  // re-render via commonItemProps and the "new messages" divider to disappear from
  // one item — changing its height and triggering scroll drift.
  // Using a plain ref avoids any extra renders; the value is stable until the user
  // switches to a different conversation.
  const firstUnreadSnapshotRef = useRef<{ convId: string; unreadId: string | null; count: number } | null>(null);
  if (mainMessages.length > 0 && firstUnreadSnapshotRef.current?.convId !== conversationId) {
    let firstUnread: string | null = null;
    let count = 0;
    for (const message of mainMessages) {
      const domId = getMessageDomId(message);
      if (conversationReadState[domId] === false) {
        if (!firstUnread) firstUnread = domId;
        count += 1;
      }
    }
    firstUnreadSnapshotRef.current = { convId: conversationId, unreadId: firstUnread, count };
  }
  const firstUnreadMessageId = firstUnreadSnapshotRef.current?.convId === conversationId
    ? (firstUnreadSnapshotRef.current.unreadId ?? null)
    : null;
  const unreadMessageCount = firstUnreadSnapshotRef.current?.convId === conversationId
    ? firstUnreadSnapshotRef.current.count
    : 0;

  useEffect(() => {
    if (!firstUnreadMessageId || separatorDismissed) return;
    const timer = setTimeout(() => setSeparatorDismissed(true), 10000);
    return () => clearTimeout(timer);
  }, [firstUnreadMessageId, separatorDismissed]);

  // Correct post-measurement scroll drift: Virtuoso positions based on estimated heights then
  // Scroll to bottom (or first unread) on conversation open, then defend against
  // the drift that happens as Virtuoso measures overscan items' actual heights.
  useEffect(() => {
    if (!conversationId || !mainMessages.length) return;
    if (scrollInitializedRef.current === conversationId) return;
    scrollInitializedRef.current = conversationId;

    // Only stabilize (auto-re-anchor) when we're targeting the bottom.
    // If there's a firstUnreadMessageId the user is intentionally NOT at the bottom.
    if (!firstUnreadMessageId) {
      isStabilizingRef.current = true;
      setIsStabilizing(true);
    } else {
      isStabilizingRef.current = false;
      setIsStabilizing(false);
    }

    // Double rAF: first lets React commit, second lets Virtuoso measure item heights.
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        if (!virtuosoRef.current) return;
        if (firstUnreadMessageId) {
          const idx = mainMessages.findIndex((m) => getMessageDomId(m) === firstUnreadMessageId);
          if (idx >= 0) {
            virtuosoRef.current.scrollToIndex({ index: idx, align: 'start', behavior: 'auto' });
            return;
          }
        }
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
    // Sending a message always means returning to the live edge. Keep that
    // intent through both the optimistic and confirmed render.
    isStabilizingRef.current = true;
    setIsStabilizing(true);
    // followOutput owns item insertion. ResizeObserver below only handles real
    // post-render size changes; running another scrollToIndex here caused two
    // competing corrections during send.
  }, [hasPendingMessage, conversationId, mainMessages.length]);

  const correctBottomImmediately = useCallback(() => {
    const el = scrollerElementRef.current;
    if (!el || !isStabilizingRef.current) return;
    el.scrollTop = Math.max(0, el.scrollHeight - el.clientHeight);
  }, []);

  const setScrollerElement = useCallback((ref: HTMLElement | Window | null) => {
    scrollerCleanupRef.current?.();
    scrollerCleanupRef.current = null;
    const el = ref instanceof HTMLElement ? ref : null;
    scrollerElementRef.current = el;
    if (!el) return;

    const stopFollowingBottom = () => {
      isStabilizingRef.current = false;
      setIsStabilizing(false);
    };
    let userScrollIntentUntil = 0;
    const handleWheel = (event: WheelEvent) => {
      if (event.deltaY < 0) {
        userScrollIntentUntil = performance.now() + 300;
        stopFollowingBottom();
      }
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (["ArrowUp", "PageUp", "Home"].includes(event.key)) stopFollowingBottom();
    };
    const handlePointerDown = (event: PointerEvent) => {
      // Pointer events whose target is the scroller itself are scrollbar
      // interactions. Clicking a message, link, or reaction must not unpin it.
      if (event.target === el) stopFollowingBottom();
    };
    let previousScrollTop = el.scrollTop;
    const handleScroll = () => {
      const currentScrollTop = el.scrollTop;
      // Wheel events can be coalesced during a fast trackpad gesture, and
      // Virtuoso may report atBottom=false before our wheel listener runs.
      // A decreasing scrollTop is an unambiguous move away from the live edge.
      if (
        performance.now() <= userScrollIntentUntil &&
        currentScrollTop < previousScrollTop - 1
      ) {
        stopFollowingBottom();
      }
      previousScrollTop = currentScrollTop;
    };

    // Virtuoso's viewport has a fixed height; its item-list descendant is the
    // element whose size changes when asynchronous message content resolves.
    // ResizeObserver runs after layout but before paint. Correcting directly in
    // its callback avoids exposing one frame where a growing textarea has
    // reduced the viewport while the scroll position is still unchanged.
    const resizeObserver = new ResizeObserver(correctBottomImmediately);
    const observeContent = () => {
      resizeObserver.disconnect();
      resizeObserver.observe(el);
      el.querySelectorAll<HTMLElement>(
        '[data-viewport-type], [data-testid="virtuoso-item-list"]'
      ).forEach((node) => resizeObserver.observe(node));
    };
    const mutationObserver = new MutationObserver(() => {
      observeContent();
    });
    observeContent();
    mutationObserver.observe(el, { childList: true, subtree: true });

    el.addEventListener("wheel", handleWheel, { passive: true });
    el.addEventListener("touchmove", stopFollowingBottom, { passive: true });
    el.addEventListener("pointerdown", handlePointerDown, { passive: true });
    el.addEventListener("keydown", handleKeyDown);
    el.addEventListener("scroll", handleScroll, { passive: true });

    scrollerCleanupRef.current = () => {
      resizeObserver.disconnect();
      mutationObserver.disconnect();
      el.removeEventListener("wheel", handleWheel);
      el.removeEventListener("touchmove", stopFollowingBottom);
      el.removeEventListener("pointerdown", handlePointerDown);
      el.removeEventListener("keydown", handleKeyDown);
      el.removeEventListener("scroll", handleScroll);
    };
  }, [correctBottomImmediately]);

  useEffect(() => () => {
    scrollerCleanupRef.current?.();
  }, []);

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
  const handleContactAvatarClick = useCallback((message: models.Message, displayName: string) => {
    const accountStatus = selectedConversation.linkedAccounts.find(
      (account) => account.status && account.status !== "offline"
    )?.status;
    setSelectedContactProfile({
      conversationId,
      userId: message.senderId,
      displayName,
      avatarUrl: message.senderAvatarUrl,
      status: message.isFromMe
        ? ""
        : accountStatus || (presenceMap[message.senderId] ? "online" : "offline"),
      isSelf: message.isFromMe,
    });
  }, [conversationId, presenceMap, selectedConversation.linkedAccounts, setSelectedContactProfile]);

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
    const nativeEmojiReactions = providerInstanceId
      ? capabilities[providerInstanceId]?.nativeEmojiReactions ?? false
      : false;
    const { apiEmoji, canonicalName, storedEmoji } = normalizeReaction(emoji, nativeEmojiReactions);

    const hasReaction = messageReactions.some((r) => {
      return reactionMatches(r.emoji, canonicalName) && r.userId === currentUserId;
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
                    return !(reactionMatches(r.emoji, canonicalName) && r.userId === currentUserId);
                  })
                : [...(msg.reactions || []), models.Reaction.createFrom({ id: 0, messageId: msg.id, userId: currentUserId || "", emoji: storedEmoji, createdAt: new Date(), updatedAt: new Date() })];
              return models.Message.createFrom({ ...msg, reactions: updatedReactions });
            })
          ),
        };
      }
    );

    try {
      if (hasReaction) await RemoveReaction(conversationId, protocolMsgId, apiEmoji);
      else await AddReaction(conversationId, protocolMsgId, apiEmoji);
    } catch (error) {
      console.error("Failed to handle reaction:", error);
      showToast(t("error"), "error");
      queryClient.invalidateQueries({ queryKey: ["messages", conversationId] });
      queryClient.refetchQueries({ queryKey: ["messages", conversationId] });
    }
  }, [capabilities, conversationId, currentUserId, providerInstanceId, queryClient, t, showToast]);


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
    onContactAvatarClick: handleContactAvatarClick,
    onNavigateToEdit: handleNavigateToEdit,
    onEditKeyDown: handleEditKeyDown,
    onEditBlur: handleEditBlur,
    editingInputRef,
    setOpenActionsMessageId,
    showToast,
  }), [toggleDeletedMessage, handleEditMessage, handleDeleteClick, handleReplyClick, handleForwardClick, handleReaction, handleRetrySend, handleDeleteLocalMessage, handleSaveEdit, handleCancelEdit, handleAvatarClick, handleContactAvatarClick, handleNavigateToEdit, handleEditKeyDown, handleEditBlur, setSelectedThreadId, setShowThreads, showToast]);

  const commonItemProps = {
    mainMessages,
    conversationId,
    providerInstanceId,
    protocol,
    isGroupConversation,
    conversationReadState,
    firstUnreadMessageId,
    unreadMessageCount,
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
        className={cn("relative flex flex-col h-full overflow-hidden transition-colors", isDragging && "bg-muted/50")}
        onDragEnter={handleDragEnter}
        onDragLeave={handleDragLeave}
        onDragOver={handleDragOver}
        onDrop={handleDrop}
      >
        {isDragging && (
          <div className="pointer-events-none absolute inset-3 z-50 flex items-center justify-center rounded-xl border-2 border-dashed border-primary bg-background/85 backdrop-blur-sm">
            <div className="flex flex-col items-center gap-3 rounded-xl px-8 py-6 text-center text-primary">
              <UploadCloud className="h-10 w-10" />
              <p className="text-sm font-semibold">{t("drop_files_to_upload")}</p>
            </div>
          </div>
        )}
        <MessageHeader
          displayName={selectedConversation.displayName}
          avatarUrl={selectedConversation.avatarUrl}
          conversationId={conversationId}
          contactUserId={messages.find((message) => !message.isFromMe && message.senderId)?.senderId}
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
          <Virtuoso
            key={conversationId}
            ref={virtuosoRef}
            className="h-full scroll-area bg-background"
            style={{ paddingTop: "1rem", paddingBottom: "1rem" }}
            data={mainMessages}
            alignToBottom
            followOutput={(isAtBottom) => {
              const lastMsg = mainMessages.at(-1) as unknown as Record<string, unknown>;
              if (lastMsg?.isFromMe === true) return "auto";
              return isAtBottom ? "smooth" : false;
            }}
            startReached={handleStartReached}
            atBottomStateChange={(isAtBottom) => {
              atBottomRef.current = isAtBottom;
              setAtBottom(isAtBottom);
              if (isAtBottom) {
                isStabilizingRef.current = true;
                setIsStabilizing(true);
              }
            }}
            scrollerRef={setScrollerElement}
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
          {isFetchingNextPage && (
            <div className="message-list__history-loader pointer-events-none absolute left-1/2 top-3 z-10 -translate-x-1/2 rounded-full border bg-background/90 px-3 py-1.5 shadow-sm backdrop-blur">
              <div className="flex items-center gap-2">
                <div className="h-4 w-4 rounded-full border-2 border-primary border-t-transparent animate-spin" />
                <span className="text-sm text-muted-foreground">{t("loading")}</span>
              </div>
            </div>
          )}
          {!atBottom && !isStabilizing && (
            <button
              className="message-list__scroll-to-bottom absolute bottom-4 right-4 z-10 flex h-9 w-9 items-center justify-center rounded-full bg-primary text-primary-foreground shadow-md transition-opacity hover:opacity-90"
              title={t("scroll_to_bottom")}
              onClick={() => {
                isStabilizingRef.current = true;
                setIsStabilizing(true);
                virtuosoRef.current?.scrollToIndex({ index: mainMessages.length - 1, behavior: "smooth" });
              }}
            >
              <ChevronDown className="h-5 w-5" />
            </button>
          )}
          </div>
        )}
        <div className="shrink-0">
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
            onTextareaMount={(textarea) => { composerRef.current = textarea; }}
            currentUserName={currentUserName}
            currentUserAvatarUrl={currentUserAvatarUrl}
            onHeightChange={correctBottomImmediately}
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
