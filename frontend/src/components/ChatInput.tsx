import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Bold, ChevronDown, Code, Italic, Link, List, ListOrdered, Paperclip, Send, Smile, Strikethrough, Underline, X } from "lucide-react";
import { GetCustomEmojis, GetGroupDetails, GetGroupParticipants, GetParticipantNames, ScheduleMessage, SendMessage, SendMessageWithMentions, SendReply, SendThreadMessage, SendThreadReply, SendTypingIndicator } from "../../wailsjs/go/main/App";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Suspense, lazy, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ToastContainer, useToast } from "@/components/ui/toast";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { flushSync } from "react-dom";

import { Button } from "@/components/ui/button";
import type { InfiniteData } from "@tanstack/react-query";
import { ScheduledMessagesDialog } from "@/components/ScheduledMessagesDialog";
import type { EmojiClickData, Theme } from "emoji-picker-react";
import { cn } from "@/lib/utils";
import { htmlFragmentToText } from "@/lib/messageUtils";
import { core, models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useTranslation } from "react-i18next";
import { orderCustomEmojis, prepareEmojiSuggestions, recordCustomEmojiUsage, recordStandardEmojiUsage } from "@/lib/emojiUsage";

interface ChatInputProps {
  onFileUploadRequest?: (files: File[], filePaths?: string[]) => void;
  replyingToMessage?: models.Message | null;
  onCancelReply?: () => void;
  onNavigateToEdit?: (direction: "up" | "down", returnFocusToInput?: () => void) => void;
  threadId?: string;
  currentUserName?: string;
  currentUserAvatarUrl?: string;
  onHeightChange?: () => void;
  onTextareaMount?: (textarea: HTMLTextAreaElement | null) => void;
}

// emoji-picker-react carries a large emoji dataset. Do not retain it in the
// renderer until the user actually opens the composer picker.
const EmojiPicker = lazy(() => import("emoji-picker-react"));

