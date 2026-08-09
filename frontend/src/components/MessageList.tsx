import {
  AddReaction,
  CancelScheduledMessage,
  DeleteMessage,
  GetCapabilities,
  GetPinnedMessageContext,
  GetPinnedMessages,
  GetScheduledMessages,
  PinMessage,
  RemoveReaction,
  UnpinMessage,
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
import { Button } from "@/components/ui/button";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { VirtuosoHandle } from "react-virtuoso";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { ChatInput } from "./ChatInput";
import { FileUploadModal } from "./FileUploadModal";
import { ForwardMessageModal } from "./ForwardMessageModal";
import type { InfiniteData } from "@tanstack/react-query";
import type { MessageHandlers } from "./MessageBubbleItem";
import { MessageBubbleItem } from "./MessageBubbleItem";
import { MessageHeader } from "./MessageHeader";
import { PinnedMessagesPanel } from "./PinnedMessagesPanel";
import { MessageIRCItem } from "./MessageIRCItem";
import { CalendarClock, ChevronDown, ChevronUp, Trash2, UploadCloud } from "lucide-react";
import { cn } from "@/lib/utils";
import { getMessageDomId } from "@/lib/messageUtils";
import { models } from "../../wailsjs/go/models";
import { normalizeReaction, reactionMatches } from "@/lib/reactionUtils";
import { groupConsecutivePhotoMessages } from "@/lib/photoMessageGroups";


import { useAppStore } from "@/lib/store";
import { usePresenceStore } from "@/lib/presenceStore";
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
  const messageLayout = useAppStore((state) => state.messageLayout);
  const capabilities = useAppStore((state) => state.capabilities);
  const setCapabilities = useAppStore((state) => state.setCapabilities);
  const isGroupFromProvider = !!activeAccount?.isGroup;

  // State
  const virtuosoRef = useRef<VirtuosoHandle>(null);
  const nativeListRef = useRef<HTMLDivElement>(null);
  const pendingAnchorRef = useRef<{ messageId: string; viewportOffset: number } | null>(null);
  const positionedUnreadBoundaryRef = useRef<string>("");
  const liveEdgeSnapshotRef = useRef<{ conversationId: string; lastMessageId: string } | null>(null);
  const presentedConversationRef = useRef<string>("");
  const scrollInitializedRef = useRef<string>('');
  const atBottomRef = useRef(true);
  const [atBottom, setAtBottom] = useState(true);
  // Whether the list should remain anchored to the bottom while rendered
  // messages change height (images, audio players, link previews, etc.).
  // This is an intent, not a timer: it stays true until the user deliberately
  // moves away from the bottom.
  const isStabilizingRef = useRef(false);
  const [isStabilizing, setIsStabilizing] = useState(false);
  // Native timeline viewport and content anchor state.
  const scrollerElementRef = useRef<HTMLElement | null>(null);
  const scrollerCleanupRef = useRef<(() => void) | null>(null);
  const historyFetchInFlightRef = useRef(false);
  const [hasWindowFocus, setHasWindowFocus] = useState<boolean>(() =>
    typeof document === "undefined" ? true : document.hasFocus()
  );
  // True for 5 seconds after regaining focus: prevents marking messages as read
  // before the user has had a chance to see them.
  const [isInFocusGracePeriod, setIsInFocusGracePeriod] = useState(false);
  const focusGraceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [separatorDismissed, setSeparatorDismissed] = useState(false);
  const [unreadBoundary, setUnreadBoundary] = useState<{
    conversationId: string;
    firstMessageId: string;
    count: number;
  } | null>(null);
  const [openActionsMessageId, setOpenActionsMessageId] = useState<string | null>(null);
  const [replyingToMessage, setReplyingToMessage] = useState<models.Message | null>(null);
  const [forwardingMessages, setForwardingMessages] = useState<models.Message[]>([]);
  const [forwardModalOpen, setForwardModalOpen] = useState(false);
  const [revealedDeletedMessages, setRevealedDeletedMessages] = useState<Set<string>>(() => new Set());
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [messageToDelete, setMessageToDelete] = useState<{ conversationID: string; messageID: string } | null>(null);
  const [showPins, setShowPins] = useState(false);
  const [historicalContext, setHistoricalContext] = useState(false);

  useEffect(() => {
    setHistoricalContext(false);
    setShowPins(false);
  }, [conversationId]);

  const supportsPinMessage = providerInstanceId ? capabilities[providerInstanceId]?.supportsPinMessage ?? false : false;
  const supportsListScheduledMessages = providerInstanceId ? capabilities[providerInstanceId]?.supportsListScheduledMessages ?? false : false;
  const supportsScheduledMessages = providerInstanceId ? capabilities[providerInstanceId]?.supportsScheduledMessages ?? false : false;

  // MessageList can be mounted before ContactList has populated the shared
  // capability cache. Load the active provider here as well so message actions
  // do not temporarily (or permanently, in alternate layouts) hide features.
  useEffect(() => {
    if (!providerInstanceId || capabilities[providerInstanceId]) return;
    GetCapabilities(providerInstanceId)
      .then((providerCapabilities) => setCapabilities(providerInstanceId, providerCapabilities))
      .catch((error) => console.error("Failed to load provider capabilities:", error));
  }, [capabilities, providerInstanceId, setCapabilities]);

  const { data: pinnedMessages = [], isFetching: pinsLoading, refetch: refetchPins } = useQuery({
    queryKey: ["message-pins", conversationId],
    queryFn: () => GetPinnedMessages(conversationId).catch(() => []),
    enabled: Boolean(conversationId && providerInstanceId && capabilities[providerInstanceId]?.supportsListMessagePins),
  });
  const pinnedMessageIds = useMemo(() => new Set(pinnedMessages.map((pin) => pin.protocolMsgId)), [pinnedMessages]);

  const prevScheduledCountRef = useRef<number>(0);
  const { data: rawScheduledMessages } = useQuery({
    queryKey: ["scheduled-messages", conversationId],
    queryFn: () => GetScheduledMessages(conversationId),
    enabled: Boolean(conversationId && supportsListScheduledMessages),
    refetchInterval: 15_000,
  });
  const scheduledMessages = rawScheduledMessages ?? [];

  // React Query v5 removed onSuccess from useQuery — use useEffect instead.
  // When the scheduled count drops, a message was delivered: refresh the conversation.
  useEffect(() => {
    const next = scheduledMessages.length;
    const prev = prevScheduledCountRef.current;
    prevScheduledCountRef.current = next;
    if (prev > 0 && next < prev) {
      queryClient.invalidateQueries({ queryKey: ["messages", conversationId] });
    }
  }, [scheduledMessages.length, conversationId, queryClient]);

  const [expandedScheduledIds, setExpandedScheduledIds] = useState<Set<string>>(new Set());

  const toggleScheduledExpand = useCallback((id: string) => {
    setExpandedScheduledIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const cancelScheduledMutation = useMutation({
    mutationFn: ({ protocolConvId, id }: { protocolConvId: string; id: string }) =>
      CancelScheduledMessage(protocolConvId, id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["scheduled-messages", conversationId] }),
    onError: (error: unknown) => {
      const msg = error instanceof Error ? error.message : String(error);
      showToast(msg, "error");
    },
  });

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
    threadReplyCounts,
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

  const displayedMessageGroups = useMemo(
    () => groupConsecutivePhotoMessages(mainMessages),
    [mainMessages]
  );
  const displayIndexByMessageId = useMemo(() => {
    const indexes = new Map<string, number>();
    displayedMessageGroups.forEach((group, index) => {
      group.messages.forEach((message) => indexes.set(getMessageDomId(message), index));
    });
    return indexes;
  }, [displayedMessageGroups]);
  const mainIndexByMessageId = useMemo(() => {
    const indexes = new Map<string, number>();
    mainMessages.forEach((message, index) => indexes.set(getMessageDomId(message), index));
    return indexes;
  }, [mainMessages]);
  const scrollToNativeIndex = useCallback((location: Parameters<VirtuosoHandle["scrollToIndex"]>[0]) => {
    const normalized = typeof location === "number" ? { index: location } : location;
    const node = nativeListRef.current?.querySelector<HTMLElement>(
      `[data-message-group-index="${normalized.index}"]`
    );
    if (!node) return;
    node.scrollIntoView({
      behavior: normalized.behavior === "smooth" ? "smooth" : "auto",
      block: normalized.align === "center" ? "center" : normalized.align === "end" ? "end" : "start",
    });
  }, []);
  useLayoutEffect(() => {
    virtuosoRef.current = {
      scrollToIndex: scrollToNativeIndex,
    } as unknown as VirtuosoHandle;
    return () => {
      virtuosoRef.current = null;
    };
  }, [scrollToNativeIndex]);

  useEffect(() => {
    if (!messageSearchTargetId || isFetchingNextPage) return;
    const index = displayedMessageGroups.findIndex((group) => group.messages.some((message) => message.protocolMsgId === messageSearchTargetId));
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
  }, [messageSearchTargetId, displayedMessageGroups, hasNextPage, isFetchingNextPage, fetchNextPage, setMessageSearchTargetId]);

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
  const showConversationDetails = useAppStore((state) => state.showConversationDetails);
  const setShowConversationDetails = useAppStore((state) => state.setShowConversationDetails);
  const setSelectedAvatarUrl = useAppStore((state) => state.setSelectedAvatarUrl);
  const setSelectedContactProfile = useAppStore((state) => state.setSelectedContactProfile);
  const presenceMap = usePresenceStore((state) => state.presenceMap);

  const markMessageAsRead = useMessageReadStore((state) => state.markAsRead);
  const markAsReadSilently = useMessageReadStore((state) => state.markAsReadSilently);

  // Effects
  useEffect(() => { setIsTypingInInput(false); }, [selectedConversation?.id, setIsTypingInInput]);
  useEffect(() => { setRevealedDeletedMessages(new Set()); }, [conversationId]);
  useLayoutEffect(() => {
    setSeparatorDismissed(false);
    setUnreadBoundary(null);
  }, [conversationId]);

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
      setUnreadBoundary(null);
      setSeparatorDismissed(false);
      positionedUnreadBoundaryRef.current = "";
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

  // Capture the unread boundary whenever the selected conversation contains
  // unread messages, independently from window focus. This layout-phase
  // snapshot happens before the passive effect that marks the conversation as
  // read, and remains visible after the server/read-store update.
  useLayoutEffect(() => {
    const unreadIds = mainMessages
      .map((message) => getMessageDomId(message))
      .filter((messageId) => conversationReadState[messageId] === false);
    if (unreadIds.length === 0) return;

    setUnreadBoundary((current) => {
      if (
        current?.conversationId === conversationId &&
        current.firstMessageId === unreadIds[0] &&
        current.count === unreadIds.length
      ) {
        return current;
      }
      return {
        conversationId,
        firstMessageId: current?.conversationId === conversationId
          ? current.firstMessageId
          : unreadIds[0],
        count: unreadIds.length,
      };
    });
  }, [conversationId, mainMessages, conversationReadState]);

  const firstUnreadMessageId = unreadBoundary?.conversationId === conversationId
    ? unreadBoundary.firstMessageId
    : null;
  const unreadMessageCount = unreadBoundary?.conversationId === conversationId
    ? unreadBoundary.count
    : 0;
  const firstUnreadGroup = firstUnreadMessageId
    ? displayedMessageGroups[displayIndexByMessageId.get(firstUnreadMessageId) ?? -1]
    : undefined;
  const displayedFirstUnreadMessageId = firstUnreadGroup
    ? getMessageDomId(firstUnreadGroup.message)
    : null;

  useEffect(() => {
    if (!hasWindowFocus || !firstUnreadMessageId || separatorDismissed) return;
    const timer = setTimeout(() => setSeparatorDismissed(true), 10000);
    return () => clearTimeout(timer);
  }, [hasWindowFocus, firstUnreadMessageId, separatorDismissed]);

  // Position the native timeline before the first paint.
  useLayoutEffect(() => {
    if (!conversationId) return;

    // Reset the presentation state as soon as the selected conversation
    // changes, even when the intermediate conversation has no messages. This
    // guarantees that returning to an already cached conversation is treated
    // as a fresh opening.
    if (presentedConversationRef.current !== conversationId) {
      presentedConversationRef.current = conversationId;
      scrollInitializedRef.current = "";
      pendingAnchorRef.current = null;
      atBottomRef.current = true;
      isStabilizingRef.current = true;
      setAtBottom(true);
      setIsStabilizing(true);
    }

    if (!mainMessages.length) return;
    if (scrollInitializedRef.current === conversationId) return;
    scrollInitializedRef.current = conversationId;

    if (!virtuosoRef.current) return;
    virtuosoRef.current.scrollToIndex({ index: displayedMessageGroups.length - 1, align: 'end', behavior: 'auto' });
  }, [conversationId, mainMessages.length, displayedMessageGroups.length]);

  // Re-anchor to bottom when a pending message is added or confirmed.
  // Keep the live-edge intent through the optimistic→confirmed transition.
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
    // The layout effect below owns the single bottom correction after commit.
  }, [hasPendingMessage, conversationId, mainMessages.length]);

  const correctBottomImmediately = useCallback(() => {
    const el = scrollerElementRef.current;
    if (!el || !isStabilizingRef.current) return;
    el.scrollTop = Math.max(0, el.scrollHeight - el.clientHeight);
  }, []);

  const saveVisibleAnchor = useCallback(() => {
    const scroller = scrollerElementRef.current;
    const list = nativeListRef.current;
    if (!scroller || !list || isStabilizingRef.current) return;
    const viewportTop = scroller.getBoundingClientRect().top;
    const nodes = list.querySelectorAll<HTMLElement>("[data-message-group-index]");
    for (const node of nodes) {
      const rect = node.getBoundingClientRect();
      if (rect.bottom <= viewportTop) continue;
      const messageId = node.dataset.anchorMessageId;
      if (messageId) {
        pendingAnchorRef.current = { messageId, viewportOffset: rect.top - viewportTop };
      }
      break;
    }
  }, []);

  const restoreVisibleAnchor = useCallback(() => {
    const anchor = pendingAnchorRef.current;
    const scroller = scrollerElementRef.current;
    if (!anchor || !scroller || isStabilizingRef.current) return;
    const groupIndex = displayIndexByMessageId.get(anchor.messageId);
    if (groupIndex === undefined) return;
    const node = nativeListRef.current?.querySelector<HTMLElement>(
      `[data-message-group-index="${groupIndex}"]`
    );
    if (!node) return;
    const currentOffset = node.getBoundingClientRect().top - scroller.getBoundingClientRect().top;
    scroller.scrollTop += currentOffset - anchor.viewportOffset;
  }, [displayIndexByMessageId]);

  const setScrollerElement = useCallback((ref: HTMLElement | Window | null) => {
    scrollerCleanupRef.current?.();
    scrollerCleanupRef.current = null;
    const el = ref instanceof HTMLElement ? ref : null;
    scrollerElementRef.current = el;
    if (!el) return;

    const stopFollowingBottom = () => {
      // Record the user's intent immediately, before the browser dispatches
      // the resulting scroll event. An incoming message can otherwise land
      // between wheel/touch input and scroll and incorrectly pull the user
      // back to the live edge.
      atBottomRef.current = false;
      isStabilizingRef.current = false;
      setAtBottom(false);
      setIsStabilizing(false);
    };
    const handleWheel = (event: WheelEvent) => {
      if (event.deltaY < 0) stopFollowingBottom();
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (["ArrowUp", "PageUp", "Home"].includes(event.key)) stopFollowingBottom();
    };
    const handlePointerDown = (event: PointerEvent) => {
      // Pointer events whose target is the scroller itself are scrollbar
      // interactions. Clicking a message, link, or reaction must not unpin it.
      if (event.target === el) stopFollowingBottom();
    };
    const resizeObserver = new ResizeObserver(() => {
      if (isStabilizingRef.current) correctBottomImmediately();
      else restoreVisibleAnchor();
    });
    resizeObserver.observe(el);
    const observeFrame = requestAnimationFrame(() => {
      const itemList = nativeListRef.current;
      if (itemList) resizeObserver.observe(itemList);
    });

    el.addEventListener("wheel", handleWheel, { passive: true });
    el.addEventListener("touchmove", stopFollowingBottom, { passive: true });
    el.addEventListener("pointerdown", handlePointerDown, { passive: true });
    el.addEventListener("keydown", handleKeyDown);

    scrollerCleanupRef.current = () => {
      cancelAnimationFrame(observeFrame);
      resizeObserver.disconnect();
      el.removeEventListener("wheel", handleWheel);
      el.removeEventListener("touchmove", stopFollowingBottom);
      el.removeEventListener("pointerdown", handlePointerDown);
      el.removeEventListener("keydown", handleKeyDown);
    };
  }, [correctBottomImmediately, restoreVisibleAnchor]);

  useEffect(() => () => {
    scrollerCleanupRef.current?.();
  }, []);

  const latestMessageId = mainMessages.length > 0
    ? getMessageDomId(mainMessages[mainMessages.length - 1])
    : "";

  useLayoutEffect(() => {
    const previous = liveEdgeSnapshotRef.current;
    const isSameConversation = previous?.conversationId === conversationId;
    const liveEdgeChanged = Boolean(
      isSameConversation &&
      previous?.lastMessageId &&
      latestMessageId &&
      previous.lastMessageId !== latestMessageId
    );
    const wasFollowingLiveEdge = atBottomRef.current && isStabilizingRef.current;

    liveEdgeSnapshotRef.current = { conversationId, lastMessageId: latestMessageId };

    // A prepend leaves the last message unchanged. A changed last message is a
    // genuine live-edge update: follow it only if the user was already at the
    // bottom before React committed the new message.
    if (!liveEdgeChanged || !wasFollowingLiveEdge) return;
    pendingAnchorRef.current = null;
    isStabilizingRef.current = true;
    atBottomRef.current = true;
    setIsStabilizing(true);
    setAtBottom(true);
    correctBottomImmediately();
  }, [conversationId, latestMessageId, correctBottomImmediately]);

  useLayoutEffect(() => {
    if (isStabilizingRef.current) correctBottomImmediately();
    else restoreVisibleAnchor();
  }, [displayedMessageGroups, correctBottomImmediately, restoreVisibleAnchor]);

  useLayoutEffect(() => {
    if (!hasWindowFocus || separatorDismissed || !displayedFirstUnreadMessageId) return;
    const positionKey = `${conversationId}:${displayedFirstUnreadMessageId}:${unreadMessageCount}`;
    if (positionedUnreadBoundaryRef.current === positionKey) return;

    const scroller = scrollerElementRef.current;
    const groupIndex = displayIndexByMessageId.get(displayedFirstUnreadMessageId);
    if (!scroller || groupIndex === undefined) return;
    const firstUnreadNode = nativeListRef.current?.querySelector<HTMLElement>(
      `[data-message-group-index="${groupIndex}"]`
    );
    if (!firstUnreadNode) return;

    positionedUnreadBoundaryRef.current = positionKey;
    const viewportTop = scroller.getBoundingClientRect().top;
    const unreadTop = scroller.scrollTop + firstUnreadNode.getBoundingClientRect().top - viewportTop;
    const unreadContentHeight = scroller.scrollHeight - unreadTop;

    if (unreadContentHeight <= scroller.clientHeight) {
      // All unread content fits: expose the latest message and eliminate any
      // residual empty space below it.
      pendingAnchorRef.current = null;
      atBottomRef.current = true;
      isStabilizingRef.current = true;
      setAtBottom(true);
      setIsStabilizing(true);
      correctBottomImmediately();
      return;
    }

    // More than one viewport of unread content: put the divider at the top and
    // retain it as the fixed anchor while asynchronous media settles.
    isStabilizingRef.current = false;
    atBottomRef.current = false;
    setIsStabilizing(false);
    setAtBottom(false);
    scroller.scrollTop = Math.max(0, unreadTop);
    pendingAnchorRef.current = {
      messageId: displayedFirstUnreadMessageId,
      viewportOffset: 0,
    };
  }, [
    conversationId,
    correctBottomImmediately,
    displayIndexByMessageId,
    displayedFirstUnreadMessageId,
    hasWindowFocus,
    separatorDismissed,
    unreadMessageCount,
  ]);

  // Handlers
  const handleStartReached = useCallback(() => {
    if (!hasNextPage || isFetchingNextPage || historyFetchInFlightRef.current) return;
    saveVisibleAnchor();
    historyFetchInFlightRef.current = true;
    void fetchNextPage().finally(() => {
      historyFetchInFlightRef.current = false;
    });
  }, [hasNextPage, isFetchingNextPage, fetchNextPage, saveVisibleAnchor]);

  const handleNativeScroll = useCallback(() => {
    const el = scrollerElementRef.current;
    if (!el) return;
    const isNowAtBottom = el.scrollHeight - el.scrollTop - el.clientHeight <= 2;
    atBottomRef.current = isNowAtBottom;
    setAtBottom(isNowAtBottom);
    if (isNowAtBottom) {
      isStabilizingRef.current = true;
      setIsStabilizing(true);
    } else {
      isStabilizingRef.current = false;
      setIsStabilizing(false);
      saveVisibleAnchor();
    }
    if (el.scrollTop <= el.clientHeight) handleStartReached();
  }, [handleStartReached, saveVisibleAnchor]);

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

  const handleForwardClick = useCallback((message: models.Message, groupedMessages?: models.Message[]) => {
    setForwardingMessages(groupedMessages?.length ? groupedMessages : [message]);
    setForwardModalOpen(true);
  }, []);

  const handlePinClick = useCallback(async (message: models.Message) => {
    if (!supportsPinMessage) return;
    const messageID = message.protocolMsgId || getMessageDomId(message);
    try {
      if (pinnedMessageIds.has(messageID)) {
        await UnpinMessage(conversationId, messageID);
        showToast(t("message_unpinned"), "success");
      } else {
        const pin = await PinMessage(conversationId, messageID);
        showToast(t(pin.scope === "personal" ? "message_pinned_personal" : "message_pinned_shared"), "success");
      }
      await refetchPins();
    } catch (error) {
      console.error("Failed to update message pin:", error);
      showToast(String(error), "error");
    }
  }, [conversationId, pinnedMessageIds, refetchPins, showToast, supportsPinMessage, t]);

  const openPinnedMessage = useCallback(async (pin: models.MessagePin) => {
    try {
      const context = await GetPinnedMessageContext(conversationId, pin.protocolMsgId);
      queryClient.setQueryData<InfiniteData<models.Message[]>>(["messages", conversationId], {
        pages: [context.messages],
        pageParams: [undefined],
      });
      setHistoricalContext(true);
      setShowPins(false);
      setMessageSearchTargetId(pin.protocolMsgId);
    } catch (error) {
      showToast(String(error), "error");
    }
  }, [conversationId, queryClient, setMessageSearchTargetId, showToast]);

  const closeHistoricalContext = useCallback(async () => {
    setHistoricalContext(false);
    queryClient.removeQueries({ queryKey: ["messages", conversationId], exact: true });
    await queryClient.refetchQueries({ queryKey: ["messages", conversationId], exact: true });
  }, [conversationId, queryClient]);

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
    onPinClick: handlePinClick,
    isMessagePinned: (message: models.Message) => pinnedMessageIds.has(message.protocolMsgId || getMessageDomId(message)),
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
  }), [toggleDeletedMessage, handleEditMessage, handleDeleteClick, handleReplyClick, handleForwardClick, handlePinClick, pinnedMessageIds, handleReaction, handleRetrySend, handleDeleteLocalMessage, handleSaveEdit, handleCancelEdit, handleAvatarClick, handleContactAvatarClick, handleNavigateToEdit, handleEditKeyDown, handleEditBlur, setSelectedThreadId, setShowThreads, showToast]);

  const commonItemProps = {
    mainMessages,
    conversationId,
    providerInstanceId,
    protocol,
    isGroupConversation,
    conversationReadState,
    firstUnreadMessageId: displayedFirstUnreadMessageId,
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
    threadReplyCounts,
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
          activeAccount={activeAccount}
          onToggleThreads={handleToggleThreads}
          onToggleDetails={handleToggleDetails}
          onTogglePins={() => { void refetchPins(); setShowPins((value) => !value); }}
          pinCount={pinnedMessages.length}
        />
        {historicalContext && (
          <div className="z-20 flex items-center justify-between border-b bg-muted px-4 py-2 text-sm">
            <span>{t("historical_pin_context")}</span>
            <Button variant="outline" size="sm" onClick={closeHistoricalContext}>{t("back_to_recent_messages")}</Button>
          </div>
        )}
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
          <div
            key={conversationId}
            ref={setScrollerElement}
            onScroll={handleNativeScroll}
            className="message-list__native-scroller h-full overflow-y-auto scroll-area bg-background"
            style={{ overflowAnchor: "none" }}
          >
            <div
              ref={nativeListRef}
              className="message-list__native-items flex min-h-full flex-col justify-end py-4"
            >
              {displayedMessageGroups.map((group, groupIndex) => {
                const message = group.message;
                const messageId = getMessageDomId(message);
                return (
                  <div
                    key={messageId}
                    data-message-group-index={groupIndex}
                    data-anchor-message-id={messageId}
                    className="message-list__native-item overflow-x-clip px-4"
                  >
                    {messageLayout === "bubble" ? (
                      <MessageBubbleItem
                        message={message}
                        index={mainIndexByMessageId.get(messageId) ?? 0}
                        photoGroupMessages={group.messages}
                        displayIndexByMessageId={displayIndexByMessageId}
                        editingInputRef={editingInputRef}
                        {...commonItemProps}
                      />
                    ) : (
                      <MessageIRCItem
                        message={message}
                        index={mainIndexByMessageId.get(messageId) ?? 0}
                        photoGroupMessages={group.messages}
                        displayIndexByMessageId={displayIndexByMessageId}
                        messageLayout={messageLayout}
                        {...commonItemProps}
                      />
                    )}
                  </div>
                );
              })}
              {scheduledMessages.length > 0 && (
                <div className="message-list__scheduled-section mx-4 mb-2 mt-4 space-y-2">
                  {scheduledMessages.map((msg) => {
                    const scheduledDate = new Date(msg.scheduledAt as unknown as string);
                    const dateLabel = scheduledDate.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
                    const isExpanded = expandedScheduledIds.has(msg.id);
                    return (
                      <div key={msg.id} className="message-list__scheduled-item rounded-lg border border-dashed border-muted-foreground/40 bg-muted/30 px-3 py-2">
                        <div className="flex items-start gap-3">
                          <CalendarClock className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
                          <button
                            type="button"
                            className="message-list__scheduled-item__body min-w-0 flex-1 text-left"
                            onClick={() => toggleScheduledExpand(msg.id)}
                          >
                            <p className={cn("text-sm", !isExpanded && "line-clamp-2")}>{msg.body}</p>
                            <p className="mt-0.5 text-xs text-muted-foreground">{t("scheduled_message_badge", { date: dateLabel })}</p>
                          </button>
                          <div className="flex shrink-0 items-center gap-0.5">
                            <button
                              type="button"
                              title={isExpanded ? t("collapse") : t("expand")}
                              className="rounded p-1 text-muted-foreground hover:bg-muted"
                              onClick={() => toggleScheduledExpand(msg.id)}
                            >
                              {isExpanded
                                ? <ChevronUp className="h-3.5 w-3.5" />
                                : <ChevronDown className="h-3.5 w-3.5" />}
                            </button>
                            {supportsScheduledMessages && (
                              <button
                                type="button"
                                title={t("cancel_scheduled_message")}
                                className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-destructive"
                                onClick={(e) => {
                                  e.stopPropagation();
                                  cancelScheduledMutation.mutate({ protocolConvId: msg.protocolConvId, id: msg.id });
                                }}
                              >
                                <Trash2 className="h-3.5 w-3.5" />
                              </button>
                            )}
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
              <div className="message-list__native-footer h-4" />
            </div>
          </div>
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
                virtuosoRef.current?.scrollToIndex({ index: displayedMessageGroups.length - 1, behavior: "smooth", align: "end" });
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
          onUploadComplete={focusComposer}
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
          messages={forwardingMessages}
          providerInstanceId={providerInstanceId}
        />
        {showPins && (
          <PinnedMessagesPanel
            pins={pinnedMessages}
            loading={pinsLoading}
            onClose={() => setShowPins(false)}
            onOpen={openPinnedMessage}
            onUnpin={async (pin) => {
              try { await UnpinMessage(conversationId, pin.protocolMsgId); await refetchPins(); showToast(t("message_unpinned"), "success"); }
              catch (error) { showToast(String(error), "error"); }
            }}
          />
        )}
      </div>
    </>
  );
}
