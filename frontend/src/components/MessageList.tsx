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
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { DeleteMessage, EditMessage, GetMessagesForConversation, GetMessagesForConversationBefore, SendFile } from "../../wailsjs/go/main/App";
import { ToastContainer, useToast } from "@/components/ui/toast";
import { cn, timeToDate } from "@/lib/utils";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";

import { ChatInput } from "./ChatInput";
import { FileUploadModal } from "./FileUploadModal";
import { Input } from "@/components/ui/input";
import type { KeyboardEvent } from "react";
import { MessageActions } from "./MessageActions";
import { MessageAttachments } from "./MessageAttachments";
import { MessageHeader } from "./MessageHeader";
import { MessageStatus } from "./MessageStatus";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useTranslation } from "react-i18next";

// Declare SendFileFromPath as it will be available after Wails bindings are regenerated
declare const SendFileFromPath: ((conversationID: string, filePath: string) => Promise<models.Message>) | undefined;

async function compressImageFile(file: File): Promise<File> {
  const isImage = file.type?.startsWith("image/");
  const shouldCompress = isImage && file.size > 1024 * 1024; // compress files > 1MB
  if (!shouldCompress) {
    return file;
  }

  try {
    const imageBitmap = await createImageBitmap(file);
    let { width, height } = imageBitmap;
    const maxDimension = Math.max(width, height);
    const targetMax = 1600;
    const scale = maxDimension > targetMax ? targetMax / maxDimension : 1;
    width = Math.round(width * scale);
    height = Math.round(height * scale);

    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    const ctx = canvas.getContext("2d");
    if (!ctx) {
      imageBitmap.close();
      return file;
    }
    ctx.drawImage(imageBitmap, 0, 0, width, height);
    imageBitmap.close();

    const blob = await new Promise<Blob | null>((resolve) =>
      canvas.toBlob((result) => resolve(result), "image/jpeg", 0.85)
    );

    if (!blob) {
      return file;
    }

    return new File(
      [blob],
      file.name.replace(/\.(png|webp)$/i, ".jpg"),
      { type: "image/jpeg", lastModified: Date.now() }
    );
  } catch (error) {
    console.warn("Image compression failed, sending original file.", error);
    return file;
  }
}

// Generate a deterministic color from a string (username)
function getColorFromString(str: string): string {
  let hash = 0;
  for (let i = 0; i < str.length; i++) {
    hash = str.charCodeAt(i) + ((hash << 5) - hash);
  }

  // Generate a hue between 0 and 360
  const hue = Math.abs(hash) % 360;

  // Use a moderate saturation and lightness for good contrast
  // Adjust these values based on light/dark mode if needed
  return `hsl(${hue}, 70%, 50%)`;
}