const normalizeClipboardPath = (rawValue: string | null): string | null => {
  if (!rawValue) {
    return null;
  }

  const trimmed = rawValue.trim();
  if (!trimmed) {
    return null;
  }

  // Handle file:// URLs (e.g., from Maccy, Raycast, etc.)
  if (trimmed.toLowerCase().startsWith("file://")) {
    try {
      const url = new URL(trimmed);
      let pathname = decodeURIComponent(url.pathname);

      // On Windows, pathname can be like /C:/Users/... -> remove leading slash
      if (/^\/[A-Za-z]:/.test(pathname)) {
        pathname = pathname.substring(1);
      }

      return pathname;
    } catch (err) {
      console.warn("Failed to parse file:// URL from clipboard:", trimmed, err);
      return trimmed.replace(/^file:\/\//i, "");
    }
  }

  return trimmed;
};

const extractPathsFromText = (text: string | null): string[] => {
  if (!text) {
    return [];
  }

  return text
    .split(/\r?\n/)
    .map((entry) => normalizeClipboardPath(entry))
    .filter(
      (normalizedPath): normalizedPath is string =>
        Boolean(
          normalizedPath &&
          (normalizedPath.startsWith("/") ||
            normalizedPath.match(/^[A-Za-z]:[\\/]/)) &&
          normalizedPath.match(/\.[a-zA-Z0-9]+$/)
        )
    );
};

// Matches the internal CustomEmoji type expected by emoji-picker-react
interface CustomEmoji {
  id: string;
  names: string[];
  imgUrl: string;
}

const DRAFT_STORAGE_PREFIX = "loom_chat_draft";

const getDraftStorageKey = (conversationId?: string, providerInstanceId?: string, threadId?: string): string | null => {
  if (!conversationId) {
    return null;
  }

  return [DRAFT_STORAGE_PREFIX, providerInstanceId || "default", conversationId, threadId || "main"]
    .map(encodeURIComponent)
    .join(":");
};

const readDraft = (key: string | null): string => {
  if (!key || typeof window === "undefined") {
    return "";
  }

  try {
    return window.localStorage.getItem(key) || "";
  } catch {
    return "";
  }
};

const saveDraft = (key: string | null, value: string): void => {
  if (!key || typeof window === "undefined") {
    return;
  }

  try {
    if (value === "") {
      window.localStorage.removeItem(key);
    } else {
      window.localStorage.setItem(key, value);
    }
  } catch {
    // A draft is non-critical; ignore unavailable or full localStorage.
  }
};

export function ChatInput({ onFileUploadRequest, replyingToMessage, onCancelReply, onNavigateToEdit, threadId, currentUserName, currentUserAvatarUrl, onHeightChange, onTextareaMount }: ChatInputProps) {
  const { t, i18n } = useTranslation();
  const { toasts, showToast, closeToast } = useToast();
  const [isEmojiPickerOpen, setIsEmojiPickerOpen] = useState(false);
  const [isScheduledMessagesOpen, setIsScheduledMessagesOpen] = useState(false);
  const [isScheduleMenuOpen, setIsScheduleMenuOpen] = useState(false);
  const [customEmojiCatalog, setCustomEmojiCatalog] = useState<{ instanceId: string; emojis: CustomEmoji[] }>({
    instanceId: "",
    emojis: [],
  });
  const [isDragging, setIsDragging] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const textareaMeasurementRef = useRef<HTMLTextAreaElement | null>(null);
  const draftSaveRef = useRef<{ key: string | null; value: string; timeoutId: number } | null>(null);
  const hasTextRef = useRef(false);
  const [textSelection, setTextSelection] = useState<{ start: number; end: number } | null>(null);
  const [isLinkEditorOpen, setIsLinkEditorOpen] = useState(false);
  const [linkUrl, setLinkUrl] = useState("https://");
  const fileInputRef = useRef<HTMLInputElement>(null);
  const selectedContact = useAppStore((state) => state.selectedContact);
  const selectedProviderFilter = useAppStore((state) => state.selectedProviderFilter);
  const theme = useAppStore((state) => state.theme);
  const capabilities = useAppStore((state) => state.capabilities);
  const metaContacts = useAppStore((state) => state.metaContacts);
  const setIsTypingInInput = useAppStore((state) => state.setIsTypingInInput);
  const showThreads = useAppStore((state) => state.showThreads);
  const selectedThreadId = useAppStore((state) => state.selectedThreadId);
  const isThreadOpen = !threadId && showThreads && selectedThreadId !== null;
  const activeAccount = useMemo(() => {
    const accounts = selectedContact?.linkedAccounts ?? [];
    return (
      (selectedProviderFilter
        ? accounts.find(
            (account) => account.providerInstanceId === selectedProviderFilter
          )
        : undefined) ?? accounts[0]
    );
  }, [selectedContact, selectedProviderFilter]);

  const conversationId =
    activeAccount?.conversationId ||
    (activeAccount?.providerInstanceId && activeAccount?.userId
      ? `${activeAccount.providerInstanceId}::${activeAccount.userId}`
      : activeAccount?.userId);
  const customEmojis = customEmojiCatalog.instanceId === activeAccount?.providerInstanceId
    ? customEmojiCatalog.emojis
    : [];
  const supportsScheduledMessages = activeAccount?.providerInstanceId
    ? capabilities[activeAccount.providerInstanceId]?.supportsScheduledMessages ?? false
    : false;
  const supportsListScheduledMessages = activeAccount?.providerInstanceId
    ? capabilities[activeAccount.providerInstanceId]?.supportsListScheduledMessages ?? false
    : false;
  const supportsTypingIndicator = activeAccount?.providerInstanceId
    ? capabilities[activeAccount.providerInstanceId]?.supportsTypingIndicator ?? false
    : false;
  const { data: groupDetails } = useQuery<models.GroupDetails>({
    queryKey: ["group-details", conversationId],
    queryFn: () => GetGroupDetails(conversationId ?? ""),
    enabled: Boolean(activeAccount?.isGroup && conversationId),
    refetchInterval: 15000,
  });
  const canSendMessages = !activeAccount?.isGroup || groupDetails?.canSendMessages !== false;
  const draftStorageKey = useMemo(
    () => getDraftStorageKey(conversationId, activeAccount?.providerInstanceId, threadId),
    [conversationId, activeAccount?.providerInstanceId, threadId]
  );
  const [message, setMessage] = useState(() => readDraft(draftStorageKey));
  const [mentions, setMentions] = useState<core.Mention[]>([]);
  const [mentionCursor, setMentionCursor] = useState(0);
  const queryClient = useQueryClient();
  const { data: mentionParticipantData = [] } = useQuery({
    queryKey: ["composer-mention-participants", conversationId],
    enabled: Boolean(activeAccount?.isGroup && conversationId),
    queryFn: async () => {
      const participants = await GetGroupParticipants(conversationId ?? "");
      const visible = (participants ?? []).filter((participant) => !participant.isSelf && participant.userId);
      const names = visible.length ? await GetParticipantNames(visible.map((participant) => participant.userId)) : {};
      return visible.map((participant) => ({
        userId: participant.userId,
        displayName: names?.[participant.userId]?.trim() || participant.userId,
      }));
    },
    staleTime: 15000,
  });
  const mentionParticipants = useMemo(() => {
    const cachedMessages = queryClient.getQueryData<InfiniteData<models.Message[]>>(["messages", conversationId]);
    return mentionParticipantData.map((participant) => {
      const linkedAccount = selectedContact?.linkedAccounts?.find((account) => account.userId === participant.userId)
        ?? metaContacts.flatMap((contact) => contact.linkedAccounts ?? []).find((account) => account.userId === participant.userId);
      const senderMessage = cachedMessages?.pages.flat().find((item) =>
        item.senderId === participant.userId && Boolean(item.senderAvatarUrl)
      );
      return { ...participant, avatarUrl: linkedAccount?.avatarUrl || senderMessage?.senderAvatarUrl };
    });
  }, [conversationId, mentionParticipantData, metaContacts, queryClient, selectedContact]);
  const mentionMatch = useMemo(() => {
    const cursor = mentionCursor;
    const prefix = message.slice(0, cursor);
    const match = prefix.match(/(^|\s)@([^@\n]*)$/);
    if (!match) return null;
    return { start: cursor - match[2].length - 1, query: match[2].toLocaleLowerCase() };
  }, [message, mentionCursor]);
  const mentionSuggestions = useMemo(() => mentionMatch
    ? mentionParticipants.filter((participant) => participant.displayName.toLocaleLowerCase().includes(mentionMatch.query))
    : [], [mentionMatch, mentionParticipants]);
  const [activeMentionIndex, setActiveMentionIndex] = useState(0);
  useEffect(() => setActiveMentionIndex(0), [mentionMatch?.query]);

  const updateTypingState = useCallback((hasText: boolean) => {
    if (hasTextRef.current === hasText) return;
    hasTextRef.current = hasText;
    setIsTypingInInput(hasText);
  }, [setIsTypingInInput]);

  const hasTypingText = message.trim().length > 0;
  useEffect(() => {
    if (!conversationId || !supportsTypingIndicator || !hasTypingText) return;

    const sendTyping = () => {
      void SendTypingIndicator(conversationId, true).catch((error) => {
        console.warn("Failed to send typing indicator:", error);
      });
    };
    sendTyping();
    const refreshTimer = window.setInterval(sendTyping, 4000);
    return () => {
      window.clearInterval(refreshTimer);
      void SendTypingIndicator(conversationId, false).catch((error) => {
        console.warn("Failed to clear typing indicator:", error);
      });
    };
  }, [conversationId, hasTypingText, supportsTypingIndicator]);

  const scheduleDraftSave = useCallback((key: string | null, value: string) => {
    if (draftSaveRef.current) {
      window.clearTimeout(draftSaveRef.current.timeoutId);
    }
    const timeoutId = window.setTimeout(() => {
      saveDraft(key, value);
      if (draftSaveRef.current?.timeoutId === timeoutId) draftSaveRef.current = null;
    }, 250);
    draftSaveRef.current = { key, value, timeoutId };
  }, []);

  const saveDraftImmediately = useCallback((key: string | null, value: string) => {
    if (draftSaveRef.current) {
      window.clearTimeout(draftSaveRef.current.timeoutId);
      draftSaveRef.current = null;
    }
    saveDraft(key, value);
  }, []);

  useEffect(() => () => {
    const pending = draftSaveRef.current;
    if (!pending) return;
    window.clearTimeout(pending.timeoutId);
    saveDraft(pending.key, pending.value);
    draftSaveRef.current = null;
  }, [draftStorageKey]);

  // Focus textarea when a conversation is selected
  useEffect(() => {
    if (selectedContact) {
      // Small delay to ensure the component is fully rendered
      const timeoutId = setTimeout(() => {
        textareaRef.current?.focus();
      }, 100);
      return () => clearTimeout(timeoutId);
    }
  }, [selectedContact]);

  const sendMessageMutation = useMutation({
    mutationFn: async ({ conversationId, text, quotedMessageId, mentions }: { conversationId: string; text: string; quotedMessageId?: string; mentions: core.Mention[] }) => {
      if (mentions.length > 0) {
        return await SendMessageWithMentions(conversationId, text, mentions, threadId || "", quotedMessageId || "");
      }
      if (threadId) {
        if (quotedMessageId) {
          return await SendThreadReply(conversationId, text, threadId, quotedMessageId);
        }
        return await SendThreadMessage(conversationId, text, threadId);
      }
      if (quotedMessageId) {
        return await SendReply(conversationId, text, quotedMessageId);
      }
      return await SendMessage(conversationId, text);
    },
    // Optimistic update: insert temp message immediately
    onMutate: ({ conversationId, text, quotedMessageId }) => {
      const tempId = `temp-${Date.now()}-${Math.random().toString(16).slice(2)}`;
      const now = new Date();

      // A thread reply is conversation activity too. Move the parent conversation
      // immediately, even though the reply itself lives in the thread cache.
      if (threadId) {
        const previousTimestamp = queryClient.getQueryData<Record<string, string | number | null>>(
          ["allLastMessageTimestamps"]
        )?.[conversationId];
        queryClient.setQueryData<Record<string, string | number | null>>(
          ["allLastMessageTimestamps"],
          (old) => ({ ...(old || {}), [conversationId]: now.toISOString() })
        );
        return { tempId, conversationId, isThreadMessage: true, previousTimestamp };
      }

      // Get current user info from existing messages, fall back to props passed from MessageList
      let currentUserInfo: { senderId?: string; senderName?: string; senderAvatarUrl?: string } = {
        senderName: currentUserName,
        senderAvatarUrl: currentUserAvatarUrl,
      };
      const existingData = queryClient.getQueryData<InfiniteData<models.Message[]>>(["messages", conversationId]);
      if (existingData?.pages) {
        for (const page of existingData.pages) {
          for (const msg of page) {
            if (msg.isFromMe && msg.senderId) {
              currentUserInfo = {
                senderId: msg.senderId,
                senderName: msg.senderName || currentUserName,
                senderAvatarUrl: msg.senderAvatarUrl || currentUserAvatarUrl,
              };
              break;
            }
          }
          if (currentUserInfo.senderId) break;
        }
      }

      const optimisticMessage: any = {
        protocolMsgId: tempId,
        protocolConvId: conversationId,
        body: text,
        timestamp: now.toISOString(),
        isFromMe: true,
        isPending: true,
        sendFailed: false,
        quotedMessageId: quotedMessageId,
        quotedBody: replyingToMessage?.body
          ? htmlFragmentToText(replyingToMessage.body)
          : undefined,
        quotedSenderName: replyingToMessage?.senderName ?? undefined,
        quotedSenderId: replyingToMessage?.senderId ?? undefined,
        senderId: currentUserInfo.senderId,
        senderName: currentUserInfo.senderName,
        senderAvatarUrl: currentUserInfo.senderAvatarUrl,
      };

      // Update messages cache (append to last page to keep chronological order)
      queryClient.setQueryData<InfiniteData<models.Message[]>>(
        ["messages", conversationId],
        (oldData) => {
          const safeData: InfiniteData<models.Message[]> = oldData && Array.isArray(oldData.pages)
            ? {
                pages: oldData.pages.map((p) => (Array.isArray(p) ? [...p] : [])),
                pageParams: Array.isArray(oldData.pageParams) ? [...oldData.pageParams] : [],
              }
            : { pages: [], pageParams: [] };

          if (safeData.pages.length === 0) {
            safeData.pages.push([]);
          }

          // Append to the last page and deduplicate by protocolMsgId
          const lastIndex = safeData.pages.length - 1;
          const lastPage = [...(safeData.pages[lastIndex] || [])];
          const seen = new Set(lastPage.map((m) => m.protocolMsgId).filter(Boolean));
          if (!seen.has(tempId)) {
            lastPage.push(optimisticMessage as models.Message);
          }
          safeData.pages[lastIndex] = lastPage;
          return safeData;
        }
      );

      // Update last message caches for preview (allLastMessages / allLastMessageTimestamps)
      queryClient.setQueryData<Record<string, models.Message | null>>(
        ["allLastMessages"],
        (old) => {
          const updated = { ...(old || {}) };
          updated[conversationId] = optimisticMessage as models.Message;
          return updated;
        }
      );
      queryClient.setQueryData<Record<string, string | null>>(
        ["allLastMessageTimestamps"],
        (old) => {
          const updated = { ...(old || {}) };
          updated[conversationId] = now.toISOString();
          return updated;
        }
      );

      return { tempId, conversationId, isThreadMessage: false };
    },
    onSuccess: (message, variables, context) => {
      const conversationId = variables.conversationId;
      const tempId = context?.tempId;
      const isThreadMessage = context?.isThreadMessage;

      // If we sent to an actual thread (threadId prop set), invalidate and refetch the thread cache
      if (isThreadMessage) {
        console.log(`[ChatInput] Sent message to thread ${threadId}, invalidating thread cache`);
        if (message?.protocolMsgId && threadId) {
          queryClient.setQueryData<models.Message[]>(
            ["threads", conversationId, threadId],
            (current = []) => current.some((item) => item.protocolMsgId === message.protocolMsgId)
              ? current
              : [...current, message]
          );
          queryClient.setQueriesData<models.ThreadSummary[]>(
            { queryKey: ["thread-summaries", conversationId] },
            (current) => current?.map((summary) => summary.parentMessageId === threadId
              ? models.ThreadSummary.createFrom({ ...summary, replyCount: summary.replyCount + 1 })
              : summary)
          );
        }
        if (message?.timestamp) {
          queryClient.setQueryData<Record<string, string | number | null>>(
            ["allLastMessageTimestamps"],
            (old) => ({ ...(old || {}), [conversationId]: message.timestamp as unknown as string })
          );
          queryClient.setQueryData<Record<string, models.Message | null>>(
            ["allLastMessages"],
            (old) => ({ ...(old || {}), [conversationId]: message })
          );
        }
        queryClient.invalidateQueries({ queryKey: ["threads", conversationId, threadId] });
        queryClient.refetchQueries({ queryKey: ["threads", conversationId, threadId] });
        queryClient.invalidateQueries({ queryKey: ["thread-summaries", conversationId] });
        // Also invalidate main messages to update thread count badge
        queryClient.invalidateQueries({ queryKey: ["messages", conversationId] });
        return; // Don't do optimistic update for thread messages
      }

      // Replace temp message with real one (only for main messages)
      queryClient.setQueryData<InfiniteData<models.Message[]>>(
        ["messages", conversationId],
        (oldData) => {
          if (!oldData || !Array.isArray(oldData.pages)) {
            return oldData;
          }
          const pages = oldData.pages.map((page) => {
            if (!Array.isArray(page)) return page;
            return page.map((msg) => {
              if (msg.protocolMsgId === tempId) {
                return { ...(message as any), isPending: false, sendFailed: false };
              }
              return msg;
            });
          });
          return { ...oldData, pages };
        }
      );

      // Update last message caches
      queryClient.setQueryData<Record<string, models.Message | null>>(
        ["allLastMessages"],
        (old) => {
          const updated = { ...(old || {}) };
          updated[conversationId] = { ...(message as any), isPending: false, sendFailed: false };
          return updated;
        }
      );
      queryClient.setQueryData<Record<string, string | null>>(
        ["allLastMessageTimestamps"],
        (old) => {
          const updated = { ...(old || {}) };
          updated[conversationId] = message.timestamp as unknown as string;
          return updated;
        }
      );
    },
    onError: (_error, variables, context) => {
      const conversationId = variables.conversationId;
      const tempId = context?.tempId;
      if (context?.isThreadMessage) {
        queryClient.setQueryData<Record<string, string | number | null>>(
          ["allLastMessageTimestamps"],
          (old) => {
            const updated = { ...(old || {}) };
            if (context.previousTimestamp == null) {
              delete updated[conversationId];
            } else {
              updated[conversationId] = context.previousTimestamp;
            }
            return updated;
          }
        );
        return;
      }
      // Mark temp message as failed
      queryClient.setQueryData<InfiniteData<models.Message[]>>(
        ["messages", conversationId],
        (oldData) => {
          if (!oldData || !Array.isArray(oldData.pages)) {
            return oldData;
          }
          const pages = oldData.pages.map((page) => {
            if (!Array.isArray(page)) return page;
            return page.map((msg) => {
              if (msg.protocolMsgId === tempId) {
                return { ...(msg as any), isPending: false, sendFailed: true };
              }
              return msg;
            });
          });
          return { ...oldData, pages };
        }
      );
    },
  });

  const scheduleMessageMutation = useMutation({
    mutationFn: ({ convId, text, scheduledAt, parentMsgId }: {
      convId: string;
      text: string;
      scheduledAt: Date;
      parentMsgId: string;
    }) => ScheduleMessage(convId, text, scheduledAt.toISOString(), parentMsgId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["scheduled-messages", conversationId] });
    },
  });

  const schedulePresets = useMemo(() => {
    const now = new Date();
    const presets: Array<{ label: string; date: Date }> = [];
    for (const hour of [13, 18]) {
      const date = new Date(now);
      date.setHours(hour, 0, 0, 0);
      if (date.getTime() > now.getTime() + 30 * 60 * 1000) {
        const timeStr = new Intl.DateTimeFormat(i18n.language, { hour: "2-digit", minute: "2-digit", hour12: false }).format(date);
        presets.push({ label: t("schedule_later_today", { time: timeStr }), date });
      }
    }
    const tomorrow = new Date(now);
    tomorrow.setDate(tomorrow.getDate() + 1);
    tomorrow.setHours(9, 0, 0, 0);
    presets.push({ label: t("schedule_tomorrow"), date: tomorrow });
    const daysUntilMonday = (1 + 7 - now.getDay()) % 7 || 7;
    if (daysUntilMonday > 1) {
      const nextMonday = new Date(now);
      nextMonday.setDate(now.getDate() + daysUntilMonday);
      nextMonday.setHours(9, 0, 0, 0);
      presets.push({ label: t("schedule_monday"), date: nextMonday });
    }
    return presets;
  }, [t, i18n.language]);

  const handleSchedulePreset = async (date: Date) => {
    if (!conversationId || !message.trim()) return;
    setIsScheduleMenuOpen(false);
    const text = message.trim();
    try {
      await scheduleMessageMutation.mutateAsync({ convId: conversationId, text, scheduledAt: date, parentMsgId: threadId ?? "" });
      setMessage("");
      saveDraftImmediately(draftStorageKey, "");
      updateTypingState(false);
      if (onCancelReply) onCancelReply();
      const dateLabel = date.toLocaleString(i18n.language, { dateStyle: "medium", timeStyle: "short" });
      showToast(t("scheduled_message_badge", { date: dateLabel }), "success");
    } catch (err) {
      const raw = err instanceof Error ? err.message : String(err);
      const msg = raw.includes("not_allowed_token_type")
        ? t("schedule_not_allowed_token")
        : raw;
      showToast(msg, "error");
    }
  };

  // Auto-resize textarea based on content
  const adjustTextareaHeight = useCallback(() => {
    const textarea = textareaRef.current;
    if (textarea) {
      const previousHeight = textarea.offsetHeight;
      const computedStyle = window.getComputedStyle(textarea);
      const minHeight = Number.parseFloat(computedStyle.minHeight) || 40;
      const maxHeight = 200; // Maximum height in pixels

      // Measure on an invisible clone. Setting the real textarea to "auto"
      // before reading scrollHeight makes it collapse and expand, which also
      // moves the adjacent message viewport twice (especially on Backspace).
      // Keep the measurement node around. Creating, attaching and removing a
      // clone synchronously for every input event delays the browser from
      // painting the character that was just typed.
      const measurement = textareaMeasurementRef.current ?? (textarea.cloneNode() as HTMLTextAreaElement);
      if (!textareaMeasurementRef.current) {
        measurement.tabIndex = -1;
        measurement.setAttribute("aria-hidden", "true");
        document.body.appendChild(measurement);
        textareaMeasurementRef.current = measurement;
      }
      measurement.value = textarea.value;
      Object.assign(measurement.style, {
        position: "fixed",
        left: "-10000px",
        top: "0",
        visibility: "hidden",
        pointerEvents: "none",
        width: `${textarea.offsetWidth}px`,
        height: "auto",
        minHeight: "0",
        maxHeight: "none",
        overflow: "hidden",
      });
      const contentHeight = measurement.scrollHeight;

      const newHeight = Math.max(minHeight, Math.min(contentHeight, maxHeight));
      textarea.style.height = `${newHeight}px`;
      if (textarea.offsetHeight !== previousHeight) {
        // The textarea is resized imperatively, so notify the message viewport
        // in the same task. Waiting for ResizeObserver exposes one incorrect
        // frame when the composer crosses a line boundary.
        onHeightChange?.();
      }
    }
  }, [onHeightChange]);

  useEffect(() => () => {
    textareaMeasurementRef.current?.remove();
    textareaMeasurementRef.current = null;
  }, []);

  const mountTextarea = useCallback((textarea: HTMLTextAreaElement | null) => {
    textareaRef.current = textarea;
    onTextareaMount?.(textarea);
  }, [onTextareaMount]);

  // Restore the draft associated with the currently displayed conversation/thread.
  useEffect(() => {
    const draft = readDraft(draftStorageKey);
    setMessage(draft);
    updateTypingState(draft.trim().length > 0);
    requestAnimationFrame(adjustTextareaHeight);
  }, [adjustTextareaHeight, draftStorageKey, updateTypingState]);

  const handleMessageChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const newValue = e.target.value;
    let prefix = 0;
    while (prefix < message.length && prefix < newValue.length && message[prefix] === newValue[prefix]) prefix++;
    let oldSuffix = message.length;
    let newSuffix = newValue.length;
    while (oldSuffix > prefix && newSuffix > prefix && message[oldSuffix - 1] === newValue[newSuffix - 1]) { oldSuffix--; newSuffix--; }
    const delta = (newSuffix - prefix) - (oldSuffix - prefix);
    // Shift mentions around an edit and discard only those whose visible token
    // was actually touched.
    setMentions((current) => current.flatMap((mention) => {
      const end = mention.start + mention.length;
      const shifted = oldSuffix <= mention.start ? { ...mention, start: mention.start + delta } : mention;
      if (prefix < end && oldSuffix > mention.start) return [];
      return newValue.slice(shifted.start, shifted.start + shifted.length) === `@${shifted.displayName}` ? [shifted] : [];
    }));
    setMessage(newValue);
    setMentionCursor(e.target.selectionStart);
    scheduleDraftSave(draftStorageKey, newValue);
    adjustTextareaHeight();

    // Hide unread divider when user starts typing
    updateTypingState(newValue.trim().length > 0);
  };

  const chooseMention = useCallback((participant: { userId: string; displayName: string }) => {
    if (!mentionMatch) return;
    const token = `@${participant.displayName}`;
    const next = message.slice(0, mentionMatch.start) + token + " " + message.slice(mentionCursor);
    setMessage(next);
    setMentions((current) => [...current, { userId: participant.userId, displayName: participant.displayName, start: mentionMatch.start, length: token.length }]);
    setMentionCursor(mentionMatch.start + token.length + 1);
    scheduleDraftSave(draftStorageKey, next);
    requestAnimationFrame(() => {
      textareaRef.current?.focus();
      textareaRef.current?.setSelectionRange(mentionMatch.start + token.length + 1, mentionMatch.start + token.length + 1);
    });
  }, [draftStorageKey, mentionCursor, mentionMatch, message, scheduleDraftSave]);

  const updateTextSelection = useCallback(() => {
    const textarea = textareaRef.current;
    if (!textarea || textarea.selectionStart === textarea.selectionEnd) {
      setTextSelection(null);
      return;
    }
    setTextSelection({ start: textarea.selectionStart, end: textarea.selectionEnd });
  }, []);

  const replaceSelection = useCallback((before: string, after = before) => {
    const textarea = textareaRef.current;
    if (!textarea || !textSelection) return;
    const { start, end } = textSelection;
    const selected = message.slice(start, end);
    const nextMessage = message.slice(0, start) + before + selected + after + message.slice(end);
    setMessage(nextMessage);
    saveDraftImmediately(draftStorageKey, nextMessage);
    updateTypingState(nextMessage.trim().length > 0);
    setTextSelection(null);
    requestAnimationFrame(() => {
      textarea.focus();
      textarea.setSelectionRange(start + before.length, end + before.length);
      adjustTextareaHeight();
    });
  }, [adjustTextareaHeight, draftStorageKey, message, saveDraftImmediately, textSelection, updateTypingState]);

  const formatSelectedLines = useCallback((ordered: boolean) => {
    const textarea = textareaRef.current;
    if (!textarea || !textSelection) return;
    const { start, end } = textSelection;
    const lineStart = message.lastIndexOf("\n", start - 1) + 1;
    const nextLineBreak = message.indexOf("\n", end);
    const lineEnd = nextLineBreak === -1 ? message.length : nextLineBreak;
    const selectedLines = message.slice(lineStart, lineEnd).split("\n");
    const formatted = selectedLines
      .map((line, index) => `${ordered ? `${index + 1}.` : "-"} ${line.replace(/^\s*(?:[-+*]|\d+[.)])\s+/, "")}`)
      .join("\n");
    const nextMessage = message.slice(0, lineStart) + formatted + message.slice(lineEnd);
    setMessage(nextMessage);
    saveDraftImmediately(draftStorageKey, nextMessage);
    updateTypingState(true);
    setTextSelection(null);
    requestAnimationFrame(() => {
      textarea.focus();
      textarea.setSelectionRange(lineStart, lineStart + formatted.length);
      adjustTextareaHeight();
    });
  }, [adjustTextareaHeight, draftStorageKey, message, saveDraftImmediately, textSelection, updateTypingState]);

  const openLinkEditor = useCallback(() => {
    if (!textSelection) return;
    setLinkUrl("https://");
    setIsLinkEditorOpen(true);
  }, [textSelection]);

  const addLink = useCallback(() => {
    const url = linkUrl.trim();
    if (!/^https?:\/\/\S+$/i.test(url)) return;
    setIsLinkEditorOpen(false);
    replaceSelection("[", `](${url})`);
  }, [linkUrl, replaceSelection]);
  const makeBold = useCallback(() => replaceSelection("**"), [replaceSelection]);
  const makeItalic = useCallback(() => replaceSelection("*"), [replaceSelection]);
  const makeUnderline = useCallback(() => replaceSelection("<u>", "</u>"), [replaceSelection]);
  const makeStrikethrough = useCallback(() => replaceSelection("~~"), [replaceSelection]);
  const makeCodeBlock = useCallback(() => replaceSelection("```\n", "\n```"), [replaceSelection]);
  const makeBulletedList = useCallback(() => formatSelectedLines(false), [formatSelectedLines]);
  const makeNumberedList = useCallback(() => formatSelectedLines(true), [formatSelectedLines]);

  const handleSendMessage = async () => {
    if (message.trim() && selectedContact) {
      const text = message.trim();
      const leadingWhitespace = message.length - message.trimStart().length;
      const quotedMessageId = replyingToMessage?.protocolMsgId;
      // Commit the cleared composer before the optimistic cache update. React
      // otherwise batches both operations until this handler returns, so the
      // cache/list work can make the typed text linger visibly after Enter.
      flushSync(() => setMessage(""));
      const sentMentions = mentions
        .filter((mention) => mention.start >= leadingWhitespace && mention.start + mention.length <= leadingWhitespace + text.length)
        .map((mention) => ({ ...mention, start: mention.start - leadingWhitespace }));
      setMentions([]);
      saveDraftImmediately(draftStorageKey, "");
      updateTypingState(false); // Reset typing state after sending
      if (textareaRef.current) {
        const previousHeight = textareaRef.current.offsetHeight;
        const minHeight =
          Number.parseFloat(window.getComputedStyle(textareaRef.current).minHeight) || 40;
        textareaRef.current.style.height = `${minHeight}px`;
        if (previousHeight !== textareaRef.current.offsetHeight) onHeightChange?.();
      }
      // Clear reply state after sending
      if (onCancelReply) {
        onCancelReply();
      }
      try {
        await sendMessageMutation.mutateAsync({
          conversationId: conversationId ?? "",
          text,
          quotedMessageId,
          mentions: sentMentions,
        });
      } catch (error) {
        // Error handling is done in onError
        console.error("Failed to send message:", error);
      }
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (mentionMatch && mentionSuggestions.length > 0) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        setActiveMentionIndex((index) => (index + (e.key === "ArrowDown" ? 1 : -1) + mentionSuggestions.length) % mentionSuggestions.length);
        return;
      }
      if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        chooseMention(mentionSuggestions[activeMentionIndex]);
        return;
      }
      if (e.key === "Escape") { setMentionCursor(-1); return; }
    }
    if (textSelection && (e.metaKey || e.ctrlKey)) {
      const key = e.key.toLowerCase();
      const formattingShortcut =
        (!e.shiftKey && key === "b" && makeBold) ||
        (!e.shiftKey && key === "i" && makeItalic) ||
        (!e.shiftKey && key === "u" && makeUnderline) ||
        (!e.shiftKey && key === "k" && openLinkEditor) ||
        (e.shiftKey && key === "x" && makeStrikethrough) ||
        (e.shiftKey && key === "c" && makeCodeBlock) ||
        (e.shiftKey && key === "7" && makeNumberedList) ||
        (e.shiftKey && key === "8" && makeBulletedList);

      if (formattingShortcut) {
        e.preventDefault();
        formattingShortcut();
        return;
      }
    }

    // Handle navigation to edit previous/next message
    if (e.key === "ArrowUp" && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
      // Only navigate if cursor is at the start of the textarea or textarea is empty
      const textarea = textareaRef.current;
      const canNavigate = textarea && (textarea.selectionStart === 0 || message.trim() === "");
      if (canNavigate) {
        e.preventDefault();
        if (onNavigateToEdit) {
          onNavigateToEdit("up", () => {
            setTimeout(() => {
              textareaRef.current?.focus();
            }, 0);
          });
        }
        return;
      }
    }

    if (e.key === "ArrowDown" && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
      // Only navigate if cursor is at the end of the textarea
      const textarea = textareaRef.current;
      const canNavigate = textarea && (textarea.selectionStart === textarea.value.length || message.trim() === "");
      if (canNavigate) {
        e.preventDefault();
        if (onNavigateToEdit) {
          onNavigateToEdit("down", () => {
            setTimeout(() => {
              textareaRef.current?.focus();
            }, 0);
          });
        }
        return;
      }
    }

    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSendMessage();
    }
    // Shift+Enter allows new line
  };

  // Fetch custom emojis when the picker opens
  useEffect(() => {
    if (!isEmojiPickerOpen || !selectedContact) return;
    prepareEmojiSuggestions();
    if (customEmojis.length > 0) return;

    const instanceId = activeAccount?.providerInstanceId;
    if (!instanceId) return;

    GetCustomEmojis(instanceId)
      .then((emojiMap: Record<string, string>) => {
        if (!emojiMap) return;
        const emojis: CustomEmoji[] = Object.entries(emojiMap).map(([name, url]) => ({
          id: name,
          names: [name],
          imgUrl: url,
        }));
        setCustomEmojiCatalog({ instanceId, emojis: orderCustomEmojis(instanceId, emojis) });
      })
      .catch(() => {
        // Silently ignore
      });
  }, [isEmojiPickerOpen, selectedContact, activeAccount, customEmojis.length]);

  const handleEmojiClick = (emojiData: EmojiClickData) => {
    if (emojiData.isCustom) {
      if (activeAccount?.providerInstanceId) {
        recordCustomEmojiUsage(activeAccount.providerInstanceId, emojiData.unified);
        setCustomEmojiCatalog((catalog) => ({
          instanceId: activeAccount.providerInstanceId,
          emojis: orderCustomEmojis(
            activeAccount.providerInstanceId,
            catalog.instanceId === activeAccount.providerInstanceId ? catalog.emojis : []
          ),
        }));
      }
    } else {
      recordStandardEmojiUsage(emojiData.unified, emojiData.unifiedWithoutSkinTone);
    }
    const emojiText = emojiData.isCustom ? `:${emojiData.unified}:` : emojiData.emoji;
    setMessage((prev) => {
      const newMessage = prev + emojiText;
      saveDraftImmediately(draftStorageKey, newMessage);
      return newMessage;
    });
    updateTypingState(true);
    setIsEmojiPickerOpen(false);
    // Adjust height after emoji is added
    setTimeout(adjustTextareaHeight, 0);
  };

  const handleFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files.length > 0) {
      const files = Array.from(e.target.files);
      if (onFileUploadRequest) {
        onFileUploadRequest(files);
      }
      // Reset file input
      if (fileInputRef.current) {
        fileInputRef.current.value = "";
      }
    }
  };

  // Handle paste event for files
  const handlePaste = useCallback((e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    // Persist event for async operations (React synthetic events)
    if (typeof e.persist === "function") {
      e.persist();
    }

    const clipboardData = e.clipboardData;
    const files: File[] = [];
    const filePaths: string[] = [];
    const asyncPathPromises: Promise<void>[] = [];

    // First, check the text that would be pasted (synchronous access)
    // In Wails, the pasted text might contain the file path
    const pastedText = clipboardData.getData("text/plain");
    if (pastedText) {
      const paths = extractPathsFromText(pastedText);
      if (paths.length > 0) {
        filePaths.push(...paths);
      }
    }

    const uriList = clipboardData.getData("text/uri-list");
    if (uriList) {
      const paths = extractPathsFromText(uriList);
      if (paths.length > 0) {
        filePaths.push(...paths);
      }
    }

    // Check clipboardData.files (most direct access)
    if (clipboardData.files && clipboardData.files.length > 0) {
      const fileList = Array.from(clipboardData.files);
      files.push(...fileList);
    }

    // Also check clipboardData.items for file items and paths
    if (clipboardData.items && clipboardData.items.length > 0) {
      for (let i = 0; i < clipboardData.items.length; i++) {
        const item = clipboardData.items[i];

        // Check for file type
        if (item.kind === "file") {
          const file = item.getAsFile();
          if (file) {
            // Avoid duplicates
            if (!files.some(f => f.name === file.name && f.size === file.size && f.lastModified === file.lastModified)) {
              files.push(file);
            }
          }
        }
        // Check for text that might be a file path (async, but we already checked synchronously above)
        else if (item.kind === "string") {
          asyncPathPromises.push(
            new Promise<void>((resolve) => {
              item.getAsString((text) => {
                const paths = extractPathsFromText(text);
                paths.forEach((path) => {
                  if (!filePaths.includes(path)) {
                    filePaths.push(path);
                  }
                });

                resolve();
              });
            })
          );
        }
      }
    }

    const finalizeUpload = () => {
      if (files.length > 0 || filePaths.length > 0) {
        e.preventDefault();
        if (onFileUploadRequest) {
          onFileUploadRequest(files, filePaths.length > 0 ? filePaths : undefined);
        }
      }
    };

    if (asyncPathPromises.length > 0) {
      Promise.all(asyncPathPromises).then(finalizeUpload);
    } else {
      finalizeUpload();
    }
  }, [onFileUploadRequest]);

  // Handle drag and drop
  const handleDragEnter = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (e.dataTransfer.types.includes("Files")) {
      setIsDragging(true);
    }
  }, []);

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    // Only set dragging to false if we're leaving the drop zone
    if (!e.currentTarget.contains(e.relatedTarget as Node)) {
      setIsDragging(false);
    }
  }, []);

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
  }, []);

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragging(false);

    const files = Array.from(e.dataTransfer.files);
    if (files.length > 0 && onFileUploadRequest) {
      onFileUploadRequest(files);
    }
  }, [onFileUploadRequest]);

  const hasMessage = message.trim().length > 0;

  // Get sender display name for reply preview
  const getSenderDisplayName = (message: models.Message): string => {
    if (message.isFromMe) return t("you") || "You";
    if (message.senderName && message.senderName.trim().length > 0) {
      return message.senderName;
    }
    return message.senderId;
  };

  if (!canSendMessages) {
    return (
      <div className="border-t p-4 text-center text-sm text-muted-foreground">
        {groupDetails?.isMember === false ? t("group_left_read_only") : t("conversation_read_only")}
      </div>
    );
  }

  return (
    <>
    <div className="flex flex-col">
      {supportsScheduledMessages && conversationId && (
        <ScheduledMessagesDialog
          open={isScheduledMessagesOpen}
          onOpenChange={setIsScheduledMessagesOpen}
          conversationId={conversationId}
          draft={message}
          supportsListScheduledMessages={supportsListScheduledMessages}
          onScheduled={() => {
            setMessage("");
            saveDraftImmediately(draftStorageKey, "");
            updateTypingState(false);
          }}
        />
      )}
      {/* Reply preview */}
      {replyingToMessage && (
        <div className="px-4 pt-3 pb-2 border-t bg-muted/30 flex items-center gap-3">
          <div className="flex-1 flex items-center gap-2 min-w-0">
            <Avatar className="h-6 w-6 shrink-0">
              <AvatarImage src={replyingToMessage.senderAvatarUrl} />
              <AvatarFallback className="text-xs">
                {getSenderDisplayName(replyingToMessage).substring(0, 2).toUpperCase()}
              </AvatarFallback>
            </Avatar>
            <div className="flex-1 min-w-0 text-left">
              <div className="text-xs font-medium text-muted-foreground text-left">
                {t("replying_to")} {getSenderDisplayName(replyingToMessage)}
              </div>
              <div className="text-sm text-foreground truncate text-left">
                {(() => {
                  const body = htmlFragmentToText(replyingToMessage.body || "");
                  if (body && body.trim().length > 0) {
                    return body.length > 50 ? `${body.substring(0, 50)}...` : body;
                  }

                  // Check for voice message in attachments
                  if (replyingToMessage.attachments) {
                    try {
                      const atts = JSON.parse(replyingToMessage.attachments);
                      if (Array.isArray(atts) && atts.length > 0 && atts[0].type === "voice") {
                        return "🎤 " + t("voice_message");
                      }
                    } catch (e) {
                      // ignore parse error
                    }
                  }

                  return t("empty_message");
                })()}
              </div>
            </div>
          </div>
          <Button
            variant="ghost"
            size="icon"
            className="h-6 w-6 shrink-0"
            onClick={onCancelReply}
            title={t("cancel_reply")}
          >
            <X className="h-4 w-4" />
          </Button>
        </div>
      )}

      {/* Message input */}
      <div
        className={cn(
          "p-4 border-t flex items-end space-x-2 transition-colors",
          isDragging && "bg-muted/50",
          replyingToMessage && "border-t-0"
        )}
        onDragEnter={handleDragEnter}
        onDragLeave={handleDragLeave}
        onDragOver={handleDragOver}
        onDrop={handleDrop}
      >
        <div className="flex items-center space-x-2 flex-1">
          <Button
            variant="ghost"
            size="icon"
            onClick={() => fileInputRef.current?.click()}
            className="shrink-0"
            title={t("attach_files")}
          >
            <Paperclip className="h-5 w-5" />
          </Button>
          <input
            ref={fileInputRef}
            type="file"
            multiple
            className="hidden"
            onChange={handleFileSelect}
          />

          <Popover open={isEmojiPickerOpen} onOpenChange={setIsEmojiPickerOpen}>
            <PopoverTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="shrink-0"
                title={t("add_emoji")}
              >
                <Smile className="h-5 w-5" />
              </Button>
            </PopoverTrigger>
            <PopoverContent className="w-auto p-0 border-0" align="start">
              <Suspense fallback={<div className="w-[352px] h-[435px]" />}>
                <EmojiPicker
                  onEmojiClick={handleEmojiClick}
                  customEmojis={customEmojis}
                  theme={(theme === "dark" ? "dark" : "light") as Theme}
                  width={352}
                  height={435}
                  lazyLoadEmojis
                />
              </Suspense>
            </PopoverContent>
          </Popover>

          <div className="relative flex-1">
            {mentionMatch && mentionSuggestions.length > 0 && (
              <div role="listbox" aria-label={t("mention_participant")} className="absolute bottom-full left-0 z-30 mb-1 max-h-[408px] w-72 overflow-y-auto rounded-md border bg-popover p-1 text-popover-foreground shadow-md">
                {mentionSuggestions.map((participant, index) => (
                  <button key={participant.userId} type="button" role="option" aria-selected={index === activeMentionIndex}
                    className={cn("flex h-10 w-full items-center gap-2 rounded px-2 text-left text-sm", index === activeMentionIndex && "bg-accent")}
                    onMouseDown={(event) => { event.preventDefault(); chooseMention(participant); }}>
                    <Avatar className="h-7 w-7 shrink-0">
                      <AvatarImage src={participant.avatarUrl} alt={participant.displayName} />
                      <AvatarFallback className="text-[10px]">
                        {participant.displayName.substring(0, 2).toUpperCase()}
                      </AvatarFallback>
                    </Avatar>
                    <span className="truncate">{participant.displayName}</span>
                  </button>
                ))}
              </div>
            )}
            {textSelection && (
              <div
                className="absolute bottom-full left-1/2 z-20 mb-1 flex -translate-x-1/2 items-center rounded-md border bg-popover p-1 text-popover-foreground shadow-md"
                onMouseDown={(event) => {
                  if (!(event.target instanceof HTMLInputElement)) {
                    event.preventDefault();
                  }
                }}
              >
                {isLinkEditorOpen ? (
                  <form
                    className="flex items-center gap-1"
                    onSubmit={(event) => {
                      event.preventDefault();
                      addLink();
                    }}
                  >
                    <input
                      autoFocus
                      type="url"
                      value={linkUrl}
                      aria-label={t("format_link_prompt")}
                      className="h-7 w-56 rounded border border-input bg-background px-2 text-xs outline-none focus:ring-1 focus:ring-ring"
                      onChange={(event) => setLinkUrl(event.target.value)}
                      onKeyDown={(event) => {
                        if (event.key === "Escape") {
                          setIsLinkEditorOpen(false);
                          textareaRef.current?.focus();
                        }
                      }}
                    />
                    <Button type="submit" size="sm" className="h-7 px-2 text-xs" disabled={!/^https?:\/\/\S+$/i.test(linkUrl.trim())}>
                      {t("format_link_apply")}
                    </Button>
                  </form>
                ) : (
                  <>
                    <button type="button" title={t("format_bold")} aria-label={t("format_bold")} className="rounded p-1.5 hover:bg-accent" onClick={makeBold}><Bold className="h-4 w-4" /></button>
                    <button type="button" title={t("format_italic")} aria-label={t("format_italic")} className="rounded p-1.5 hover:bg-accent" onClick={makeItalic}><Italic className="h-4 w-4" /></button>
                    <button type="button" title={t("format_underline")} aria-label={t("format_underline")} className="rounded p-1.5 hover:bg-accent" onClick={makeUnderline}><Underline className="h-4 w-4" /></button>
                    <button type="button" title={t("format_strikethrough")} aria-label={t("format_strikethrough")} className="rounded p-1.5 hover:bg-accent" onClick={makeStrikethrough}><Strikethrough className="h-4 w-4" /></button>
                    <button type="button" title={t("format_code_block")} aria-label={t("format_code_block")} className="rounded p-1.5 hover:bg-accent" onClick={makeCodeBlock}><Code className="h-4 w-4" /></button>
                    <button type="button" title={t("format_link")} aria-label={t("format_link")} className="rounded p-1.5 hover:bg-accent" onClick={openLinkEditor}><Link className="h-4 w-4" /></button>
                    <button type="button" title={t("format_bulleted_list")} aria-label={t("format_bulleted_list")} className="rounded p-1.5 hover:bg-accent" onClick={makeBulletedList}><List className="h-4 w-4" /></button>
                    <button type="button" title={t("format_numbered_list")} aria-label={t("format_numbered_list")} className="rounded p-1.5 hover:bg-accent" onClick={makeNumberedList}><ListOrdered className="h-4 w-4" /></button>
                  </>
                )}
              </div>
            )}
            <textarea
              ref={mountTextarea}
              value={message}
              onChange={handleMessageChange}
              onKeyDown={handleKeyDown}
              onPaste={handlePaste}
              onSelect={updateTextSelection}
              onClick={(event) => setMentionCursor(event.currentTarget.selectionStart)}
              onBlur={() => {
                if (!isLinkEditorOpen) setTextSelection(null);
              }}
              disabled={isThreadOpen}
              placeholder={t("type_a_message")}
              className="block w-full min-h-[40px] max-h-[200px] resize-none rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
              rows={1}
              autoCorrect="off"
              autoComplete="off"
              spellCheck="false"
            />
          </div>
        </div>

        {hasMessage && (
          supportsScheduledMessages ? (
            <div className="chat-input__schedule-send flex shrink-0">
              <Button onClick={handleSendMessage} size="icon" className="chat-input__send-button rounded-r-none" title={t("send")}>
                <Send className="h-5 w-5" />
              </Button>
              <Popover open={isScheduleMenuOpen} onOpenChange={setIsScheduleMenuOpen}>
                <PopoverTrigger asChild>
                  <Button size="icon" className="chat-input__schedule-chevron w-6 rounded-l-none border-l border-l-primary-foreground/20" title={t("schedule_menu_title")}>
                    <ChevronDown className="h-3.5 w-3.5" />
                  </Button>
                </PopoverTrigger>
                <PopoverContent side="top" align="end" className="w-52 p-1">
                  <p className="px-2 py-1.5 text-xs font-semibold text-muted-foreground">{t("schedule_menu_title")}</p>
                  {schedulePresets.map((preset) => (
                    <button
                      key={preset.label}
                      type="button"
                      className="w-full rounded-sm px-2 py-1.5 text-left text-sm hover:bg-accent"
                      onClick={() => handleSchedulePreset(preset.date)}
                    >
                      {preset.label}
                    </button>
                  ))}
                  <div className="my-1 border-t" />
                  <button
                    type="button"
                    className="w-full rounded-sm px-2 py-1.5 text-left text-sm hover:bg-accent"
                    onClick={() => { setIsScheduleMenuOpen(false); setIsScheduledMessagesOpen(true); }}
                  >
                    {t("schedule_custom_time")}
                  </button>
                </PopoverContent>
              </Popover>
            </div>
          ) : (
            <Button onClick={handleSendMessage} size="icon" className="shrink-0" title={t("send")}>
              <Send className="h-5 w-5" />
            </Button>
          )
        )}
      </div>
    </div>
    <ToastContainer toasts={toasts} onClose={closeToast} />
    </>
  );
}
