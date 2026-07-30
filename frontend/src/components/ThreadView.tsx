import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { ChatInput } from "./ChatInput";
import { AddReaction, DeleteMessage, GetMessagesForConversation, GetMessagesForConversationBefore, GetThreadMessages, RemoveReaction } from "../../wailsjs/go/main/App";
import { MessageActions } from "./MessageActions";
import { MessageAttachments } from "./MessageAttachments";
import { MessageReactions } from "./MessageReactions";
import { MessageText } from "./MessageText";
import { MessageThreadPreview } from "./MessageThreadPreview";
import { Input } from "@/components/ui/input";
import { ToastContainer, useToast } from "@/components/ui/toast";
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
import { ArrowLeft, Loader2, X } from "lucide-react";
import type { InfiniteData } from "@tanstack/react-query";
import type { models } from "../../wailsjs/go/models";
// InfiniteData is used to type the cache seed read via queryClient.getQueryData
import { getColorFromString, getMessageDomId, getSenderDisplayName, normalizeSlackQuotedReply } from "@/lib/messageUtils";
import { cn, timeToDate } from "@/lib/utils";
import { normalizeReaction, reactionMatches } from "@/lib/reactionUtils";
import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useMessageEdit } from "@/hooks/useMessageEdit";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

const fetchThreads = async (conversationID: string, threadID: string) => {
  return GetThreadMessages(conversationID, threadID);
};

const fetchMessages = async (conversationID: string, beforeTimestamp?: Date): Promise<models.Message[]> => {
  try {
    const result = beforeTimestamp
      ? await GetMessagesForConversationBefore(conversationID, beforeTimestamp)
      : await GetMessagesForConversation(conversationID);
    return Array.isArray(result) ? result : [];
  } catch (error) {
    console.error("Error fetching messages:", error);
    return [];
  }
};

interface ThreadListItemProps {
  parentMessage: models.Message;
  replies: models.Message[];
  providerInstanceId: string | undefined;
  onClick: () => void;
  onAvatarClick: (url: string | undefined, name?: string) => void;
}

function ThreadListItem({ parentMessage, replies, providerInstanceId: _providerInstanceId, onClick, onAvatarClick }: ThreadListItemProps) {
  const { t } = useTranslation();
  const displayName = getSenderDisplayName(parentMessage.senderName, parentMessage.senderId, parentMessage.isFromMe, t);
  const timestamp = timeToDate(parentMessage.timestamp);
  const today = new Date();
  const isToday = timestamp.toDateString() === today.toDateString();
  const timeStr = isToday
    ? timestamp.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
    : timestamp.toLocaleDateString([], { month: "short", day: "numeric" });

  return (
    <button
      onClick={onClick}
      className="thread-list-item w-full p-4 text-left hover:bg-muted/50 transition-colors flex flex-col gap-2 border-b last:border-b-0"
    >
      <div className="flex items-center gap-2 min-w-0">
        <button
          type="button"
          className="shrink-0 bg-transparent border-0 p-0"
          onClick={(e) => {
            e.stopPropagation();
            onAvatarClick(parentMessage.senderAvatarUrl, displayName);
          }}
        >
          <Avatar className="h-6 w-6 cursor-pointer hover:opacity-80 transition-opacity">
            <AvatarImage src={parentMessage.senderAvatarUrl} />
            <AvatarFallback className="text-xs">{displayName.substring(0, 2).toUpperCase()}</AvatarFallback>
          </Avatar>
        </button>
        <span className="text-sm font-medium truncate">{displayName}</span>
        <span className="text-xs text-muted-foreground ml-auto shrink-0">{timeStr}</span>
      </div>
      {parentMessage.body && parentMessage.body.trim() !== "" && (
        <p className="text-sm text-muted-foreground line-clamp-2 pl-8">
          {parentMessage.body}
        </p>
      )}
      <div className="pl-8">
        <MessageThreadPreview
          threadMessages={replies}
          onThreadClick={() => {}}
          onAvatarClick={onAvatarClick}
        />
      </div>
    </button>
  );
}