// Get display name for a message sender
function getSenderDisplayName(
  senderName: string | undefined,
  senderId: string,
  isFromMe: boolean,
  t: (key: string) => string
): string {
  if (isFromMe) return t("you") || "You";
  if (senderName && senderName.trim().length > 0) {
    return senderName;
  }

  // For WhatsApp IDs like "33631207926@s.whatsapp.net", extract and format the phone number
  const whatsappMatch = senderId.match(/^(\d+)@s\.whatsapp\.net$/);
  if (whatsappMatch) {
    const phoneNumber = whatsappMatch[1];
    // Format phone number with spaces for readability
    // Example: 33631207926 -> +33 6 31 20 79 26
    if (phoneNumber.startsWith("33") && phoneNumber.length >= 10) {
      // French phone number format: +33 followed by 9 digits (without leading 0)
      const countryCode = phoneNumber.substring(0, 2);
      const rest = phoneNumber.substring(2);
      // Format as +33 X XX XX XX XX
      const formatted = `+${countryCode} ${rest.substring(0, 1)} ${rest.substring(1, 3)} ${rest.substring(3, 5)} ${rest.substring(5, 7)} ${rest.substring(7)}`;
      return formatted;
    } else {
      // Other formats: add spaces every 2 digits
      const formatted = phoneNumber.replace(/(\d{2})(?=\d)/g, "$1 ");
      return `+${formatted}`;
    }
  }

  // Fallback for other ID formats
  return senderId
    .replace(/^user-/, "")
    .replace(/^whatsapp-/, "")
    .replace(/^slack-/, "")
    .split("-")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

// Wrapper function to use Wails with React Query's infinite query
const fetchMessages = async (conversationID: string, beforeTimestamp?: Date): Promise<models.Message[]> => {
  try {
    const result = beforeTimestamp
      ? await GetMessagesForConversationBefore(conversationID, beforeTimestamp)
      : await GetMessagesForConversation(conversationID);
    // Ensure we always return an array
    return Array.isArray(result) ? result : [];
  } catch (error) {
    console.error("Error fetching messages:", error);
    return [];
  }
};

const getMessageDomId = (message: models.Message): string => {
  if (message.protocolMsgId && message.protocolMsgId.trim().length > 0) {
    return message.protocolMsgId;
  }
  if (message.id) {
    return `message-${message.id}`;
  }
  return `ts-${timeToDate(message.timestamp).getTime()}`;
};

const isElementVisibleWithinContainer = (
  element: HTMLElement,
  container: HTMLElement
) => {
  const containerRect = container.getBoundingClientRect();
  const elementRect = element.getBoundingClientRect();
  const intersectionTop = Math.max(elementRect.top, containerRect.top);
  const intersectionBottom = Math.min(elementRect.bottom, containerRect.bottom);
  const intersectionHeight = intersectionBottom - intersectionTop;
  return intersectionHeight > elementRect.height * 0.6;
};

// Check if two dates are on different days
const isDifferentDay = (date1: Date, date2: Date | null): boolean => {
  if (!date2) return true;
  return (
    date1.getFullYear() !== date2.getFullYear() ||
    date1.getMonth() !== date2.getMonth() ||
    date1.getDate() !== date2.getDate()
  );
};

// Format date for date separator
const formatDateSeparator = (date: Date, t: (key: string) => string): string => {
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const messageDate = new Date(date.getFullYear(), date.getMonth(), date.getDate());
  const yesterday = new Date(today);
  yesterday.setDate(yesterday.getDate() - 1);

  // Check if it's today
  if (messageDate.getTime() === today.getTime()) {
    return t("today");
  }

  // Check if it's yesterday
  if (messageDate.getTime() === yesterday.getTime()) {
    return t("yesterday");
  }

  // Format as "dayName day month" or "dayName day month year" if different year
  const dayNames = [
    t("sunday"),
    t("monday"),
    t("tuesday"),
    t("wednesday"),
    t("thursday"),
    t("friday"),
    t("saturday"),
  ];
  const monthNames = [
    t("january"),
    t("february"),
    t("march"),
    t("april"),
    t("may"),
    t("june"),
    t("july"),
    t("august"),
    t("september"),
    t("october"),
    t("november"),
    t("december"),
  ];

  const dayName = dayNames[date.getDay()];
  const day = date.getDate();
  const month = monthNames[date.getMonth()];
  const year = date.getFullYear();

  if (year !== now.getFullYear()) {
    return `${dayName} ${day} ${month} ${year}`;
  }

  return `${dayName} ${day} ${month}`;
};

export function MessageList({
  selectedConversation,
}: {
  selectedConversation: models.MetaContact;
}) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const conversationId = selectedConversation.linkedAccounts[0]?.userId ?? "";

  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const messageElementsRef = useRef<Map<string, HTMLElement>>(new Map());
  const observerRef = useRef<IntersectionObserver | null>(null);
  const hasUserScrolledRef = useRef<boolean>(false);

  const isInitialLoadRef = useRef<boolean>(true);
  const lastScrollTopRef = useRef<number>(0);
  const isLoadingMoreRef = useRef<boolean>(false);
  const [hasWindowFocus, setHasWindowFocus] = useState<boolean>(() =>
    typeof document === "undefined" ? true : document.hasFocus()
  );
  const focusStateRef = useRef<boolean>(hasWindowFocus);
  const scrollStateRef = useRef({
    distanceFromBottom: 0,
    atBottom: true,
  });
  const hasScrolledToUnreadRef = useRef<string | null>(null);

  const {
    data,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
    isLoading,
  } = useInfiniteQuery<models.Message[], Error>({
    queryKey: ["messages", conversationId],
    queryFn: ({ pageParam }) => {
      const beforeTimestamp = pageParam ? new Date(pageParam as string) : undefined;
      return fetchMessages(conversationId, beforeTimestamp);
    },
    enabled: !!conversationId,
    initialData: { pages: [], pageParams: [] },
    placeholderData: (previousData) => {
      if (previousData && previousData.pages && Array.isArray(previousData.pages)) {
        return previousData;
      }
      return { pages: [], pageParams: [] };
    },
    structuralSharing: (oldData, newData) => {
      // Ensure we always return a valid structure
      if (!newData || typeof newData !== 'object' || !('pages' in newData) || !Array.isArray((newData as { pages: unknown }).pages)) {
        return oldData || { pages: [], pageParams: [] };
      }
      return newData;
    },
    getNextPageParam: (lastPage, allPages) => {
      if (!lastPage || !Array.isArray(lastPage) || lastPage.length === 0) {
        return undefined;
      }
      // Get the oldest message timestamp from all loaded pages
      if (!allPages || !Array.isArray(allPages)) {
        return undefined;
      }
      const allMessages = allPages.flat();
      if (allMessages.length === 0) {
        return undefined;
      }
      // Find the oldest message
      const oldestMessage = allMessages.reduce((oldest, msg) => {
        const msgTime = timeToDate(msg.timestamp);
        const oldestTime = timeToDate(oldest.timestamp);
        return msgTime < oldestTime ? msg : oldest;
      });
      return timeToDate(oldestMessage.timestamp).toISOString();
    },
    initialPageParam: undefined,
  });

  // Flatten all pages into a single array
  const messages = useMemo(() => {
    // Ensure data exists and has pages property
    if (!data) return [];
    if (!data.pages || !Array.isArray(data.pages)) return [];
    // Filter out any null/undefined pages and flat
    return data.pages.filter((page) => Array.isArray(page)).flat();
  }, [data]);

  // Determine if this is a group conversation
  // For WhatsApp, groups have "@g.us" in the conversation ID
  // For other providers, we can check if there are multiple unique senders
  const isGroupConversation = useMemo(() => {
    if (conversationId.includes("@g.us")) {
      return true; // WhatsApp group
    }
    // Check if there are multiple unique senders in the messages
    if (messages.length > 0) {
      const uniqueSenders = new Set(messages.map((m) => m.senderId));
      return uniqueSenders.size > 2; // More than 2 (me + at least 2 others)
    }
    return false;
  }, [conversationId, messages]);

  // With reverse scroll, we don't need complex scroll restoration logic
  // The browser naturally maintains scroll position when new items are added at the "bottom" (visually top)

  const syncConversation = useMessageReadStore(
    (state) => state.syncConversation
  );
  const cleanupObsoleteMessages = useMessageReadStore(
    (state) => state.cleanupObsoleteMessages
  );
  const markMessageAsRead = useMessageReadStore((state) => state.markAsRead);
  const readByConversation = useMessageReadStore(
    (state) => state.readByConversation
  );
  const conversationReadState = useMemo(
    () => readByConversation[conversationId] ?? {},
    [readByConversation, conversationId]
  );

  // Filter out thread messages and group threads by parent message
  const { mainMessages, threadsByParent } = useMemo(() => {
    const main: models.Message[] = [];
    const threads: Record<string, models.Message[]> = {};

    messages.forEach((msg) => {
      // Skip empty messages (no body and no attachments)
      const hasBody = msg.body && msg.body.trim() !== "";
      const hasAttachments = msg.attachments && msg.attachments.trim() !== "";
      const isEmpty = !hasBody && !hasAttachments;
      
      if (isEmpty) {
        // Skip empty messages completely
        return;
      }

      if (!msg.threadId) {
        // This is a main message
        main.push(msg);
      } else {
        // This is a thread reply
        if (!threads[msg.threadId]) {
          threads[msg.threadId] = [];
        }
        threads[msg.threadId].push(msg);
      }
    });

    // Sort main messages by timestamp
    main.sort(
      (a, b) =>
        timeToDate(a.timestamp).getTime() - timeToDate(b.timestamp).getTime()
    );

    return { mainMessages: main, threadsByParent: threads };
  }, [messages]);

  // Handle initial scroll position (bottom or first unread message)
  useLayoutEffect(() => {
    const container = scrollContainerRef.current;
    if (!container || messages.length === 0 || isLoading) {

      return;
    }

    // Only on initial load (use isInitialLoadRef flag)
    if (isInitialLoadRef.current && !isFetchingNextPage) {


      // Find first unread message
      const firstUnreadMessage = mainMessages.find(msg => {
        const domId = getMessageDomId(msg);
        return conversationReadState[domId] === false;
      });

      if (firstUnreadMessage) {
        const targetId = getMessageDomId(firstUnreadMessage);
        const target = messageElementsRef.current.get(targetId);
        if (target) {
          const marginTop = 100;

          // Calculate target scroll top relative to the container
          // We use offsetTop for more reliable positioning within the scroll container
          const targetScrollTop = target.offsetTop - marginTop;



          container.scrollTop = Math.max(0, targetScrollTop);
          hasScrolledToUnreadRef.current = targetId;

          // Mark that we've done the initial scroll
          isInitialLoadRef.current = false;
          return;
        }
      }

      // No unread messages, scroll to bottom
      container.scrollTop = container.scrollHeight;

      // Mark that we've done the initial scroll
      isInitialLoadRef.current = false;
    }
  }, [messages, isLoading, isFetchingNextPage, mainMessages, conversationReadState]);

  const showThreads = useAppStore((state) => state.showThreads);
  const setShowThreads = useAppStore((state) => state.setShowThreads);
  const setSelectedThreadId = useAppStore((state) => state.setSelectedThreadId);
  const messageLayout = useAppStore((state) => state.messageLayout);
  const [isFileUploadModalOpen, setIsFileUploadModalOpen] = useState(false);
  const [pendingFiles, setPendingFiles] = useState<File[]>([]);
  const [pendingFilePaths, setPendingFilePaths] = useState<string[]>([]);
  const [isDragging, setIsDragging] = useState(false);
  const [revealedDeletedMessages, setRevealedDeletedMessages] = useState<Set<string>>(
    () => new Set()
  );
  const [editingMessageId, setEditingMessageId] = useState<string | null>(null);
  const [editingText, setEditingText] = useState<string>("");
  const [originalEditText, setOriginalEditText] = useState<string>("");
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [messageToDelete, setMessageToDelete] = useState<{ conversationID: string; messageID: string } | null>(null);
  const [openActionsMessageId, setOpenActionsMessageId] = useState<string | null>(null);
  const { toasts, showToast, closeToast } = useToast();

  const handleToggleThreads = () => {
    if (showThreads) {
      // When closing the panel, also clear the selected thread
      setSelectedThreadId(null);
    }
    setShowThreads(!showThreads);
  };

  useEffect(() => {
    if (!conversationId || messages.length === 0) {
      return;
    }
    // Sync with ALL messages (not just mainMessages) to ensure we clean up messages
    // that were filtered out (empty messages) or are no longer in the conversation
    syncConversation(conversationId, messages);
    
    // Create a set of all valid message IDs (from all loaded messages)
    const allMessageIds = new Set(messages.map(msg => {
      const id = getMessageDomId(msg);
      return id;
    }));
    
    // Cleanup obsolete messages that are not in the loaded messages
    // This handles messages that were deleted, filtered out (empty messages), or no longer exist
    cleanupObsoleteMessages(conversationId, allMessageIds);
    
    // Log pour déboguer : vérifier s'il y a des messages non lus qui ne sont pas dans les messages chargés
    const unreadInStore = Object.entries(conversationReadState)
      .filter(([, isRead]) => !isRead)
      .map(([msgId]) => msgId);
    const unreadNotInMessages = unreadInStore.filter(msgId => !allMessageIds.has(msgId));
    
    if (unreadNotInMessages.length > 0) {
      console.log(`MessageList: Conversation ${conversationId} - Cleaning up ${unreadNotInMessages.length} unread messages that are not in loaded messages`);
    }
  }, [conversationId, messages, syncConversation, cleanupObsoleteMessages, conversationReadState]);

  const firstUnreadMessageId = useMemo(() => {
    for (const message of mainMessages) {
      const domId = getMessageDomId(message);
      if (conversationReadState[domId] === false) {
        return domId;
      }
    }
    return null;
  }, [conversationReadState, mainMessages]);

  useEffect(() => {
    hasScrolledToUnreadRef.current = null;
    hasUserScrolledRef.current = false;
    isInitialLoadRef.current = true;
    lastScrollTopRef.current = 0;
    isLoadingMoreRef.current = false;
  }, [conversationId]);

  useEffect(() => {
    setRevealedDeletedMessages(new Set());
  }, [conversationId]);

  useEffect(() => {
    const container = scrollContainerRef.current;
    if (!container) {
      return;
    }
    const handleScroll = () => {
      const distance =
        container.scrollHeight - container.scrollTop - container.clientHeight;
      scrollStateRef.current = {
        distanceFromBottom: distance,
        atBottom: distance < 80,
      };

      const currentScrollTop = container.scrollTop;
      const scrollDelta = currentScrollTop - lastScrollTopRef.current;

      // Mark that user has scrolled (not programmatic scroll)
      // Only if scroll position changed significantly (user action, not programmatic)
      if (Math.abs(scrollDelta) > 5) {
        hasUserScrolledRef.current = true;
        // If user scrolled up (negative delta), reset the loading flag
        if (scrollDelta < 0 && isLoadingMoreRef.current) {
          isLoadingMoreRef.current = false;
        }
      }

      lastScrollTopRef.current = currentScrollTop;

      // With column-reverse on WebKit/Safari, scrollTop can become negative when scrolling up
      // We need to trigger when we're near the "top" (old messages)
      // This can be when scrollTop is small (close to 0) or negative (overscroll)

      // Calculate distance from the visual "top" where old messages are
      // On WebKit with column-reverse, this is when scrollTop approaches 0 from below (negative)
      const isNearTop = container.scrollTop < 200;



      // Load more messages when scrolling near the top (within 200px, including negative for overscroll)
      // On WebKit with column-reverse, scrollTop becomes negative when scrolling up past the content
      if (
        isNearTop &&
        hasNextPage &&
        !isFetchingNextPage &&
        !isLoadingMoreRef.current &&
        hasUserScrolledRef.current &&
        !isInitialLoadRef.current &&
        scrollDelta < 0 // User scrolled up (toward old messages)
      ) {

        isLoadingMoreRef.current = true;
        fetchNextPage();
      }
    };
    container.addEventListener("scroll", handleScroll, { passive: true });
    handleScroll();
    return () => container.removeEventListener("scroll", handleScroll);
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);

  useEffect(() => {
    const container = scrollContainerRef.current;
    if (!container || messages.length === 0) {
      return;
    }

    // Use a small delay to ensure DOM is ready
    const timeoutId = setTimeout(() => {
      // Mark initial load as complete
      if (isInitialLoadRef.current) {
        isInitialLoadRef.current = false;
      }

      // If loading more messages (infinite scroll), don't interfere
      if (isFetchingNextPage) {

        return;
      }

      // Handle auto-scroll to bottom for new messages
      // If we were at the bottom before the update, stay at the bottom
      const { atBottom } = scrollStateRef.current;


      if (atBottom) {

        container.scrollTop = container.scrollHeight;
      }
    }, 100);

    return () => clearTimeout(timeoutId);
  }, [messages, isFetchingNextPage]);


  useEffect(() => {
    focusStateRef.current = hasWindowFocus;
  }, [hasWindowFocus]);

  const registerMessageNode = useCallback(
    (messageId: string) => (node: HTMLDivElement | null) => {
      const elementsMap = messageElementsRef.current;
      const existingNode = elementsMap.get(messageId);
      if (existingNode && observerRef.current) {
        observerRef.current.unobserve(existingNode);
      }
      if (!node) {
        elementsMap.delete(messageId);
        return;
      }
      elementsMap.set(messageId, node);
      node.dataset.messageId = messageId;
      if (observerRef.current) {
        observerRef.current.observe(node);
      }
    },
    []
  );

  const handleVisibilityChange = useCallback(
    (entries: IntersectionObserverEntry[]) => {
      if (!focusStateRef.current || !conversationId) {
        return;
      }
      entries.forEach((entry) => {
        if (!entry.isIntersecting) {
          return;
        }
        const element = entry.target as HTMLElement;
        const messageId = element.dataset.messageId;
        if (messageId) {
          markMessageAsRead(conversationId, messageId);
        }
      });
    },
    [conversationId, markMessageAsRead]
  );

  useEffect(() => {
    const container = scrollContainerRef.current;
    if (!container) {
      return;
    }
    const observer = new IntersectionObserver(handleVisibilityChange, {
      root: container,
      threshold: 0.65,
    });
    observerRef.current = observer;
    messageElementsRef.current.forEach((element) => observer.observe(element));
    return () => observer.disconnect();
  }, [handleVisibilityChange]);

  useEffect(() => {
    const handleFocus = () => setHasWindowFocus(true);
    const handleBlur = () => setHasWindowFocus(false);
    window.addEventListener("focus", handleFocus);
    window.addEventListener("blur", handleBlur);
    return () => {
      window.removeEventListener("focus", handleFocus);
      window.removeEventListener("blur", handleBlur);
    };
  }, []);

  const evaluateVisibleMessages = useCallback(() => {
    if (!conversationId || !focusStateRef.current) {
      return;
    }
    const container = scrollContainerRef.current;
    if (!container) {
      return;
    }
    messageElementsRef.current.forEach((element, messageId) => {
      if (isElementVisibleWithinContainer(element, container)) {
        markMessageAsRead(conversationId, messageId);
      }
    });
  }, [conversationId, markMessageAsRead]);

  useEffect(() => {
    if (hasWindowFocus) {
      evaluateVisibleMessages();
    }
  }, [evaluateVisibleMessages, hasWindowFocus]);

  const getLastThreadMessage = (parentMsgId: string): models.Message | null => {
    const threadMessages = threadsByParent[parentMsgId];
    if (!threadMessages || threadMessages.length === 0) return null;
    // Sort by timestamp and get the last one
    return threadMessages.sort(
      (a, b) =>
        timeToDate(b.timestamp).getTime() - timeToDate(a.timestamp).getTime()
    )[0];
  };

  const getThreadCount = (parentMsgId: string): number => {
    return threadsByParent[parentMsgId]?.length || 0;
  };

  const handleThreadClick = (parentMsgId: string) => {
    setSelectedThreadId(parentMsgId);
  };

  const showConversationDetails = useAppStore(
    (state) => state.showConversationDetails
  );
  const setShowConversationDetails = useAppStore(
    (state) => state.setShowConversationDetails
  );
  const setSelectedAvatarUrl = useAppStore(
    (state) => state.setSelectedAvatarUrl
  );

  const handleToggleDetails = () => {
    setShowConversationDetails(!showConversationDetails);
  };

  const handleAvatarClick = (avatarUrl: string | undefined, displayName?: string) => {
    // Use avatar URL if available, otherwise use a placeholder based on display name
    const urlToShow = avatarUrl || (displayName ? `https://api.dicebear.com/7.x/initials/svg?seed=${encodeURIComponent(displayName)}` : null);
    if (urlToShow) {
      setSelectedAvatarUrl(urlToShow);
    }
  };

  const toggleDeletedMessage = useCallback((messageId: string) => {
    setRevealedDeletedMessages((prev) => {
      const next = new Set(prev);
      if (next.has(messageId)) {
        next.delete(messageId);
      } else {
        next.add(messageId);
      }
      return next;
    });
  }, []);

  const handleEditMessage = useCallback((message: models.Message) => {
    const messageId = getMessageDomId(message);
    setEditingMessageId(messageId);
    const body = message.body || "";
    setEditingText(body);
    setOriginalEditText(body);
  }, []);

  const handleSaveEdit = useCallback(async (skipValidation = false) => {
    if (!editingMessageId || typeof EditMessage !== "function") {
      return;
    }

    if (!skipValidation && !editingText.trim()) {
      return;
    }

    // Find the message to get its protocol message ID
    const message = messages.find((msg) => getMessageDomId(msg) === editingMessageId);
    if (!message || !message.protocolMsgId) {
      return;
    }

    const newText = editingText.trim();
    const originalText = originalEditText;
    const messageId = message.protocolMsgId;

    // Exit edit mode immediately
    setEditingMessageId(null);
    setEditingText("");
    setOriginalEditText("");

    // Invalidate and refetch messages immediately (optimistic update)
    queryClient.invalidateQueries({
      queryKey: ["messages", conversationId],
    });
    queryClient.refetchQueries({
      queryKey: ["messages", conversationId],
    });

    // Try to edit the message asynchronously
    try {
      await EditMessage(conversationId, messageId, newText);
    } catch (error) {
      console.error("Failed to edit message:", error);
      // Restore original text in the message
      queryClient.setQueryData(["messages", conversationId], (oldData: models.Message[] | undefined) => {
        if (!oldData) return oldData;
        return oldData.map((msg) => {
          if (msg.protocolMsgId === messageId) {
            return { ...msg, body: originalText };
          }
          return msg;
        });
      });
      // Show error toast
      showToast(t("edit_failed"), "error");
    }
  }, [editingMessageId, editingText, originalEditText, conversationId, messages, queryClient, t, showToast]);

  const handleCancelEdit = useCallback(() => {
    setEditingMessageId(null);
    setEditingText("");
  }, []);

  const handleDeleteClick = useCallback((message: models.Message) => {
    const protocolMsgId = message.protocolMsgId || getMessageDomId(message);
    setMessageToDelete({
      conversationID: conversationId,
      messageID: protocolMsgId,
    });
    setDeleteConfirmOpen(true);
  }, [conversationId]);

  const handleConfirmDelete = useCallback(async () => {
    if (!messageToDelete) {
      console.error("No message to delete");
      return;
    }

    if (typeof DeleteMessage !== "function") {
      console.error("DeleteMessage is not available");
      return;
    }

    console.log("Deleting message:", messageToDelete);
    try {
      await DeleteMessage(messageToDelete.conversationID, messageToDelete.messageID);
      console.log("Message deleted successfully");
      setDeleteConfirmOpen(false);
      setMessageToDelete(null);
      // Invalidate and refetch messages
      queryClient.invalidateQueries({
        queryKey: ["messages", messageToDelete.conversationID],
      });
      queryClient.refetchQueries({
        queryKey: ["messages", messageToDelete.conversationID],
      });
    } catch (error) {
      console.error("Failed to delete message:", error);
    }
  }, [messageToDelete, queryClient]);

  // Handle drag and drop for file upload
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
    if (files.length > 0) {
      setPendingFiles(files);
      setIsFileUploadModalOpen(true);
    }
  }, []);

  const handleFileUpload = async (files: File[], filePaths?: string[]) => {
    const hasFilePaths = Boolean(filePaths && filePaths.length > 0);

    if (!selectedConversation || (files.length === 0 && !hasFilePaths)) {
      return;
    }

    const conversationId = selectedConversation.linkedAccounts[0]?.userId;
    if (!conversationId) {
      return;
    }

    // First, handle file paths (from clipboard/drag&drop in Wails or clipboard managers)
    if (hasFilePaths && filePaths) {
      for (const filePath of filePaths) {
        try {
          if (typeof SendFileFromPath === "function") {
            await SendFileFromPath(conversationId, filePath);
          } else {
            console.error("SendFileFromPath API is not available. Please rebuild the application.");
          }
        } catch (error) {
          console.error("Failed to send file from path:", filePath, error);
        }
      }
    }

    const shouldProcessFileObjects = files.length > 0 && !hasFilePaths;

    if (!shouldProcessFileObjects) {
      // Only file paths were processed (or no files to process), just refresh messages
      queryClient.invalidateQueries({
        queryKey: ["messages", conversationId],
      });
      queryClient.refetchQueries({
        queryKey: ["messages", conversationId],
      });
      return;
    }

    // Check if SendFile is available
    if (typeof SendFile !== "function") {
      console.error("SendFile API is not available. Please rebuild the application to generate Wails bindings.");
      return;
    }

    // Send each file
    for (const initialFile of files) {
      let file = initialFile;
      try {

        // Check file size (limit to 64MB to avoid memory issues)
        const maxSize = 64 * 1024 * 1024; // 64MB
        if (file.size > maxSize) {
          console.error(`File ${file.name} is too large (${(file.size / 1024 / 1024).toFixed(2)}MB). Maximum size is 64MB.`);
          continue;
        }

        // In Wails, files from clipboard/drag&drop may have a path property
        // Check all possible ways to get the file path
        interface FileWithPath {
          path?: string;
          webkitRelativePath?: string;
          [key: string]: unknown;
        }
        const fileWithPath = file as File & FileWithPath;

        // Try multiple ways to get the path
        let filePath: string | undefined = undefined;

        // Method 1: Direct path property (Wails-specific)
        if (fileWithPath.path && typeof fileWithPath.path === "string") {
          filePath = fileWithPath.path;
        }
        // Method 2: webkitRelativePath (may contain full path in some cases)
        else if (fileWithPath.webkitRelativePath && typeof fileWithPath.webkitRelativePath === "string") {
          // webkitRelativePath is usually relative, but check if it looks like an absolute path
          if (fileWithPath.webkitRelativePath.startsWith("/") || fileWithPath.webkitRelativePath.match(/^[A-Za-z]:/)) {
            filePath = fileWithPath.webkitRelativePath;
          }
        }
        // Method 3: Check all properties for any string that looks like a file path
        else {
          for (const key in fileWithPath) {
            if (Object.prototype.hasOwnProperty.call(fileWithPath, key)) {
              const value = fileWithPath[key];
              if (typeof value === "string" && (value.startsWith("/") || value.match(/^[A-Za-z]:[\\/]/))) {
                // This looks like a file path
                filePath = value;
                break;
              }
            }
          }
        }

        if (filePath && typeof filePath === "string") {
          // File has a path - use SendFileFromPath directly
          if (typeof SendFileFromPath === "function") {
            await SendFileFromPath(conversationId, filePath);
            continue;
          } else {
            console.warn("SendFileFromPath not available yet, falling back to reading file");
          }
        }

        // File doesn't have a path - try FileReader first, then fallback to Go clipboard API
        // ATTEMPT 1: Standard JS FileReader (works for screenshots/images copied in browser)
        // Attempt to compress image files before reading
        file = await compressImageFile(file);

        let fileData: string;
        let fileMimeType = file.type || "application/octet-stream";
        let fileName = file.name;

        try {
          fileData = await new Promise<string>((resolve, reject) => {
            const reader = new FileReader();

            // Set timeout to avoid hanging
            const timeout = setTimeout(() => {
              reader.abort();
              reject(new Error(`Timeout reading file: ${file.name}`));
            }, 30000); // 30 seconds timeout

            reader.onload = (e) => {
              clearTimeout(timeout);
              try {
                if (e.target?.result && typeof e.target.result === "string") {
                  // Remove data URL prefix (e.g., "data:image/jpeg;base64,")
                  const parts = e.target.result.split(",");
                  if (parts.length > 1) {
                    resolve(parts[1]); // Return only the base64 data part
                  } else {
                    reject(new Error("Invalid data URL format"));
                  }
                } else {
                  reject(new Error("FileReader result is empty or invalid"));
                }
              } catch (err) {
                clearTimeout(timeout);
                reject(err);
              }
            };

            reader.onerror = (error) => {
              clearTimeout(timeout);
              console.error("FileReader error for file:", file.name, error);
              reject(new Error(`Failed to read file: ${file.name} (size: ${(file.size / 1024 / 1024).toFixed(2)}MB)`));
            };

            reader.onabort = () => {
              clearTimeout(timeout);
              reject(new Error(`File reading aborted: ${file.name}`));
            };

            // Use readAsDataURL to convert file to base64
            try {
              reader.readAsDataURL(file);
            } catch (err) {
              clearTimeout(timeout);
              reject(new Error(`Failed to start reading file: ${file.name} - ${err}`));
            }
          });
        } catch (readerError) {
          // ATTEMPT 2: Fallback to Go clipboard API (for files from Finder/Explorer)
          console.warn("JS FileReader failed (likely WebKit security restriction), trying Go clipboard fallback...", readerError);

          // Try to get GetClipboardFile dynamically from window
          let getClipboardFileFn: (() => Promise<{ filename: string; base64: string; mimeType: string }>) | undefined;

          if (typeof window !== "undefined") {
            try {
              // GetClipboardFile will be available after Wails bindings are regenerated
              // eslint-disable-next-line @typescript-eslint/no-explicit-any
              getClipboardFileFn = (window as any).go?.main?.App?.GetClipboardFile;
            } catch {
              // Ignore
            }
          }

          if (getClipboardFileFn && typeof getClipboardFileFn === "function") {
            try {
              const clipboardFile = await getClipboardFileFn();
              if (clipboardFile && clipboardFile.base64) {
                fileData = clipboardFile.base64;
                fileMimeType = clipboardFile.mimeType || file.type || "application/octet-stream";
                fileName = clipboardFile.filename || file.name;
              } else {
                throw new Error("Go clipboard API returned empty result");
              }
            } catch (goError) {
              console.error("Go clipboard API also failed:", goError);
              throw new Error(`Cannot read file "${file.name}". Both JS FileReader and Go clipboard API failed. Please try using the file picker button (📎) instead.`);
            }
          } else {
            console.error("GetClipboardFile API is not available. Please rebuild the application with 'wails dev' or 'wails build'.");
            throw new Error(`Cannot read file "${file.name}". FileReader failed and GetClipboardFile is not available. Please rebuild the application to generate Wails bindings, or use the file picker button (📎) instead.`);
          }
        }

        // Send file via API using base64 data
        if (typeof SendFile === "function") {
          await SendFile(conversationId, fileData, fileName, fileMimeType);
        } else {
          throw new Error("SendFile API is not available");
        }
      } catch (error) {
        console.error("Failed to send file:", file.name, error);
        // Continue with other files even if one fails
      }
    }

    // Invalidate and refetch messages after sending
    queryClient.invalidateQueries({
      queryKey: ["messages", conversationId],
    });
    queryClient.refetchQueries({
      queryKey: ["messages", conversationId],
    });
  };

  return (
    <>
      <ToastContainer toasts={toasts} onClose={closeToast} />
      <div
        className={cn(
          "flex flex-col h-full overflow-hidden transition-colors",
          isDragging && "bg-muted/50"
        )}
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
        <div className="flex-1 overflow-y-auto p-4 min-h-0 scroll-area flex flex-col-reverse" ref={scrollContainerRef}>
          {isLoading ? (
            <div className="flex flex-col gap-2">
              {[...Array(3)].map((_, i) => (
                <div key={i} className="flex items-start gap-3 animate-pulse">
                  <div className="h-10 w-10 rounded-full bg-muted"></div>
                  <div className="flex-1 space-y-2">
                    <div className="h-4 w-24 bg-muted rounded"></div>
                    <div className="h-16 w-3/4 bg-muted rounded"></div>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <>
              {messageLayout === "bubble" ? (
                <div className="space-y-4">
                  {/* Reverse array for display with column-reverse */}
                  {[...mainMessages].reverse().map((message, index) => {
                    const messageId = getMessageDomId(message);
                    const lastThreadMsg = getLastThreadMessage(message.protocolMsgId);
                    const threadCount = getThreadCount(message.protocolMsgId);
                    const hasThread = threadCount > 0;
                    const displayName = getSenderDisplayName(
                      message.senderName,
                      message.senderId,
                      message.isFromMe,
                      t
                    );
                    const isUnread = conversationReadState[messageId] === false;
                    const showUnreadDivider =
                      messageId === firstUnreadMessageId && isUnread;
                    const timestamp = timeToDate(message.timestamp);
                    const timeString = `${timestamp
                      .getHours()
                      .toString()
                      .padStart(2, "0")}:${timestamp
                        .getMinutes()
                        .toString()
                        .padStart(2, "0")}`;
                    const isDeleted = Boolean(message.isDeleted);
                    const isDeletedRevealed =
                      isDeleted && revealedDeletedMessages.has(messageId);
                    const showDeletedPlaceholder =
                      isDeleted && !isDeletedRevealed;
                    const baseBubbleColorClass = message.isFromMe
                      ? "bg-blue-600 text-white"
                      : "bg-muted text-foreground";
                    const deletedPlaceholderClass = message.isFromMe
                      ? "bg-blue-950/80 text-blue-100"
                      : "bg-muted/70 text-muted-foreground";
                    const deletedRevealedClass = message.isFromMe
                      ? "bg-blue-600/80 text-white"
                      : "bg-muted text-foreground";
                    const bubbleClass = cn(
                      "rounded-lg p-3 transition-colors border border-transparent",
                      isDeleted
                        ? isDeletedRevealed
                          ? deletedRevealedClass
                          : deletedPlaceholderClass
                        : baseBubbleColorClass,
                      isUnread &&
                      "ring-2 ring-primary/70 bg-primary/10 shadow-lg",
                      isDeleted &&
                      "border-dashed border-destructive/60 cursor-pointer group"
                    );
                    const deletedInteractionHandlers = isDeleted
                      ? {
                        role: "button" as const,
                        tabIndex: 0,
                        onClick: () => toggleDeletedMessage(messageId),
                        onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => {
                          if (event.key === "Enter" || event.key === " ") {
                            event.preventDefault();
                            toggleDeletedMessage(messageId);
                          }
                        },
                      }
                      : {};

                    const messageDate = timeToDate(message.timestamp);
                    const prevMessage = index > 0 ? mainMessages[index - 1] : null;
                    const prevMessageDate = prevMessage ? timeToDate(prevMessage.timestamp) : null;
                    const showDateSeparator = isDifferentDay(messageDate, prevMessageDate);

                    return (
                      <div key={messageId} className="space-y-2">
                        {showDateSeparator && (
                          <div
                            className="flex items-center gap-2 text-xs font-medium text-muted-foreground my-4"
                            role="separator"
                            aria-label={formatDateSeparator(messageDate, t)}
                          >
                            <span className="h-px flex-1 bg-border" />
                            <span className="px-2">{formatDateSeparator(messageDate, t)}</span>
                            <span className="h-px flex-1 bg-border" />
                          </div>
                        )}
                        {showUnreadDivider && (
                          <div
                            className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-primary"
                            role="separator"
                            aria-label={t("new_messages_separator")}
                          >
                            <span className="h-px flex-1 bg-border" />
                            {t("new_messages_separator")}
                            <span className="h-px flex-1 bg-border" />
                          </div>
                        )}
                        <div
                          ref={registerMessageNode(messageId)}
                          data-message-id={messageId}
                          className={cn(
                            "space-y-2 scroll-mt-28 group"
                          )}
                          onMouseEnter={() => setOpenActionsMessageId(messageId)}
                          onMouseLeave={() => setOpenActionsMessageId(null)}
                        >
                          <div
                            className={cn(
                              "flex items-start gap-3",
                              message.isFromMe && "justify-end"
                            )}
                          >
                            {!message.isFromMe && (
                              <div className="flex flex-col items-center shrink-0">
                                <button
                                  onClick={() =>
                                    handleAvatarClick(
                                      message.senderAvatarUrl,
                                      displayName
                                    )
                                  }
                                  className="shrink-0"
                                >
                                  <Avatar className="cursor-pointer hover:opacity-80 transition-opacity">
                                    <AvatarImage src={message.senderAvatarUrl} />
                                    <AvatarFallback>
                                      {displayName.substring(0, 2).toUpperCase()}
                                    </AvatarFallback>
                                  </Avatar>
                                </button>
                                <span className="text-xs text-muted-foreground mt-1">
                                  {timeString}
                                </span>
                              </div>
                            )}
                            <div className="flex flex-col items-start gap-1 relative group/bubble">
                              <div className="flex items-start gap-2 relative w-full">
                                <div
                                  className={bubbleClass}
                                  aria-live="polite"
                                  aria-label={
                                    isUnread ? t("unread_message_label") : undefined
                                  }
                                  {...deletedInteractionHandlers}
                                >
                                  {editingMessageId === messageId ? (
                                    <div className="flex flex-col gap-2">
                                      <Input
                                        value={editingText}
                                        onChange={(e) => setEditingText(e.target.value)}
                                        onKeyDown={(e) => {
                                          if (e.key === "Enter" && !e.shiftKey) {
                                            e.preventDefault();
                                            handleSaveEdit();
                                          } else if (e.key === "Escape") {
                                            handleCancelEdit();
                                          }
                                        }}
                                        onBlur={(e) => {
                                          // Only save if the blur is not caused by clicking on a button
                                          const relatedTarget = e.relatedTarget as HTMLElement | null;
                                          if (!relatedTarget || (!relatedTarget.closest('button') && !relatedTarget.closest('[role="button"]'))) {
                                            handleSaveEdit(false);
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
                                      {message.body && message.body.trim() !== "" && (
                                        <p>{message.body}</p>
                                      )}
                                    </>
                                  )}
                                  {message.attachments &&
                                    message.attachments.trim() !== "" && (
                                      <MessageAttachments
                                        attachments={message.attachments}
                                        isFromMe={message.isFromMe}
                                        layout="bubble"
                                      />
                                    )}
                                  {(!message.body || message.body.trim() === "") &&
                                    (!message.attachments ||
                                      message.attachments.trim() === "") && (
                                      <p className="text-sm opacity-70 italic">
                                        {t("empty_message")}
                                      </p>
                                    )}
                                  <div className="flex flex-col mt-1">
                                    {showDeletedPlaceholder && (
                                      <div className="text-xs italic text-muted-foreground/80 flex items-center gap-2 leading-none">
                                        <span>{t("message_deleted")}</span>
                                        <span className="text-[10px] uppercase tracking-wide hidden group-hover:inline">
                                          {t("click_to_view_deleted")}
                                        </span>
                                      </div>
                                    )}
                                    {isDeleted && isDeletedRevealed && (
                                      <span className="text-[11px] font-semibold uppercase tracking-wide text-destructive/80">
                                        {t("deleted_message_badge")}
                                      </span>
                                    )}
                                    {isUnread && (
                                      <p
                                        className={cn(
                                          "text-xs flex items-center gap-2 justify-end",
                                          message.isFromMe
                                            ? "text-blue-100"
                                            : "text-muted-foreground"
                                        )}
                                      >
                                        <span className="text-[10px] font-semibold uppercase tracking-wide text-primary">
                                          {t("unread_indicator")}
                                        </span>
                                      </p>
                                    )}
                                  </div>
                                </div>
                                {!isDeleted && editingMessageId !== messageId && message.isFromMe && (
                                  <div className="absolute -top-7 right-0 opacity-0 group-hover/bubble:opacity-100 transition-opacity z-50">
                                    <MessageActions
                                      isFromMe={message.isFromMe}
                                      hasAttachments={Boolean(message.attachments && message.attachments.trim() !== "")}
                                      onEdit={() => handleEditMessage(message)}
                                      onDelete={() => handleDeleteClick(message)}
                                      messageId={messageId}
                                      openActionsMessageId={openActionsMessageId}
                                    />
                                  </div>
                                )}
                              </div>
                              {message.isEdited && (
                                <span className={cn(
                                  "text-xs text-muted-foreground italic",
                                  message.isFromMe ? "self-end" : "self-start"
                                )}>
                                  {t("edited")}
                                </span>
                              )}
                              <MessageStatus
                                message={message}
                                isGroup={isGroupConversation}
                                allMessages={mainMessages}
                                layout="bubble"
                              />
                            </div>
                            {message.isFromMe && (
                              <div className="flex flex-col items-center shrink-0">
                                <button
                                  onClick={() => handleAvatarClick("", t("you"))}
                                  className="shrink-0"
                                >
                                  <Avatar className="cursor-pointer hover:opacity-80 transition-opacity">
                                    <AvatarImage src="" />
                                    <AvatarFallback>{t("me")}</AvatarFallback>
                                  </Avatar>
                                </button>
                                <span className="text-xs text-muted-foreground mt-1">
                                  {timeString}
                                </span>
                              </div>
                            )}
                          </div>
                          {hasThread && lastThreadMsg && (
                            <button
                              onClick={() =>
                                handleThreadClick(message.protocolMsgId)
                              }
                              className={cn(
                                "ml-15 flex items-center gap-2 p-2 rounded-lg bg-muted/50 hover:bg-muted transition-colors cursor-pointer text-left",
                                message.isFromMe
                                  ? "ml-auto max-w-[80%]"
                                  : "mr-auto max-w-[80%]"
                              )}
                            >
                              <button
                                onClick={() =>
                                  handleAvatarClick(
                                    lastThreadMsg.senderAvatarUrl,
                                    getSenderDisplayName(
                                      lastThreadMsg.senderName,
                                      lastThreadMsg.senderId,
                                      lastThreadMsg.isFromMe,
                                      t
                                    )
                                  )
                                }
                                className="shrink-0"
                              >
                                <Avatar className="h-5 w-5 shrink-0 cursor-pointer hover:opacity-80 transition-opacity">
                                  <AvatarImage src={lastThreadMsg.senderAvatarUrl} />
                                  <AvatarFallback className="text-xs">
                                    {getSenderDisplayName(
                                      lastThreadMsg.senderName,
                                      lastThreadMsg.senderId,
                                      lastThreadMsg.isFromMe,
                                      t
                                    )
                                      .substring(0, 2)
                                      .toUpperCase()}
                                  </AvatarFallback>
                                </Avatar>
                              </button>
                              <div className="flex-1 min-w-0">
                                <p className="text-sm text-muted-foreground truncate">
                                  {lastThreadMsg.body.length > 50
                                    ? `${lastThreadMsg.body.substring(0, 50)}...`
                                    : lastThreadMsg.body}
                                </p>
                                <div className="flex items-center gap-2 mt-1">
                                  <p className="text-xs text-muted-foreground/70">
                                    {timeToDate(
                                      lastThreadMsg.timestamp
                                    ).toLocaleTimeString()}
                                  </p>
                                  {threadCount > 0 && (
                                    <span className="text-xs text-muted-foreground/70">
                                      ·{" "}
                                      {threadCount === 1
                                        ? t("single_reply")
                                        : t("multiple_replies", {
                                          count: threadCount,
                                        })}
                                    </span>
                                  )}
                                </div>
                              </div>
                            </button>
                          )}
                        </div>
                      </div>
                    );
                  })}
                </div>
              ) : (
                <div className="space-y-1 text-sm">
                  {mainMessages.map((message, index) => {
                    const messageId = getMessageDomId(message);
                    const lastThreadMsg = getLastThreadMessage(message.protocolMsgId);
                    const threadCount = getThreadCount(message.protocolMsgId);
                    const hasThread = threadCount > 0;
                    const prevMessage = index > 0 ? mainMessages[index - 1] : null;
                    const nextMessage = index < mainMessages.length - 1 ? mainMessages[index + 1] : null;
                    const timestamp = timeToDate(message.timestamp);
                    const prevTimestamp = prevMessage
                      ? timeToDate(prevMessage.timestamp)
                      : null;
                    const timeDiffMinutes = prevTimestamp
                      ? (timestamp.getTime() - prevTimestamp.getTime()) / (1000 * 60)
                      : Infinity;
                    const isDeleted = Boolean(message.isDeleted);
                    // For deleted messages, also check if next message is from same sender
                    // If so, we should show sender info to maintain context
                    const shouldShowSenderForDeleted = isDeleted && nextMessage &&
                      nextMessage.senderId === message.senderId &&
                      nextMessage.isFromMe === message.isFromMe;
                    const showSender =
                      !prevMessage ||
                      prevMessage.senderId !== message.senderId ||
                      prevMessage.isFromMe !== message.isFromMe ||
                      timeDiffMinutes >= 5 ||
                      shouldShowSenderForDeleted;
                    const displayName = getSenderDisplayName(
                      message.senderName,
                      message.senderId,
                      message.isFromMe,
                      t
                    );
                    const senderColor = getColorFromString(message.senderId);
                    const timeString = `${timestamp
                      .getHours()
                      .toString()
                      .padStart(2, "0")}:${timestamp
                        .getMinutes()
                        .toString()
                        .padStart(2, "0")}`;
                    const isUnread = conversationReadState[messageId] === false;
                    const showUnreadDivider =
                      messageId === firstUnreadMessageId && isUnread;

                    const messageDate = timeToDate(message.timestamp);
                    const prevMessageDate = prevMessage ? timeToDate(prevMessage.timestamp) : null;
                    const showDateSeparator = isDifferentDay(messageDate, prevMessageDate);
                    const isDeletedRevealed =
                      isDeleted && revealedDeletedMessages.has(messageId);
                    const showDeletedPlaceholder =
                      isDeleted && !isDeletedRevealed;
                    const deletedListWrapperClass = cn(
                      "w-full flex flex-col gap-1",
                      isDeleted && "group",
                      showDeletedPlaceholder && "cursor-pointer text-muted-foreground/80"
                    );
                    const deletedListHandlers = isDeleted
                      ? {
                        role: "button" as const,
                        tabIndex: 0,
                        onClick: () => toggleDeletedMessage(messageId),
                        onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => {
                          if (event.key === "Enter" || event.key === " ") {
                            event.preventDefault();
                            toggleDeletedMessage(messageId);
                          }
                        },
                      }
                      : {};

                    return (
                      <div key={messageId} className="space-y-1">
                        {showDateSeparator && (
                          <div
                            className="flex items-center gap-2 text-xs font-medium text-muted-foreground my-4"
                            role="separator"
                            aria-label={formatDateSeparator(messageDate, t)}
                          >
                            <span className="h-px flex-1 bg-border" />
                            <span className="px-2">{formatDateSeparator(messageDate, t)}</span>
                            <span className="h-px flex-1 bg-border" />
                          </div>
                        )}
                        {showUnreadDivider && (
                          <div
                            className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-primary"
                            role="separator"
                            aria-label={t("new_messages_separator")}
                          >
                            <span className="h-px flex-1 bg-border" />
                            {t("new_messages_separator")}
                            <span className="h-px flex-1 bg-border" />
                          </div>
                        )}
                        <div
                          className={cn(
                            "flex items-start py-1 scroll-mt-28 group relative",
                            isUnread && "border border-primary/30 bg-primary/5 px-2"
                          )}
                          ref={registerMessageNode(messageId)}
                          data-message-id={messageId}
                          onMouseEnter={() => setOpenActionsMessageId(messageId)}
                          onMouseLeave={() => setOpenActionsMessageId(null)}
                        >
                          {/* Left column */}
                          <div className="flex flex-col items-center min-w-[60px]">
                            {showSender ? (
                              <>
                                <button
                                  onClick={() =>
                                    handleAvatarClick(
                                      message.senderAvatarUrl,
                                      displayName
                                    )
                                  }
                                  className="shrink-0"
                                >
                                  <Avatar className="h-6 w-6 mt-2.5 cursor-pointer hover:opacity-80 transition-opacity">
                                    <AvatarImage src={message.senderAvatarUrl} />
                                    <AvatarFallback className="text-xs">
                                      {message.isFromMe
                                        ? t("me")
                                        : displayName.substring(0, 2).toUpperCase()}
                                    </AvatarFallback>
                                  </Avatar>
                                </button>
                                <span className="text-xs text-muted-foreground mt-1">
                                  {timeString}
                                </span>
                              </>
                            ) : (
                              <span
                                className="text-xs text-muted-foreground leading-none"
                                style={{ marginTop: "10px" }}
                              >
                                {timeString}
                              </span>
                            )}
                          </div>
                          {/* Right column with 20px margin */}
                          <div className="flex flex-col items-start ml-5 flex-1 min-w-0 relative">
                            {showSender && (
                              <span
                                className="font-semibold text-sm text-left h-6 flex items-center mt-2.5"
                                style={{ color: senderColor }}
                              >
                                {displayName}
                              </span>
                            )}
                            <div className="w-full flex items-start gap-2">
                              <div className="flex-1 rounded-md transition-colors hover:bg-muted/50 -ml-2 pl-2 -mr-2 pr-2 relative">
                                {!isDeleted && editingMessageId !== messageId && (
                                  <div className="absolute -top-7 right-0 opacity-0 group-hover:opacity-100 transition-opacity z-50">
                                    <MessageActions
                                      isFromMe={message.isFromMe}
                                      hasAttachments={Boolean(message.attachments && message.attachments.trim() !== "")}
                                      onEdit={() => handleEditMessage(message)}
                                      onDelete={() => handleDeleteClick(message)}
                                      messageId={messageId}
                                      openActionsMessageId={openActionsMessageId}
                                    />
                                  </div>
                                )}
                                {showDeletedPlaceholder ? (
                                  <div className="flex flex-col gap-1">
                                    <div
                                      className="text-xs italic text-muted-foreground/80 flex items-center gap-2 leading-none text-left cursor-pointer"
                                      style={{ marginTop: "10px" }}
                                      {...deletedListHandlers}
                                    >
                                      <span>{t("message_deleted")}</span>
                                      <span className="text-[10px] uppercase tracking-wide hidden group-hover:inline">
                                        {t("click_to_view_deleted")}
                                      </span>
                                    </div>
                                  </div>
                                ) : (
                                  <>
                                    <div
                                      className={deletedListWrapperClass}
                                      {...deletedListHandlers}
                                    >
                                      {isDeleted && (
                                        <span className="text-[11px] font-semibold uppercase tracking-wide text-destructive/80">
                                          {t("deleted_message_badge")}
                                        </span>
                                      )}
                                      {editingMessageId === messageId ? (
                                        <div className="flex flex-col gap-2 w-full">
                                          <Input
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
                                            onBlur={(e) => {
                                              // Only save if the blur is not caused by clicking on a button
                                              const relatedTarget = e.relatedTarget as HTMLElement | null;
                                              if (!relatedTarget || (!relatedTarget.closest('button') && !relatedTarget.closest('[role="button"]'))) {
                                                handleSaveEdit(false);
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
                                          {!showSender && message.body && (
                                            <p
                                              className="text-foreground text-left m-0 leading-none"
                                              style={{ marginTop: "10px" }}
                                            >
                                              {message.body}
                                              {message.isEdited && (
                                                <span className="text-muted-foreground ml-1 text-xs italic">
                                                  ({t("edited")})
                                                </span>
                                              )}
                                            </p>
                                          )}
                                          {showSender && message.body && message.body.trim() !== "" && (
                                            <p className="text-foreground text-left m-0">
                                              {message.body}
                                              {message.isEdited && (
                                                <span className="text-muted-foreground ml-1 text-xs italic">
                                                  ({t("edited")})
                                                </span>
                                              )}
                                            </p>
                                          )}
                                          {message.attachments &&
                                            message.attachments.trim() !== "" && (
                                              <MessageAttachments
                                                attachments={message.attachments}
                                                isFromMe={message.isFromMe}
                                              />
                                            )}
                                          {!showSender && (
                                            <>
                                              <MessageAttachments
                                                attachments={message.attachments || ""}
                                                isFromMe={message.isFromMe}
                                                layout="bubble"
                                              />
                                              <MessageAttachments
                                                attachments={message.attachments || ""}
                                                isFromMe={message.isFromMe}
                                                layout="irc"
                                              />
                                            </>
                                          )}
                                          {(!message.body || message.body.trim() === "") &&
                                            (!message.attachments ||
                                              message.attachments.trim() === "") && (
                                              <p className="text-sm opacity-70 italic">
                                                {t("empty_message")}
                                              </p>
                                            )}
                                        </>
                                      )}
                                    </div>
                                  </>
                                )}
                              </div>
                              {message.isFromMe && (
                                <MessageStatus
                                  message={message}
                                  isGroup={isGroupConversation}
                                  allMessages={mainMessages}
                                  layout="irc"
                                />
                              )}
                            </div>
                            {isUnread && (
                              <span className="text-[10px] font-semibold uppercase tracking-wide text-primary mt-1">
                                {t("unread_indicator")}
                              </span>
                            )}
                          </div>
                        </div>
                        {hasThread && lastThreadMsg && (
                          <button
                            onClick={() => handleThreadClick(message.protocolMsgId)}
                            className="ml-[80px] flex items-center gap-2 p-2 rounded-lg bg-muted/50 hover:bg-muted transition-colors cursor-pointer text-left max-w-[80%]"
                          >
                            <button
                              onClick={() =>
                                handleAvatarClick(
                                  lastThreadMsg.senderAvatarUrl,
                                  getSenderDisplayName(
                                    lastThreadMsg.senderName,
                                    lastThreadMsg.senderId,
                                    lastThreadMsg.isFromMe,
                                    t
                                  )
                                )
                              }
                              className="shrink-0"
                            >
                              <Avatar className="h-5 w-5 shrink-0 cursor-pointer hover:opacity-80 transition-opacity">
                                <AvatarImage src={lastThreadMsg.senderAvatarUrl} />
                                <AvatarFallback className="text-xs">
                                  {getSenderDisplayName(
                                    lastThreadMsg.senderName,
                                    lastThreadMsg.senderId,
                                    lastThreadMsg.isFromMe,
                                    t
                                  )
                                    .substring(0, 2)
                                    .toUpperCase()}
                                </AvatarFallback>
                              </Avatar>
                            </button>
                            <div className="flex-1 min-w-0">
                              <p className="text-sm text-muted-foreground truncate">
                                {lastThreadMsg.body.length > 50
                                  ? `${lastThreadMsg.body.substring(0, 50)}...`
                                  : lastThreadMsg.body}
                              </p>
                              <div className="flex items-center gap-2 mt-1">
                                <p className="text-xs text-muted-foreground/70">
                                  {timeToDate(
                                    lastThreadMsg.timestamp
                                  ).toLocaleTimeString()}
                                </p>
                                {threadCount > 0 && (
                                  <span className="text-xs text-muted-foreground/70">
                                    ·{" "}
                                    {threadCount === 1
                                      ? t("single_reply")
                                      : t("multiple_replies", { count: threadCount })}
                                  </span>
                                )}
                              </div>
                            </div>
                          </button>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}
              {isFetchingNextPage && (
                <div
                  id="loading-bar-top"
                  className="flex justify-center items-center h-16 w-full bg-muted/30"
                >
                  <div className="flex items-center gap-2">
                    <div className="h-4 w-4 border-2 border-primary border-t-transparent rounded-full animate-spin"></div>
                    <span className="text-sm text-muted-foreground">{t("loading")}</span>
                  </div>
                </div>
              )}
            </>
          )}
        </div>
        <div className="shrink-0">
          <ChatInput
            onFileUploadRequest={(files, filePaths) => {
              setPendingFiles(files);
              setPendingFilePaths(filePaths || []);
              setIsFileUploadModalOpen(true);
            }}
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
              <AlertDialogDescription>
                {t("delete_message_description")}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel onClick={() => setDeleteConfirmOpen(false)}>
                {t("cancel")}
              </AlertDialogCancel>
              <AlertDialogAction
                onClick={handleConfirmDelete}
                className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              >
                {t("delete_message")}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </div>
    </>
  );
}