function ThreadParentMessage({
  message,
  layout,
  providerInstanceId,
  onAvatarClick,
  t,
}: {
  message: models.Message;
  layout: "bubble" | "irc";
  providerInstanceId: string | undefined;
  onAvatarClick: (url: string | undefined, name?: string) => void;
  t: (key: string) => string;
}) {
  const displayName = getSenderDisplayName(message.senderName, message.senderId, message.isFromMe, t);
  const timestamp = timeToDate(message.timestamp);

  if (layout === "bubble") {
    return (
      <div className={cn("flex items-start gap-3", message.isFromMe && "justify-end")}>
        {!message.isFromMe && (
          <button onClick={() => onAvatarClick(message.senderAvatarUrl, displayName)} className="shrink-0">
            <Avatar className="h-6 w-6 cursor-pointer hover:opacity-80 transition-opacity">
              <AvatarImage src={message.senderAvatarUrl} />
              <AvatarFallback className="text-xs">{displayName.substring(0, 2).toUpperCase()}</AvatarFallback>
            </Avatar>
          </button>
        )}
        <div className={cn("rounded-lg p-2 text-left max-w-[80%]", message.isFromMe ? "bg-blue-600 text-white" : "bg-muted text-foreground")}>
          <MessageText text={message.body} providerInstanceId={providerInstanceId} emojiSize={14} isFromMe={message.isFromMe} />
          {message.attachments && message.attachments.trim() !== "" && (
            <MessageAttachments attachments={message.attachments} conversationID="" messageID={message.protocolMsgId || String(message.id ?? "")} isFromMe={message.isFromMe} layout="bubble" />
          )}
          <p className={cn("text-xs mt-1", message.isFromMe ? "text-blue-100" : "text-muted-foreground")}>
            {timestamp.toLocaleTimeString()}
          </p>
        </div>
        {message.isFromMe && (
          <button onClick={() => onAvatarClick(message.senderAvatarUrl, t("you"))} className="shrink-0">
            <Avatar className="h-6 w-6 cursor-pointer hover:opacity-80 transition-opacity">
              <AvatarImage src={message.senderAvatarUrl} />
              <AvatarFallback className="text-xs">{t("me")}</AvatarFallback>
            </Avatar>
          </button>
        )}
      </div>
    );
  }

  const senderColor = getColorFromString(message.senderId);
  const timeString = `${timestamp.getHours().toString().padStart(2, "0")}:${timestamp.getMinutes().toString().padStart(2, "0")}`;
  return (
    <div className="flex items-start">
      <div className="flex flex-col items-center min-w-[60px]">
        <button onClick={() => onAvatarClick(message.senderAvatarUrl, displayName)} className="shrink-0">
          <Avatar className="h-6 w-6 mt-2.5 cursor-pointer hover:opacity-80 transition-opacity">
            <AvatarImage src={message.senderAvatarUrl} />
            <AvatarFallback className="text-xs">{message.isFromMe ? t("me") : displayName.substring(0, 2).toUpperCase()}</AvatarFallback>
          </Avatar>
        </button>
        <span className="text-xs text-muted-foreground mt-1">{timeString}</span>
      </div>
      <div className="flex flex-col items-start ml-5 flex-1 min-w-0 text-left">
        <span className="font-semibold text-sm h-6 flex items-center mt-2.5" style={{ color: senderColor }}>{displayName}</span>
        <MessageText text={message.body} providerInstanceId={providerInstanceId} emojiSize={14} isFromMe={message.isFromMe} />
        {message.attachments && message.attachments.trim() !== "" && (
          <MessageAttachments attachments={message.attachments} conversationID="" messageID={message.protocolMsgId || String(message.id ?? "")} isFromMe={message.isFromMe} layout="irc" />
        )}
      </div>
    </div>
  );
}

export function ThreadView() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { toasts, showToast, closeToast } = useToast();

  const selectedThreadId = useAppStore((state) => state.selectedThreadId);
  const setSelectedThreadId = useAppStore((state) => state.setSelectedThreadId);
  const setShowThreads = useAppStore((state) => state.setShowThreads);
  const messageLayout = useAppStore((state) => state.messageLayout);
  const selectedThreadParentMessage = useAppStore((state) => state.selectedThreadParentMessage);
  const setSelectedAvatarUrl = useAppStore(
    (state) => state.setSelectedAvatarUrl
  );
  const selectedContact = useAppStore((state) => state.selectedContact);
  const capabilities = useAppStore((state) => state.capabilities);

  const activeAccount = selectedContact?.linkedAccounts[0];
  const protocol = activeAccount?.protocol;
  const providerInstanceId = activeAccount?.providerInstanceId;

  const conversationId =
    activeAccount?.conversationId ??
    activeAccount?.userId ??
    "";

  // Thread list navigation state
  const [threadOpenedFromList, setThreadOpenedFromList] = useState(false);
  const savedScrollTopRef = useRef(0);
  const shouldRestoreScrollRef = useRef(false);
  const threadListScrollRef = useRef<HTMLDivElement>(null);

  // Reset navigation state when conversation changes
  useEffect(() => {
    setThreadOpenedFromList(false);
    savedScrollTopRef.current = 0;
    shouldRestoreScrollRef.current = false;
  }, [conversationId]);

  // Restore thread list scroll position after going back
  useEffect(() => {
    if (selectedThreadId === null && shouldRestoreScrollRef.current) {
      shouldRestoreScrollRef.current = false;
      const savedTop = savedScrollTopRef.current;
      requestAnimationFrame(() => {
        if (threadListScrollRef.current) {
          threadListScrollRef.current.scrollTop = savedTop;
        }
      });
    }
  }, [selectedThreadId]);

  // Independent message accumulation for the thread list — never touches the shared
  // ["messages", conversationId] cache so MessageList is never disturbed.
  const [threadListMessages, setThreadListMessages] = useState<models.Message[]>([]);
  const [threadListHasMore, setThreadListHasMore] = useState(false);
  const [threadListIsFetching, setThreadListIsFetching] = useState(false);

  // Seed from the shared cache whenever the conversation changes, then diverge.
  useEffect(() => {
    const cached = queryClient.getQueryData<InfiniteData<models.Message[]>>(["messages", conversationId]);
    const flat = cached ? cached.pages.flat() : [];
    setThreadListMessages(flat);
    const lastPage = cached?.pages[cached.pages.length - 1];
    setThreadListHasMore(!!lastPage && lastPage.length >= 50);
  }, [conversationId, queryClient]);

  const fetchMoreThreadListMessages = useCallback(async () => {
    if (threadListIsFetching || !threadListHasMore || !conversationId) return;
    setThreadListIsFetching(true);
    try {
      const oldest = threadListMessages.length
        ? threadListMessages.reduce((a, b) =>
            timeToDate(a.timestamp) < timeToDate(b.timestamp) ? a : b
          )
        : null;
      const newMsgs = await fetchMessages(
        conversationId,
        oldest ? timeToDate(oldest.timestamp) : undefined
      );
      const seenIds = new Set(threadListMessages.map((m) => m.protocolMsgId).filter(Boolean));
      const deduped = newMsgs.filter((m) => !seenIds.has(m.protocolMsgId));
      setThreadListMessages((prev) => [...deduped, ...prev]);
      setThreadListHasMore(newMsgs.length >= 50);
    } catch (e) {
      console.error("Failed to load more messages for thread list", e);
    } finally {
      setThreadListIsFetching(false);
    }
  }, [conversationId, threadListMessages, threadListIsFetching, threadListHasMore]);

  const threadsParentList = useMemo(() => {
    const threadReplies: Record<string, models.Message[]> = {};
    threadListMessages.forEach((msg) => {
      const isReply = msg.threadId && msg.threadId !== msg.protocolMsgId;
      if (isReply && msg.threadId) {
        if (!threadReplies[msg.threadId]) threadReplies[msg.threadId] = [];
        threadReplies[msg.threadId].push(msg);
      }
    });
    return threadListMessages
      .filter(
        (msg) =>
          (!msg.threadId || msg.threadId === msg.protocolMsgId) &&
          msg.protocolMsgId &&
          (threadReplies[msg.protocolMsgId]?.length ?? 0) > 0
      )
      .map((msg) => ({
        parentMessage: msg,
        replies: (threadReplies[msg.protocolMsgId!] ?? []).sort(
          (a, b) => timeToDate(a.timestamp).getTime() - timeToDate(b.timestamp).getTime()
        ),
      }))
      .sort((a, b) => {
        const aLatest = Math.max(...a.replies.map((r) => timeToDate(r.timestamp).getTime()));
        const bLatest = Math.max(...b.replies.map((r) => timeToDate(r.timestamp).getTime()));
        return bLatest - aLatest;
      });
  }, [threadListMessages]);

  // Scroll-position preservation when older messages are prepended to the list
  const prevScrollMetricsRef = useRef<{ scrollTop: number; scrollHeight: number } | null>(null);

  // After prepend: compensate the layout shift so the viewport doesn't jump
  useEffect(() => {
    if (!prevScrollMetricsRef.current || !threadListScrollRef.current) return;
    const el = threadListScrollRef.current;
    const delta = el.scrollHeight - prevScrollMetricsRef.current.scrollHeight;
    if (delta > 0) el.scrollTop = prevScrollMetricsRef.current.scrollTop + delta;
    prevScrollMetricsRef.current = null;
  }, [threadsParentList]);

  // Auto-fill: keep fetching until the list overflows (user can then scroll to get more)
  useEffect(() => {
    if (selectedThreadId !== null) return;
    if (!threadListHasMore || threadListIsFetching) return;
    requestAnimationFrame(() => {
      const container = threadListScrollRef.current;
      if (!container) return;
      if (container.scrollHeight <= container.clientHeight) {
        prevScrollMetricsRef.current = { scrollTop: container.scrollTop, scrollHeight: container.scrollHeight };
        fetchMoreThreadListMessages();
      }
    });
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedThreadId, threadsParentList.length, threadListHasMore, threadListIsFetching]);

  const handleThreadListScroll = useCallback(() => {
    const el = threadListScrollRef.current;
    if (!el || !threadListHasMore || threadListIsFetching) return;
    if (el.scrollTop <= 80) {
      prevScrollMetricsRef.current = { scrollTop: el.scrollTop, scrollHeight: el.scrollHeight };
      fetchMoreThreadListMessages();
    }
  }, [threadListHasMore, threadListIsFetching, fetchMoreThreadListMessages]);

  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const shouldStickToThreadBottomRef = useRef(true);
  const threadBottomFrameRef = useRef<number | null>(null);
  const previousThreadMessageCountRef = useRef(0);

  const [replyingToMessage, setReplyingToMessage] = useState<models.Message | null>(null);
  const [openActionsMessageId, setOpenActionsMessageId] = useState<string | null>(null);
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [messageToDelete, setMessageToDelete] = useState<models.Message | null>(null);

  const handleClose = () => {
    setSelectedThreadId(null);
    setShowThreads(false);
    setThreadOpenedFromList(false);
    setReplyingToMessage(null);
  };

  const handleOpenThread = (messageId: string) => {
    savedScrollTopRef.current = threadListScrollRef.current?.scrollTop ?? 0;
    setThreadOpenedFromList(true);
    setSelectedThreadId(messageId);
  };

  const handleBackToList = () => {
    shouldRestoreScrollRef.current = true;
    setSelectedThreadId(null);
    setThreadOpenedFromList(false);
    setReplyingToMessage(null);
  };

  const handleAvatarClick = (avatarUrl: string | undefined, displayName?: string) => {
    const urlToShow = avatarUrl || (displayName ? `https://api.dicebear.com/7.x/initials/svg?seed=${encodeURIComponent(displayName)}` : null);
    if (urlToShow) {
      setSelectedAvatarUrl(urlToShow);
    }
  };

  const { data: threadMessages, isLoading } = useQuery<models.Message[], Error>({
    queryKey: ["threads", conversationId, selectedThreadId || ""],
    queryFn: () => {
      if (!selectedThreadId || !conversationId) {
        return Promise.resolve([]);
      }
      return fetchThreads(conversationId, selectedThreadId);
    },
    enabled: !!selectedThreadId && !!conversationId,
  });

  const markMultipleAsRead = useMessageReadStore((state) => state.markMultipleAsRead);

  const sortedThreadMessages = useMemo(() => {
    if (!threadMessages || threadMessages.length === 0) return [];
    const filtered = threadMessages.map(normalizeSlackQuotedReply).filter((msg) => {
      const hasBody = msg.body && msg.body.trim() !== "";
      const hasAttachments = msg.attachments && msg.attachments.trim() !== "";
      if (!hasBody && !hasAttachments) return false;
      // Exclude the parent message itself — it is displayed separately above the replies
      if (selectedThreadId && msg.protocolMsgId === selectedThreadId) return false;
      return true;
    });
    return [...filtered].sort(
      (a, b) =>
        timeToDate(a.timestamp).getTime() - timeToDate(b.timestamp).getTime()
    );
  }, [threadMessages, selectedThreadId]);

  const currentUserId = useMemo(() => {
    for (const msg of sortedThreadMessages) {
      if (msg.isFromMe && msg.senderId) return msg.senderId;
    }
    return undefined;
  }, [sortedThreadMessages]);

  const {
    editingMessageId,
    editingText,
    setEditingText,
    editingInputRef,
    handleEditMessage,
    handleSaveEdit,
    handleCancelEdit,
  } = useMessageEdit({ messages: sortedThreadMessages, conversationId, showToast, t });

  const handleDeleteClick = useCallback((message: models.Message) => {
    setMessageToDelete(message);
    setDeleteConfirmOpen(true);
  }, []);

  const handleConfirmDelete = useCallback(async () => {
    if (!messageToDelete || typeof DeleteMessage !== "function") return;
    const msgId = messageToDelete.protocolMsgId || getMessageDomId(messageToDelete);
    const targetConvId = messageToDelete.protocolConvId || conversationId;
    try {
      await DeleteMessage(targetConvId, msgId);
      setDeleteConfirmOpen(false);
      setMessageToDelete(null);
      queryClient.invalidateQueries({ queryKey: ["messages"] });
      queryClient.invalidateQueries({ queryKey: ["threads"] });
    } catch (error) {
      console.error("Failed to delete message:", error);
      showToast(t("delete_failed") || "Erreur lors de la suppression", "error");
    }
  }, [messageToDelete, conversationId, queryClient, showToast, t]);

  const handleReaction = useCallback(
    async (message: models.Message, emoji: string) => {
      const protocolMsgId = message.protocolMsgId || getMessageDomId(message);
      const messageReactions = message.reactions || [];
      const nativeEmojiReactions = providerInstanceId
        ? capabilities[providerInstanceId]?.nativeEmojiReactions ?? false
        : false;
      const { apiEmoji, canonicalName } = normalizeReaction(emoji, nativeEmojiReactions);

      const hasReaction = messageReactions.some((r) => {
        return reactionMatches(r.emoji, canonicalName) && (currentUserId ? r.userId === currentUserId : false);
      });

      try {
        if (hasReaction) {
          await RemoveReaction(conversationId, protocolMsgId, apiEmoji);
        } else {
          await AddReaction(conversationId, protocolMsgId, apiEmoji);
        }
        queryClient.invalidateQueries({ queryKey: ["threads", conversationId, selectedThreadId] });
        queryClient.invalidateQueries({ queryKey: ["messages", conversationId] });
      } catch (error) {
        console.error("Failed to update reaction:", error);
      }
    },
    [capabilities, conversationId, selectedThreadId, providerInstanceId, currentUserId, queryClient]
  );

  const scheduleThreadBottomCorrection = useCallback(() => {
    if (!shouldStickToThreadBottomRef.current || threadBottomFrameRef.current !== null) return;
    threadBottomFrameRef.current = requestAnimationFrame(() => {
      threadBottomFrameRef.current = null;
      const el = scrollContainerRef.current;
      if (!el || !shouldStickToThreadBottomRef.current) return;
      el.scrollTop = Math.max(0, el.scrollHeight - el.clientHeight);
    });
  }, []);

  // A newly opened thread starts at its live edge. Unlike a fixed timeout, this
  // intent remains active while media and other asynchronous content settle.
  useEffect(() => {
    if (!selectedThreadId) return;
    shouldStickToThreadBottomRef.current = true;
    previousThreadMessageCountRef.current = 0;
    scheduleThreadBottomCorrection();
  }, [selectedThreadId, scheduleThreadBottomCorrection]);

  useEffect(() => {
    const el = scrollContainerRef.current;
    if (!el || !selectedThreadId) return;

    const stopFollowingBottom = () => {
      shouldStickToThreadBottomRef.current = false;
    };
    const handleWheel = (event: WheelEvent) => {
      if (event.deltaY < 0) stopFollowingBottom();
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (["ArrowUp", "PageUp", "Home"].includes(event.key)) stopFollowingBottom();
    };
    const handlePointerDown = (event: PointerEvent) => {
      if (event.target === el) stopFollowingBottom();
    };
    const handleScroll = () => {
      const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
      if (distanceFromBottom <= 2) shouldStickToThreadBottomRef.current = true;
    };

    // The scroll container has a fixed viewport height, so observe both it and
    // its direct content wrappers. Their sizes change when images, audio
    // players, previews, reactions, or edited messages finish rendering.
    const resizeObserver = new ResizeObserver(scheduleThreadBottomCorrection);
    const observeContent = () => {
      resizeObserver.disconnect();
      resizeObserver.observe(el);
      Array.from(el.children).forEach((node) => resizeObserver.observe(node));
    };
    const mutationObserver = new MutationObserver(() => {
      observeContent();
      scheduleThreadBottomCorrection();
    });
    observeContent();
    mutationObserver.observe(el, { childList: true, subtree: true });

    el.addEventListener("wheel", handleWheel, { passive: true });
    el.addEventListener("touchmove", stopFollowingBottom, { passive: true });
    el.addEventListener("pointerdown", handlePointerDown, { passive: true });
    el.addEventListener("keydown", handleKeyDown);
    el.addEventListener("scroll", handleScroll, { passive: true });

    return () => {
      resizeObserver.disconnect();
      mutationObserver.disconnect();
      el.removeEventListener("wheel", handleWheel);
      el.removeEventListener("touchmove", stopFollowingBottom);
      el.removeEventListener("pointerdown", handlePointerDown);
      el.removeEventListener("keydown", handleKeyDown);
      el.removeEventListener("scroll", handleScroll);
    };
  }, [selectedThreadId, isLoading, scheduleThreadBottomCorrection]);

  useEffect(() => () => {
    if (threadBottomFrameRef.current !== null) {
      cancelAnimationFrame(threadBottomFrameRef.current);
    }
  }, []);

  useEffect(() => {
    const previousMessageCount = previousThreadMessageCountRef.current;
    previousThreadMessageCountRef.current = sortedThreadMessages.length;
    if (sortedThreadMessages.length === 0) return;
    const lastMsg = sortedThreadMessages[sortedThreadMessages.length - 1];
    // Sending a reply always returns to the live edge and keeps it pinned
    // through the optimistic/confirmed transition.
    if (
      sortedThreadMessages.length > previousMessageCount &&
      lastMsg?.isFromMe
    ) {
      shouldStickToThreadBottomRef.current = true;
    }
    scheduleThreadBottomCorrection();
  }, [sortedThreadMessages, scheduleThreadBottomCorrection]);

  useEffect(() => {
    if (!selectedThreadId || !conversationId || sortedThreadMessages.length === 0) return;
    const unreadIds = sortedThreadMessages
      .filter((msg) => !msg.isFromMe)
      .map((msg) => getMessageDomId(msg));
    if (unreadIds.length > 0) markMultipleAsRead(conversationId, unreadIds);
  }, [selectedThreadId, conversationId, sortedThreadMessages, markMultipleAsRead]);

  // Thread list view (no thread selected yet)
  if (!selectedThreadId) {
    return (
      <div className="thread-view flex flex-col h-full overflow-hidden">
        <div className="thread-view__header px-4 py-3 border-b flex items-center justify-between shrink-0">
          <h3 className="thread-view__title font-semibold text-base">{t("threads")}</h3>
          <Button variant="ghost" size="icon" className="h-8 w-8" onClick={handleClose}>
            <X className="h-4 w-4" />
          </Button>
        </div>
        <div
          ref={threadListScrollRef}
          onScroll={handleThreadListScroll}
          className="thread-view__list flex-1 overflow-y-auto min-h-0 scroll-area"
        >
          {threadListIsFetching && (
            <div className="flex justify-center py-3">
              <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
            </div>
          )}
          {threadsParentList.length === 0 && !threadListIsFetching ? (
            <div className="text-center text-muted-foreground py-8 px-4">
              {t("no_threads_in_conversation")}
            </div>
          ) : (
            <div className="thread-view__list-items">
              {threadsParentList.map(({ parentMessage, replies }) => (
                <ThreadListItem
                  key={parentMessage.protocolMsgId}
                  parentMessage={parentMessage}
                  replies={replies}
                  providerInstanceId={providerInstanceId}
                  onClick={() => handleOpenThread(parentMessage.protocolMsgId!)}
                  onAvatarClick={handleAvatarClick}
                />
              ))}
            </div>
          )}
        </div>
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className="thread-view flex flex-col h-full overflow-hidden">
        <div className="thread-view__header px-4 py-3 border-b flex items-center justify-between shrink-0">
          <div className="flex items-center gap-2 min-w-0">
            {threadOpenedFromList && (
              <Button variant="ghost" size="icon" className="h-8 w-8 shrink-0" onClick={handleBackToList} title={t("back_to_threads")}>
                <ArrowLeft className="h-4 w-4" />
              </Button>
            )}
            <h3 className="thread-view__title font-semibold text-base">Thread</h3>
          </div>
          <Button variant="ghost" size="icon" className="h-8 w-8" onClick={handleClose}>
            <X className="h-4 w-4" />
          </Button>
        </div>
        <div className="flex-1 flex items-center justify-center text-muted-foreground">
          {t("loading") || "Loading…"}
        </div>
      </div>
    );
  }

  return (
    <div className="thread-view flex flex-col h-full overflow-hidden">
      <div className="thread-view__header px-4 py-3 border-b flex items-center justify-between shrink-0">
        <div className="flex items-center gap-2 min-w-0">
          {threadOpenedFromList && (
            <Button variant="ghost" size="icon" className="h-8 w-8 shrink-0" onClick={handleBackToList} title={t("back_to_threads")}>
              <ArrowLeft className="h-4 w-4" />
            </Button>
          )}
          <h3 className="thread-view__title font-semibold text-base">Thread</h3>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="h-8 w-8"
          onClick={handleClose}
        >
          <X className="h-4 w-4" />
        </Button>
      </div>
      <div ref={scrollContainerRef} className="flex-1 overflow-y-auto p-4 min-h-0 scroll-area">
        {selectedThreadParentMessage && (
          <div className="thread-view__parent mb-4 pb-4 border-b">
            <ThreadParentMessage
              message={selectedThreadParentMessage}
              layout={messageLayout}
              providerInstanceId={providerInstanceId}
              onAvatarClick={handleAvatarClick}
              t={t}
            />
            <div className="thread-view__reply-count flex items-center gap-2 mt-3 text-xs text-muted-foreground">
              <div className="flex-1 h-px bg-border" />
              <span>{sortedThreadMessages.length} {sortedThreadMessages.length === 1 ? t("reply") || "reply" : t("replies") || "replies"}</span>
              <div className="flex-1 h-px bg-border" />
            </div>
          </div>
        )}
        {sortedThreadMessages.length === 0 ? (
          <div className="text-center text-muted-foreground py-8">
            No messages in this thread
          </div>
        ) : messageLayout === "bubble" ? (
          <div className="space-y-4">
            {sortedThreadMessages.map((message) => {
              const messageId = getMessageDomId(message);
              const displayName = getSenderDisplayName(message.senderName, message.senderId, message.isFromMe, t);
              return (
                <div
                  key={message.protocolMsgId || `thread-${message.id}`}
                  className={cn("flex items-start gap-3 group relative", message.isFromMe && "justify-end")}
                  onMouseEnter={() => setOpenActionsMessageId(messageId)}
                  onMouseLeave={() => setOpenActionsMessageId(null)}
                >
                  {!message.isFromMe && (
                    <button
                      onClick={() => handleAvatarClick(message.senderAvatarUrl, displayName)}
                      className="shrink-0"
                    >
                      <Avatar className="h-6 w-6 cursor-pointer hover:opacity-80 transition-opacity">
                        <AvatarImage src={message.senderAvatarUrl} />
                        <AvatarFallback className="text-xs">
                          {displayName.substring(0, 2).toUpperCase()}
                        </AvatarFallback>
                      </Avatar>
                    </button>
                  )}
                  <div className="flex flex-col items-start gap-1 relative group/bubble">
                    {editingMessageId !== messageId && (
                      <div
                        className={cn(
                          "opacity-0 group-hover/bubble:opacity-100 transition-opacity mb-1",
                          openActionsMessageId === messageId ? "opacity-100" : "",
                          message.isFromMe ? "self-end" : "self-start"
                        )}
                      >
                        <MessageActions
                          isFromMe={message.isFromMe}
                          hasAttachments={Boolean(message.attachments?.trim())}
                          onEdit={() => handleEditMessage(message)}
                          onDelete={() => handleDeleteClick(message)}
                          onReply={() => setReplyingToMessage(message)}
                          onReact={(emoji) => handleReaction(message, emoji)}
                          currentReactions={(message.reactions || []).filter((r) => currentUserId ? r.userId === currentUserId : false).map((r) => r.emoji)}
                          messageId={messageId}
                          openActionsMessageId={openActionsMessageId}
                          provider={protocol}
                          instanceId={providerInstanceId}
                        />
                      </div>
                    )}
                    <div
                      className={`rounded-lg p-2 text-left ${message.isFromMe
                          ? "bg-blue-600 text-white"
                          : "bg-muted text-foreground"
                        }`}
                    >
                      {editingMessageId === messageId ? (
                        <div className="flex flex-col gap-2">
                          <Input
                            ref={editingInputRef}
                            value={editingText}
                            onChange={(e) => setEditingText(e.target.value)}
                            onKeyDown={(e) => {
                              if (e.key === "Enter" && !e.shiftKey) {
                                e.preventDefault();
                                handleSaveEdit(false);
                              } else if (e.key === "Escape") {
                                handleCancelEdit();
                              }
                            }}
                            className="text-foreground"
                            autoFocus
                          />
                          <div className="flex gap-2 justify-end">
                            <button
                              onClick={handleCancelEdit}
                              className="text-xs px-2 py-1 rounded hover:bg-muted"
                            >
                              {t("cancel")}
                            </button>
                            <button
                              onClick={() => handleSaveEdit(false)}
                              className="text-xs px-2 py-1 rounded bg-primary text-primary-foreground hover:bg-primary/90"
                            >
                              {t("save")}
                            </button>
                          </div>
                        </div>
                      ) : (
                        <>
                          {message.quotedMessageId && message.quotedBody && (
                            <div
                              className={cn(
                                "mb-2 pl-3 pr-2 py-1.5 border-l-[3px] rounded-r transition-colors text-left",
                                message.isFromMe
                                  ? "border-white/70 bg-black/10 hover:bg-black/20"
                                  : "border-purple-600 dark:border-purple-400 bg-muted/40 hover:bg-muted/70"
                              )}
                            >
                              <div
                                className={cn(
                                  "text-xs font-semibold mb-0.5 text-left flex items-center gap-1.5",
                                  message.isFromMe ? "text-white/90" : "text-purple-700 dark:text-purple-400"
                                )}
                              >
                                {message.quotedSenderName || (message.isFromMe ? t("you") : t("contact"))}
                              </div>
                              <div className={cn("text-xs md:text-sm line-clamp-2 text-left", message.isFromMe ? "text-white/80" : "text-foreground/80")}>
                                <MessageText text={message.quotedBody} providerInstanceId={providerInstanceId} emojiSize={14} isFromMe={message.isFromMe} />
                              </div>
                            </div>
                          )}
                          <MessageText
                            text={message.body}
                            providerInstanceId={providerInstanceId}
                            emojiSize={14}
                            isFromMe={message.isFromMe}
                          />
                          {message.attachments && message.attachments.trim() !== "" && (
                            <MessageAttachments
                              attachments={message.attachments}
                              conversationID={conversationId}
                              messageID={message.protocolMsgId || String(message.id ?? "")}
                              isFromMe={message.isFromMe}
                              layout="bubble"
                            />
                          )}
                          <p
                            className={`text-xs mt-1 text-left ${message.isFromMe ? "text-blue-100" : "text-muted-foreground"
                              }`}
                          >
                            {timeToDate(message.timestamp).toLocaleTimeString()}
                          </p>
                        </>
                      )}
                    </div>
                    {message.reactions && message.reactions.length > 0 && (
                      <MessageReactions
                        reactions={message.reactions}
                        isGroup={false}
                        currentUserId={currentUserId}
                        providerInstanceId={providerInstanceId}
                        allMessages={sortedThreadMessages}
                        onReactionClick={(emoji) => handleReaction(message, emoji)}
                        className="mt-1"
                      />
                    )}
                  </div>
                  {message.isFromMe && (
                    <button
                      onClick={() => handleAvatarClick(message.senderAvatarUrl, t("you"))}
                      className="shrink-0"
                    >
                      <Avatar className="h-6 w-6 cursor-pointer hover:opacity-80 transition-opacity">
                        <AvatarImage src={message.senderAvatarUrl} />
                        <AvatarFallback className="text-xs">{t("me")}</AvatarFallback>
                      </Avatar>
                    </button>
                  )}
                </div>
              );
            })}
          </div>
        ) : (
          <div className="space-y-1">
            {sortedThreadMessages.map((message, index) => {
              const messageId = getMessageDomId(message);
              const prevMessage = index > 0 ? sortedThreadMessages[index - 1] : null;
              const timestamp = timeToDate(message.timestamp);
              const prevTimestamp = prevMessage ? timeToDate(prevMessage.timestamp) : null;
              const timeDiffMinutes = prevTimestamp
                ? (timestamp.getTime() - prevTimestamp.getTime()) / (1000 * 60)
                : Infinity;
              const showSender =
                !prevMessage ||
                prevMessage.senderId !== message.senderId ||
                prevMessage.isFromMe !== message.isFromMe ||
                timeDiffMinutes >= 5;
              const displayName = getSenderDisplayName(message.senderName, message.senderId, message.isFromMe, t);
              const senderColor = getColorFromString(message.senderId);
              const timeString = `${timestamp.getHours().toString().padStart(2, "0")}:${timestamp.getMinutes().toString().padStart(2, "0")}`;

              return (
                <div
                  key={message.protocolMsgId || `thread-${message.id}`}
                  className="space-y-1 group relative"
                  onMouseEnter={() => setOpenActionsMessageId(messageId)}
                  onMouseLeave={() => setOpenActionsMessageId(null)}
                >
                  {editingMessageId !== messageId && (
                    <div
                      className={cn(
                        "absolute right-2 top-1 z-10 transition-opacity",
                        openActionsMessageId === messageId ? "opacity-100" : "opacity-0 pointer-events-none"
                      )}
                    >
                      <MessageActions
                        isFromMe={message.isFromMe}
                        hasAttachments={Boolean(message.attachments?.trim())}
                        onEdit={() => handleEditMessage(message)}
                        onDelete={() => handleDeleteClick(message)}
                        onReply={() => setReplyingToMessage(message)}
                        onReact={(emoji) => handleReaction(message, emoji)}
                        currentReactions={(message.reactions || []).filter((r) => currentUserId ? r.userId === currentUserId : false).map((r) => r.emoji)}
                        messageId={messageId}
                        openActionsMessageId={openActionsMessageId}
                        provider={protocol}
                        instanceId={providerInstanceId}
                      />
                    </div>
                  )}
                  <div className="flex items-start py-1">
                    <div className="flex flex-col items-center min-w-[60px]">
                      {showSender ? (
                        <>
                          <button
                            onClick={() => handleAvatarClick(message.senderAvatarUrl, displayName)}
                            className="shrink-0"
                          >
                            <Avatar className="h-6 w-6 mt-2.5 cursor-pointer hover:opacity-80 transition-opacity">
                              <AvatarImage src={message.senderAvatarUrl} />
                              <AvatarFallback className="text-xs">
                                {message.isFromMe ? t("me") : displayName.substring(0, 2).toUpperCase()}
                              </AvatarFallback>
                            </Avatar>
                          </button>
                          <span className="text-xs text-muted-foreground mt-1">{timeString}</span>
                        </>
                      ) : (
                        <span className="text-xs text-muted-foreground leading-none" style={{ marginTop: '10px' }}>{timeString}</span>
                      )}
                    </div>
                    <div className="flex flex-col items-start ml-5 flex-1 min-w-0 text-left">
                      {editingMessageId === messageId ? (
                        <div className="flex flex-col gap-2 w-full mt-2">
                          <Input
                            ref={editingInputRef}
                            value={editingText}
                            onChange={(e) => setEditingText(e.target.value)}
                            onKeyDown={(e) => {
                              if (e.key === "Enter" && !e.shiftKey) {
                                e.preventDefault();
                                handleSaveEdit(false);
                              } else if (e.key === "Escape") {
                                handleCancelEdit();
                              }
                            }}
                            className="text-foreground"
                            autoFocus
                          />
                          <div className="flex gap-2 justify-end">
                            <button
                              onClick={handleCancelEdit}
                              className="text-xs px-2 py-1 rounded hover:bg-muted"
                            >
                              {t("cancel")}
                            </button>
                            <button
                              onClick={() => handleSaveEdit(false)}
                              className="text-xs px-2 py-1 rounded bg-primary text-primary-foreground hover:bg-primary/90"
                            >
                              {t("save")}
                            </button>
                          </div>
                        </div>
                      ) : showSender ? (
                        <>
                          <span
                            className="font-semibold text-sm text-left h-6 flex items-center mt-2.5"
                            style={{ color: senderColor }}
                          >
                            {displayName}
                          </span>
                          <div className="text-left">
                            {message.quotedMessageId && message.quotedBody && (
                              <div className="mb-2 pl-3 pr-2 py-1.5 border-l-[3px] border-purple-600 dark:border-purple-400 bg-muted/40 hover:bg-muted/70 rounded-r transition-colors text-left">
                                <div className="text-xs font-semibold text-purple-700 dark:text-purple-400 mb-0.5 text-left flex items-center gap-1.5">
                                  {message.quotedSenderName || (message.isFromMe ? t("you") : t("contact"))}
                                </div>
                                <div className="text-xs md:text-sm text-foreground/80 line-clamp-2 text-left">
                                  <MessageText text={message.quotedBody} providerInstanceId={providerInstanceId} emojiSize={14} isFromMe={message.isFromMe} />
                                </div>
                              </div>
                            )}
                            <MessageText
                              text={message.body}
                              providerInstanceId={providerInstanceId}
                              emojiSize={14}
                              isFromMe={message.isFromMe}
                            />
                            {message.attachments && message.attachments.trim() !== "" && (
                              <MessageAttachments
                                attachments={message.attachments}
                                conversationID={conversationId}
                                messageID={message.protocolMsgId || String(message.id ?? "")}
                                isFromMe={message.isFromMe}
                                layout="irc"
                              />
                            )}
                          </div>
                        </>
                      ) : (
                        <div className="text-left">
                          <MessageText
                            text={message.body}
                            providerInstanceId={providerInstanceId}
                            emojiSize={14}
                            isFromMe={message.isFromMe}
                          />
                          {message.attachments && message.attachments.trim() !== "" && (
                            <MessageAttachments
                              attachments={message.attachments}
                              conversationID={conversationId}
                              messageID={message.protocolMsgId || String(message.id ?? "")}
                              isFromMe={message.isFromMe}
                              layout="irc"
                            />
                          )}
                        </div>
                      )}
                      {message.reactions && message.reactions.length > 0 && (
                        <MessageReactions
                          reactions={message.reactions}
                          isGroup={false}
                          currentUserId={currentUserId}
                          providerInstanceId={providerInstanceId}
                          allMessages={sortedThreadMessages}
                          onReactionClick={(emoji) => handleReaction(message, emoji)}
                          className="mt-1"
                        />
                      )}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>
      <div className="border-t shrink-0">
        <ChatInput
          key={`thread-${conversationId}-${selectedThreadId}`}
          onFileUploadRequest={() => { }}
          replyingToMessage={replyingToMessage}
          onCancelReply={() => setReplyingToMessage(null)}
          threadId={selectedThreadId || undefined}
        />
      </div>

      <AlertDialog open={deleteConfirmOpen} onOpenChange={setDeleteConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("delete_message_title") || "Supprimer le message"}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("delete_message_description") || "Voulez-vous vraiment supprimer ce message ? Cette action me permettra de masquer ou supprimer ce message."}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleConfirmDelete}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {t("delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <ToastContainer toasts={toasts} onClose={closeToast} />
    </div>
  );
}
