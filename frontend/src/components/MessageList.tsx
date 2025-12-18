import { AddReaction, DeleteMessage, EditMessage, GetMessagesForConversation, GetMessagesForConversationBefore, GetParticipantNames, RemoveReaction, SendFile, SendMessage, SendReply } from "../../wailsjs/go/main/App";
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
import { ToastContainer, useToast } from "@/components/ui/toast";
import { cn, timeToDate } from "@/lib/utils";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";

import { CallMessage } from "./CallMessage";
import { ChatInput } from "./ChatInput";
import { FileUploadModal } from "./FileUploadModal";
import type { InfiniteData } from "@tanstack/react-query";
import { Input } from "@/components/ui/input";
import type { KeyboardEvent } from "react";
import { MessageActions } from "./MessageActions";
import { MessageAttachments } from "./MessageAttachments";
import { MessageHeader } from "./MessageHeader";
import { MessageReactions } from "./MessageReactions";
import { MessageStatus } from "./MessageStatus";
import { MessageText } from "./MessageText";
import { TypingIndicator } from "./TypingIndicator";
import { models } from "../../wailsjs/go/models";
import { unicodeToSlackEmojiName } from "@/lib/emojiMap";
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
function getColorFromString(str: string | undefined | null): string {
  const safe = (str ?? "").toString();
  if (safe.length === 0) {
    return "hsl(0, 0%, 50%)"; // neutral fallback
  }

  let hash = 0;
  for (let i = 0; i < safe.length; i++) {
    hash = safe.charCodeAt(i) + ((hash << 5) - hash);
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
  // Reset typing state when conversation changes
  const setIsTypingInInput = useAppStore((state) => state.setIsTypingInInput);
  useEffect(() => {
    setIsTypingInInput(false);
  }, [selectedConversation?.id, setIsTypingInInput]);
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
    isFetching,
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

      const oldestTimestamp = timeToDate(oldestMessage.timestamp).toISOString();

      // Only continue if we got a full page (50 messages) - this indicates there might be more
      if (lastPage.length < 50) {
        return undefined;
      }

      return oldestTimestamp;
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

    console.log(`[MessageList] Processing ${messages.length} total messages for conversation ${conversationId}`);

    messages.forEach((msg, index) => {
      // Skip empty messages (no body and no attachments)
      // BUT keep call messages even if they have no body/attachments
      const hasBody = msg.body && msg.body.trim() !== "";
      const hasAttachments = msg.attachments && msg.attachments.trim() !== "";
      const isCallMessage = msg.callType && msg.callType.trim() !== "";
      const isEmpty = !hasBody && !hasAttachments && !isCallMessage;

      if (index < 3) {
        console.log(`[MessageList] Message ${index}:`, {
          protocolMsgId: msg.protocolMsgId,
          threadId: msg.threadId,
          hasBody,
          hasAttachments,
          isCallMessage,
          isEmpty,
          bodyPreview: msg.body?.substring(0, 30)
        });
      }

      if (isEmpty) {
        // Skip empty messages completely (but not call messages)
        console.log(`[MessageList] Skipping empty message ${msg.protocolMsgId}`);
        return;
      }

      // In Slack, parent messages of threads have threadId = protocolMsgId (they reference themselves)
      // Thread replies have threadId = parent's protocolMsgId (different from their own ID)
      const isThreadReply = msg.threadId && msg.threadId !== msg.protocolMsgId;

      if (!msg.threadId || msg.threadId === msg.protocolMsgId) {
        // This is a main message (no thread OR parent of a thread)
        main.push(msg);
      } else if (isThreadReply) {
        // This is a thread reply (threadId points to a different message)
        if (!threads[msg.threadId]) {
          threads[msg.threadId] = [];
        }
        threads[msg.threadId].push(msg);
      }
    });

    console.log(`[MessageList] After processing: ${main.length} main messages, ${Object.keys(threads).length} thread groups`);

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
  const isTypingInInput = useAppStore((state) => state.isTypingInInput);
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
  const editingInputRef = useRef<HTMLInputElement | null>(null);
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [messageToDelete, setMessageToDelete] = useState<{ conversationID: string; messageID: string } | null>(null);
  const [openActionsMessageId, setOpenActionsMessageId] = useState<string | null>(null);
  const [replyingToMessage, setReplyingToMessage] = useState<models.Message | null>(null);
  const { toasts, showToast, closeToast } = useToast();
  const [participantNames, setParticipantNames] = useState<Map<string, string>>(new Map());

  // Get current user ID from messages
  const currentUserId = useMemo(() => {
    for (const msg of messages) {
      if (msg.isFromMe && msg.senderId) {
        return msg.senderId;
      }
    }
    return undefined;
  }, [messages]);

  // Load participant names for groups and Slack conversations
  useEffect(() => {
    if (!conversationId) {
      return;
    }
    const loadParticipantNames = async () => {
      try {
        // Get unique user IDs from reactions and messages
        const userIds = new Set<string>();
        messages.forEach((msg) => {
          // Add sender ID
          if (msg.senderId) {
            userIds.add(msg.senderId);
          }
          // Add reaction user IDs
          if (msg.reactions) {
            msg.reactions.forEach((reaction) => {
              userIds.add(reaction.userId);
            });
          }
        });
        if (userIds.size > 0) {
          // Normalize IDs by removing ":digits" part (LID format for WhatsApp)
          // e.g., "33662865152:47@s.whatsapp.net" -> "33662865152@s.whatsapp.net"
          // For Slack IDs (U1234567890), keep as-is
          const normalizedIds = Array.from(userIds).map(id => {
            // Only normalize WhatsApp IDs, not Slack IDs
            if (id.includes("@s.whatsapp.net") || id.includes("@g.us")) {
              return id.replace(/:\d+@/, "@");
            }
            return id;
          });
          const names = await GetParticipantNames(normalizedIds);

          // Create a map that includes both normalized and original IDs
          const namesMap = new Map<string, string>();
          Array.from(userIds).forEach((originalId, index) => {
            const normalizedId = normalizedIds[index];
            const name = names[normalizedId];
            if (name) {
              // Map both the original ID and normalized ID to the same name
              namesMap.set(originalId, name);
              namesMap.set(normalizedId, name);
            }
          });
          
          // Also add names from message senderName fields (for Slack)
          messages.forEach((msg) => {
            if (msg.senderId && msg.senderName && msg.senderName.trim().length > 0) {
              namesMap.set(msg.senderId, msg.senderName);
            }
          });
          
          setParticipantNames(namesMap);
        }
      } catch (error) {
        console.error("Failed to load participant names:", error);
      }
    };
    loadParticipantNames();
  }, [conversationId, messages]);

  // Handle reaction
  const handleReaction = useCallback(async (message: models.Message, emoji: string) => {
    const protocolMsgId = message.protocolMsgId || getMessageDomId(message);
    const messageReactions = message.reactions || [];
    
    // Determine the Slack emoji name to use
    let slackEmojiName: string;
    
    if (emoji.startsWith(":") && emoji.endsWith(":")) {
      // Already in Slack format (e.g., ":thumbsup:" or ":+1:")
      // Remove colons for API call
      slackEmojiName = emoji.slice(1, -1);
    } else {
      // Unicode emoji (e.g., "👍") - convert to Slack name
      const converted = unicodeToSlackEmojiName(emoji);
      if (!converted) {
        console.error("Failed to convert emoji to Slack format:", emoji);
        showToast(t("error"), "error");
        return;
      }
      slackEmojiName = converted;
    }
    
    // Normalized emoji for DB comparison (with colons)
    const normalizedEmojiForComparison = `:${slackEmojiName}:`;
    
    // Debug: Log all reactions for this message
    console.log(`handleReaction: DEBUG - All reactions for message:`, messageReactions.map(r => ({
      emoji: r.emoji,
      userId: r.userId,
      isCurrentUser: r.userId === currentUserId
    })));
    
    // Check if current user already has this reaction
    const hasReaction = messageReactions.some(
      (r) => {
        // Ensure reaction.emoji has colons for comparison
        let reactionEmoji = r.emoji;
        if (!reactionEmoji.startsWith(":")) reactionEmoji = `:${reactionEmoji}`;
        if (!reactionEmoji.endsWith(":")) reactionEmoji = `${reactionEmoji}:`;
        
        const matches = reactionEmoji === normalizedEmojiForComparison && r.userId === currentUserId;
        if (matches || (reactionEmoji === normalizedEmojiForComparison && currentUserId)) {
          console.log(`handleReaction: DEBUG - Checking reaction: emoji="${reactionEmoji}" vs "${normalizedEmojiForComparison}", userId="${r.userId}" vs "${currentUserId}", matches=${matches}`);
        }
        
        return matches;
      }
    );
    
    console.log(`handleReaction: emoji="${emoji}" -> slackName="${slackEmojiName}", normalized="${normalizedEmojiForComparison}", hasReaction=${hasReaction}, currentUser=${currentUserId}`);

    // Optimistic update: immediately update the cache
    queryClient.setQueryData<InfiniteData<models.Message[]>>(
      ["messages", conversationId],
      (oldData) => {
        if (!oldData) return oldData;
        
        return {
          ...oldData,
          pages: oldData.pages.map(page => 
            page.map(msg => {
              if (msg.protocolMsgId === protocolMsgId || getMessageDomId(msg) === protocolMsgId) {
                const updatedReactions = hasReaction
                  ? // Remove reaction
                    (msg.reactions || []).filter(r => {
                      let reactionEmoji = r.emoji;
                      if (!reactionEmoji.startsWith(":")) reactionEmoji = `:${reactionEmoji}`;
                      if (!reactionEmoji.endsWith(":")) reactionEmoji = `${reactionEmoji}:`;
                      return !(reactionEmoji === normalizedEmojiForComparison && r.userId === currentUserId);
                    })
                  : // Add reaction
                    [
                      ...(msg.reactions || []),
                      models.Reaction.createFrom({
                        id: 0, // Will be set by backend
                        messageId: msg.id,
                        userId: currentUserId || "",
                        emoji: normalizedEmojiForComparison,
                        createdAt: new Date(),
                        updatedAt: new Date(),
                      })
                    ];
                
                console.log(`Optimistic update: ${hasReaction ? 'removed' : 'added'} reaction ${normalizedEmojiForComparison} for message ${protocolMsgId}`);
                
                // Create a new Message instance with updated reactions
                return models.Message.createFrom({
                  ...msg,
                  reactions: updatedReactions
                });
              }
              return msg;
            })
          ),
        };
      }
    );

    try {
      // Call API
      if (hasReaction) {
        console.log(`handleReaction: Calling RemoveReaction("${conversationId}", "${protocolMsgId}", "${slackEmojiName}")`);
        await RemoveReaction(conversationId, protocolMsgId, slackEmojiName);
      } else {
        console.log(`handleReaction: Calling AddReaction("${conversationId}", "${protocolMsgId}", "${slackEmojiName}")`);
        await AddReaction(conversationId, protocolMsgId, slackEmojiName);
      }
      
      // Don't refetch immediately - the optimistic update is enough
      // The DB will be updated via RTM events in the background
      // If we refetch too soon, we'll get the old state from DB before RTM event arrives
      console.log(`Reaction ${hasReaction ? 'removed' : 'added'} successfully, keeping optimistic update`);
      
      // Optional: Refetch after a delay to ensure RTM event has been processed
      // setTimeout(() => {
      //   queryClient.refetchQueries({
      //     queryKey: ["messages", conversationId],
      //   });
      // }, 2000);
    } catch (error) {
      console.error("Failed to handle reaction:", error);
      showToast(t("error"), "error");
      
      // Rollback optimistic update on error
      queryClient.invalidateQueries({
        queryKey: ["messages", conversationId],
      });
      queryClient.refetchQueries({
        queryKey: ["messages", conversationId],
      });
    }
  }, [conversationId, currentUserId, queryClient, t, showToast]);

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

    // Debug log: check if there are unread messages that are not in the loaded messages
    const unreadInStore = Object.entries(conversationReadState)
      .filter(([, isRead]) => !isRead)
      .map(([msgId]) => msgId);
    const unreadNotInMessages = unreadInStore.filter(msgId => !allMessageIds.has(msgId));

    if (unreadNotInMessages.length > 0) {
      console.log(`MessageList: Conversation ${conversationId} - Cleaning up ${unreadNotInMessages.length} unread messages that are not in loaded messages:`, unreadNotInMessages);
      // Force cleanup of these obsolete unread messages
      // This ensures messages that no longer exist (like reactions that were incorrectly counted) are removed
      cleanupObsoleteMessages(conversationId, allMessageIds);
    }
  }, [conversationId, messages, syncConversation, cleanupObsoleteMessages, conversationReadState]);

  // Mark all visible messages as read when conversation is opened
  useEffect(() => {
    if (!conversationId) {
      return;
    }

    // Check if there are unread messages
    const unreadMessages = Object.entries(conversationReadState)
      .filter(([, isRead]) => !isRead)
      .map(([msgId]) => msgId);

    if (unreadMessages.length === 0) {
      return; // No unread messages, nothing to do
    }

    // For Slack, optimize by calling MarkConversationAsRead once instead of MarkMessageAsRead for each message
    const isSlack = selectedConversation.linkedAccounts[0]?.protocol === "slack";
    
    if (isSlack) {
      // Call MarkConversationAsRead once for Slack to mark entire conversation as read
      const markConversationAsReadOnServer = async (convId: string): Promise<void> => {
        return new Promise((resolve, reject) => {
          if (typeof window === "undefined" || !window.go?.main?.App) {
            reject(new Error("Wails runtime not available"));
            return;
          }
          const fn = (window.go.main.App as any).MarkConversationAsRead;
          if (!fn || typeof fn !== 'function') {
            reject(new Error("MarkConversationAsRead not available"));
            return;
          }
          fn(convId)
            .then(() => {
              console.log(`MessageList: Successfully marked Slack conversation ${convId} as read`);
              resolve();
            })
            .catch((error: Error) => {
              console.error(`MessageList: Error marking Slack conversation as read:`, error);
              reject(error);
            });
        });
      };

      console.log(`MessageList: Marking Slack conversation ${conversationId} as read (${unreadMessages.length} unread messages)`);
      markConversationAsReadOnServer(conversationId)
        .then(() => {
          // Mark all messages as read in the store
          unreadMessages.forEach((msgId) => {
            markMessageAsRead(conversationId, msgId);
          });
        })
        .catch((error) => {
          console.error(`MessageList: Failed to mark Slack conversation as read, falling back to individual marks:`, error);
          // Fallback: mark each message individually
          unreadMessages.forEach((msgId) => {
            markMessageAsRead(conversationId, msgId);
          });
        });
    } else {
      // For other providers (WhatsApp), mark each message individually
      // Mark all loaded messages as read
      if (mainMessages.length > 0) {
        mainMessages.forEach((msg) => {
          const messageId = getMessageDomId(msg);
          if (conversationReadState[messageId] === false) {
            markMessageAsRead(conversationId, messageId);
          }
        });
      }

      // Also mark all unread messages in the store as read (including obsolete ones)
      console.log(`MessageList: Marking ${unreadMessages.length} unread messages as read for conversation ${conversationId}`);
      unreadMessages.forEach((msgId) => {
        markMessageAsRead(conversationId, msgId);
      });
    }
  }, [conversationId, mainMessages, markMessageAsRead, conversationReadState, selectedConversation]);

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

  // Invalidate sorting queries when messages are loaded for the first time
  // This ensures the conversation appears in the "Recent" tab with correct sorting
  useEffect(() => {
    if (messages.length > 0 && !isLoading) {
      // Invalidate queries used for sorting conversations
      queryClient.invalidateQueries({ queryKey: ["allLastMessageTimestamps"] });
      queryClient.invalidateQueries({ queryKey: ["allLastMessages"] });
      queryClient.invalidateQueries({ queryKey: ["lastMessage"] });
    }
  }, [messages.length, isLoading, queryClient]);

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
      // Check if the last loaded page was empty to avoid infinite loops
      const lastPageWasEmpty = data?.pages && data.pages.length > 0 && data.pages[data.pages.length - 1].length === 0;

      if (
        isNearTop &&
        hasNextPage &&
        !isFetchingNextPage &&
        !isLoadingMoreRef.current &&
        hasUserScrolledRef.current &&
        !isInitialLoadRef.current &&
        !lastPageWasEmpty && // Don't load if last page was empty
        scrollDelta < 0 // User scrolled up (toward old messages)
      ) {

        console.log(`[MessageList] Loading more messages (hasNextPage: ${hasNextPage}, lastPageWasEmpty: ${lastPageWasEmpty})`);
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
    setShowThreads(true); // Open the threads sidebar
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

  // Navigate to edit previous/next message sent by current user
  const handleNavigateToEdit = useCallback((direction: "up" | "down", returnFocusToInput?: () => void) => {
    console.log("[MessageList] handleNavigateToEdit called", {
      direction,
      editingMessageId,
      messagesCount: messages.length,
      hasReturnFocusToInput: !!returnFocusToInput
    });

    // Get all messages sent by current user, sorted by timestamp (newest first)
    const sentMessages = messages
      .filter((msg) => msg.isFromMe && msg.body && msg.body.trim() !== "")
      .sort((a, b) => {
        const timeA = timeToDate(a.timestamp).getTime();
        const timeB = timeToDate(b.timestamp).getTime();
        return timeB - timeA; // Newest first (index 0 = most recent)
      });

    console.log("[MessageList] Filtered sent messages", {
      sentMessagesCount: sentMessages.length,
      sentMessages: sentMessages.map((msg, idx) => ({
        index: idx,
        id: getMessageDomId(msg),
        body: msg.body?.substring(0, 50),
        timestamp: msg.timestamp
      }))
    });

    if (sentMessages.length === 0) {
      console.log("[MessageList] No sent messages found");
      return;
    }

    let targetMessage: models.Message | null = null;

    if (editingMessageId) {
      // We're already editing a message, find the next/previous one
      const currentIndex = sentMessages.findIndex(
        (msg) => getMessageDomId(msg) === editingMessageId
      );

      console.log("[MessageList] Currently editing message", {
        editingMessageId,
        currentIndex,
        totalSentMessages: sentMessages.length
      });

      if (currentIndex !== -1) {
        if (direction === "up") {
          // Go to previous (older) message
          if (currentIndex < sentMessages.length - 1) {
            targetMessage = sentMessages[currentIndex + 1];
            console.log("[MessageList] Navigating UP to older message", {
              fromIndex: currentIndex,
              toIndex: currentIndex + 1,
              targetMessageId: getMessageDomId(targetMessage)
            });
          } else {
            console.log("[MessageList] Already at oldest message, cannot go up");
          }
          // If already at the oldest message, do nothing
        } else {
          // Go to next (newer) message
          if (currentIndex > 0) {
            targetMessage = sentMessages[currentIndex - 1];
            console.log("[MessageList] Navigating DOWN to newer message", {
              fromIndex: currentIndex,
              toIndex: currentIndex - 1,
              targetMessageId: getMessageDomId(targetMessage)
            });
          } else {
            // At the newest message (index 0), exit edit mode and return focus to input
            console.log("[MessageList] At newest message, exiting edit mode");
            setEditingMessageId(null);
            setEditingText("");
            setOriginalEditText("");
            if (returnFocusToInput) {
              console.log("[MessageList] Calling returnFocusToInput");
              returnFocusToInput();
            }
            return;
          }
        }
      } else {
        console.warn("[MessageList] Current editing message not found in sent messages", {
          editingMessageId
        });
      }
    } else {
      // Not editing, start with the most recent message
      if (direction === "up") {
        targetMessage = sentMessages[0]; // Most recent
        console.log("[MessageList] Starting edit mode with most recent message", {
          targetMessageId: getMessageDomId(targetMessage),
          index: 0
        });
      } else {
        console.log("[MessageList] Not editing and direction is down, doing nothing");
      }
      // If direction is "down" and we're not editing, do nothing
    }

    if (targetMessage) {
      console.log("[MessageList] Setting target message to edit mode", {
        targetMessageId: getMessageDomId(targetMessage),
        body: targetMessage.body?.substring(0, 50)
      });
      handleEditMessage(targetMessage);
      // Scroll to the message being edited
      const messageId = getMessageDomId(targetMessage);
      const messageElement = document.getElementById(messageId);
      if (messageElement) {
        console.log("[MessageList] Scrolling to message element", { messageId });
        messageElement.scrollIntoView({ behavior: "smooth", block: "center" });
      } else {
        console.warn("[MessageList] Message element not found in DOM", { messageId });
      }
    } else {
      console.log("[MessageList] No target message to edit");
    }
  }, [messages, editingMessageId, handleEditMessage]);

  // Attach keyboard event listener directly to the editing input element
  useEffect(() => {
    if (!editingMessageId) {
      return;
    }

    // Wait for the input to be rendered in the DOM
    const timeoutId = setTimeout(() => {
      const inputElement = editingInputRef.current;
      if (!inputElement) {
        console.warn("[MessageList] Edit input element not found in DOM", {
          editingMessageId,
          hasRef: !!editingInputRef.current
        });
        return;
      }

      console.log("[MessageList] Setting up keyboard listener on edit input", {
        editingMessageId,
        hasInputElement: !!inputElement,
        inputValue: inputElement.value?.substring(0, 20)
      });

      const handleKeyDown = (e: Event) => {
        const keyboardEvent = e as globalThis.KeyboardEvent;
        console.log("[MessageList] Key pressed in edit input (direct listener)", {
          key: keyboardEvent.key,
          shiftKey: keyboardEvent.shiftKey,
          ctrlKey: keyboardEvent.ctrlKey,
          metaKey: keyboardEvent.metaKey,
          altKey: keyboardEvent.altKey,
          target: keyboardEvent.target,
          currentTarget: keyboardEvent.currentTarget
        });

        // Handle navigation to previous/next message
        if (keyboardEvent.key === "ArrowUp" && !keyboardEvent.shiftKey && !keyboardEvent.ctrlKey && !keyboardEvent.metaKey && !keyboardEvent.altKey) {
          const input = keyboardEvent.target as HTMLInputElement;
          const currentText = input?.value || editingText;
          const canNavigate = (input?.selectionStart === 0) || currentText.trim() === "";
          console.log("[MessageList] ArrowUp in edit input (direct listener)", {
            selectionStart: input?.selectionStart,
            valueLength: input?.value?.length,
            editingTextTrimmed: currentText.trim().length,
            canNavigate
          });
          
          if (canNavigate) {
            keyboardEvent.preventDefault();
            keyboardEvent.stopPropagation();
            keyboardEvent.stopImmediatePropagation();
            console.log("[MessageList] Calling handleNavigateToEdit('up') from direct listener");
            if (handleNavigateToEdit) {
              handleNavigateToEdit("up");
            }
          }
          return;
        }

        if (keyboardEvent.key === "ArrowDown" && !keyboardEvent.shiftKey && !keyboardEvent.ctrlKey && !keyboardEvent.metaKey && !keyboardEvent.altKey) {
          const input = keyboardEvent.target as HTMLInputElement;
          const currentText = input?.value || editingText;
          const canNavigate = (input?.selectionStart === input?.value?.length) || currentText.trim() === "";
          console.log("[MessageList] ArrowDown in edit input (direct listener)", {
            selectionStart: input?.selectionStart,
            valueLength: input?.value?.length,
            editingTextTrimmed: currentText.trim().length,
            canNavigate
          });
          
          if (canNavigate) {
            keyboardEvent.preventDefault();
            keyboardEvent.stopPropagation();
            keyboardEvent.stopImmediatePropagation();
            console.log("[MessageList] Calling handleNavigateToEdit('down') from direct listener");
            if (handleNavigateToEdit) {
              handleNavigateToEdit("down", () => {
                // Return focus to chat input
                const chatInput = document.querySelector('textarea[placeholder*="message"], textarea[placeholder*="Message"]') as HTMLTextAreaElement;
                if (chatInput) {
                  setTimeout(() => {
                    chatInput.focus();
                  }, 0);
                }
              });
            }
          }
          return;
        }
      };

      inputElement.addEventListener("keydown", handleKeyDown, true); // Use capture phase

      return () => {
        console.log("[MessageList] Removing keyboard listener from edit input");
        inputElement.removeEventListener("keydown", handleKeyDown, true);
      };
    }, 100); // Small delay to ensure DOM is updated

    return () => {
      clearTimeout(timeoutId);
    };
  }, [editingMessageId, editingText, handleNavigateToEdit]);

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
      queryClient.setQueryData<InfiniteData<models.Message[]>>(
        ["messages", conversationId],
        (oldData: InfiniteData<models.Message[]> | undefined) => {
          if (!oldData || !Array.isArray(oldData.pages)) return oldData;
          const pages = oldData.pages.map((page) => {
            if (!Array.isArray(page)) return page as models.Message[];
            const typedPage = page as models.Message[];
            return typedPage.map((msg) => (msg.protocolMsgId === messageId ? { ...msg, body: originalText } : msg));
          });
          return { ...oldData, pages: pages as models.Message[][] };
        }
      );
      // Show error toast
      showToast(t("edit_failed"), "error");
    }
  }, [editingMessageId, editingText, originalEditText, conversationId, messages, queryClient, t, showToast]);

  const handleCancelEdit = useCallback(() => {
    setEditingMessageId(null);
    setEditingText("");
  }, []);

  // Helpers for optimistic cache updates (failed/pending messages)
  const markMessageState = useCallback(
    (conversationId: string, protocolMsgId: string, updates: Partial<models.Message>) => {
      queryClient.setQueryData<InfiniteData<models.Message[]>>(
        ["messages", conversationId],
        (oldData: InfiniteData<models.Message[]> | undefined) => {
          if (!oldData || !Array.isArray(oldData.pages)) return oldData;
          const pages = oldData.pages.map((page) => {
            if (!Array.isArray(page)) return page as models.Message[];
            const typedPage = page as models.Message[];
            return typedPage.map((msg) =>
              msg.protocolMsgId === protocolMsgId ? ({ ...(msg as any), ...updates } as models.Message) : msg
            );
          });
          return { ...oldData, pages: pages as models.Message[][] };
        }
      );
    },
    [queryClient]
  );

  const removeMessageFromCache = useCallback(
    (conversationId: string, protocolMsgId: string) => {
      queryClient.setQueryData<InfiniteData<models.Message[]>>(
        ["messages", conversationId],
        (oldData: InfiniteData<models.Message[]> | undefined) => {
          if (!oldData || !Array.isArray(oldData.pages)) return oldData;
          const pages = oldData.pages.map((page) => {
            if (!Array.isArray(page)) return page as models.Message[];
            const typedPage = page as models.Message[];
            return typedPage.filter((msg) => msg.protocolMsgId !== protocolMsgId);
          });
          return { ...oldData, pages: pages as models.Message[][] };
        }
      );
    },
    [queryClient]
  );

  const handleRetrySend = useCallback(
    async (message: models.Message) => {
      const convId = message.protocolConvId;
      const text = message.body || "";
      const quotedId = message.quotedMessageId || undefined;
      if (!convId || !text.trim()) {
        return;
      }

      // Mark as pending again
      markMessageState(convId, message.protocolMsgId, { isPending: true, sendFailed: false } as any);

      try {
        const sent = quotedId
          ? await SendReply(convId, text, quotedId)
          : await SendMessage(convId, text);

        // Replace with real message
        queryClient.setQueryData<InfiniteData<models.Message[]>>(
          ["messages", convId],
          (oldData: InfiniteData<models.Message[]> | undefined) => {
            if (!oldData || !Array.isArray(oldData.pages)) return oldData;
            const pages = oldData.pages.map((page) => {
              if (!Array.isArray(page)) return page as models.Message[];
              const typedPage = page as models.Message[];
              return typedPage.map((msg) =>
                msg.protocolMsgId === message.protocolMsgId
                  ? ({ ...(sent as any), isPending: false, sendFailed: false } as models.Message)
                  : msg
              );
            });
            return { ...oldData, pages: pages as models.Message[][] };
          }
        );
      } catch (err) {
        console.error("Retry send failed", err);
        markMessageState(convId, message.protocolMsgId, { isPending: false, sendFailed: true } as any);
      }
    },
    [markMessageState, queryClient]
  );

  const handleDeleteLocalMessage = useCallback(
    (message: models.Message) => {
      const convId = message.protocolConvId;
      const msgId = message.protocolMsgId;
      if (!convId || !msgId) return;

      removeMessageFromCache(convId, msgId);
    },
    [removeMessageFromCache]
  );

  const handleDeleteClick = useCallback((message: models.Message) => {
    const protocolMsgId = message.protocolMsgId || getMessageDomId(message);
    setMessageToDelete({
      conversationID: conversationId,
      messageID: protocolMsgId,
    });
    setDeleteConfirmOpen(true);
  }, [conversationId]);

  const handleReplyClick = useCallback((message: models.Message) => {
    setReplyingToMessage(message);
  }, []);

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
          {(isLoading || (isFetching && messages.length === 0)) ? (
            <div className="flex items-center justify-center h-full">
              <div className="flex flex-col items-center gap-4">
                <div className="relative">
                  <svg
                    className="w-16 h-16 text-primary"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    viewBox="0 0 24 24"
                  style={{
                      animation: "shimmer 2s ease-in-out infinite",
                      filter: "drop-shadow(0 0 8px currentColor)"
                    }}
                  >
                    <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path>
                    <polyline points="7 10 12 15 17 10"></polyline>
                    <line x1="12" y1="15" x2="12" y2="3"></line>
                  </svg>
                </div>
                <p 
                  className="text-muted-foreground text-sm"
                  style={{
                    animation: "shimmer 2s ease-in-out infinite"
                  }}
                >
                  {t("fetching_messages") || "Récupération des messages"}
                </p>
              </div>
            </div>
          ) : (
            <>
              {messageLayout === "bubble" ? (
                <div className="space-y-4">
                  {mainMessages.map((message, index) => {
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
                      messageId === firstUnreadMessageId && isUnread && !isTypingInInput;
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
                    const isPending = (message as any).isPending;
                    const sendFailed = (message as any).sendFailed;
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
                      "rounded-lg p-3 transition-colors border border-transparent text-left",
                      isDeleted
                        ? isDeletedRevealed
                          ? deletedRevealedClass
                          : deletedPlaceholderClass
                        : baseBubbleColorClass,
                      isPending && "opacity-70",
                      sendFailed && "border border-destructive bg-destructive/10 opacity-80",
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

                    // Check if this is a call message
                    if (message.callType) {
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
                              className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-primary my-2"
                              role="separator"
                              aria-label={t("new") || "New"}
                            >
                              <span className="h-px flex-1 bg-border" />
                              <span className="px-2">{t("new") || "New"}</span>
                              <span className="h-px flex-1 bg-border" />
                            </div>
                          )}
                          <div
                            ref={registerMessageNode(messageId)}
                            data-message-id={messageId}
                            className="scroll-mt-28"
                          >
                            <CallMessage
                              message={message}
                              layout="bubble"
                              isGroup={isGroupConversation}
                            />
                          </div>
                        </div>
                      );
                    }

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
                              {!isDeleted && editingMessageId !== messageId && (
                                <div className={cn(
                                  "opacity-0 group-hover/bubble:opacity-100 transition-opacity mb-1",
                                  message.isFromMe ? "self-end" : "self-start"
                                )}>
                                  <MessageActions
                                    isFromMe={message.isFromMe}
                                    hasAttachments={Boolean(message.attachments && message.attachments.trim() !== "")}
                                    onEdit={() => handleEditMessage(message)}
                                    onDelete={() => handleDeleteClick(message)}
                                    onReply={() => handleReplyClick(message)}
                                    onReact={(emoji) => handleReaction(message, emoji)}
                                    currentReactions={(message.reactions || [])
                                      .filter((r) => r.userId === currentUserId)
                                      .map((r) => r.emoji)}
                                    messageId={messageId}
                                    openActionsMessageId={openActionsMessageId}
                                  />
                                </div>
                              )}
                              <div className="flex items-start gap-2 relative w-full">
                                <div
                                  className={bubbleClass}
                                  aria-live="polite"
                                  aria-label={
                                    isUnread ? t("unread_message_label") : undefined
                                  }
                                  {...deletedInteractionHandlers}
                                >
                                  {isPending && (
                                    <div className="text-xs opacity-80 mb-1">
                                      {t("sending") || "Envoi en cours…"}
                                    </div>
                                  )}
                                  {sendFailed && (
                                    <div className="text-xs text-destructive mb-1 flex items-center gap-2">
                                      <span>{t("send_failed") || "Envoi échoué"}</span>
                                      <button
                                        className="px-2 py-1 text-xs rounded bg-primary text-primary-foreground hover:bg-primary/90"
                                        onClick={() => handleRetrySend(message)}
                                      >
                                        {t("resend") || "Renvoyer"}
                                      </button>
                                      <button
                                        className="px-2 py-1 text-xs rounded border border-destructive text-destructive hover:bg-destructive/10"
                                        onClick={() => handleDeleteLocalMessage(message)}
                                      >
                                        {t("delete") || "Supprimer"}
                                      </button>
                                    </div>
                                  )}
                                  {editingMessageId === messageId ? (
                                    <div className="flex flex-col gap-2">
                                      <Input
                                        ref={editingInputRef}
                                        value={editingText}
                                        onChange={(e) => setEditingText(e.target.value)}
                                        onKeyDown={(e) => {
                                          console.log("[MessageList] Key pressed in edit input", {
                                            key: e.key,
                                            shiftKey: e.shiftKey,
                                            ctrlKey: e.ctrlKey,
                                            metaKey: e.metaKey,
                                            altKey: e.altKey,
                                            selectionStart: e.currentTarget.selectionStart,
                                            valueLength: e.currentTarget.value.length,
                                            editingTextLength: editingText.length,
                                            editingTextTrimmed: editingText.trim().length,
                                            hasHandleNavigateToEdit: !!handleNavigateToEdit
                                          });

                                          // Handle navigation to previous/next message
                                          if (e.key === "ArrowUp" && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
                                            // Only navigate if cursor is at the start of the input or input is empty
                                            const input = e.currentTarget;
                                            const canNavigate = input.selectionStart === 0 || editingText.trim() === "";
                                            console.log("[MessageList] ArrowUp in edit input", {
                                              selectionStart: input.selectionStart,
                                              valueLength: input.value.length,
                                              editingTextTrimmed: editingText.trim().length,
                                              canNavigate
                                            });
                                            
                                            if (canNavigate) {
                                              e.preventDefault();
                                              e.stopPropagation();
                                              console.log("[MessageList] Calling handleNavigateToEdit('up') from edit input");
                                              if (handleNavigateToEdit) {
                                                handleNavigateToEdit("up");
                                              } else {
                                                console.error("[MessageList] handleNavigateToEdit is not defined!");
                                              }
                                            }
                                            return;
                                          }

                                          if (e.key === "ArrowDown" && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
                                            // Only navigate if cursor is at the end of the input
                                            const input = e.currentTarget;
                                            const canNavigate = input.selectionStart === input.value.length || editingText.trim() === "";
                                            console.log("[MessageList] ArrowDown in edit input", {
                                              selectionStart: input.selectionStart,
                                              valueLength: input.value.length,
                                              editingTextTrimmed: editingText.trim().length,
                                              canNavigate
                                            });
                                            
                                            if (canNavigate) {
                                              e.preventDefault();
                                              e.stopPropagation();
                                              console.log("[MessageList] Calling handleNavigateToEdit('down') from edit input");
                                              if (handleNavigateToEdit) {
                                                handleNavigateToEdit("down", () => {
                                                  // Return focus to chat input
                                                  const chatInput = document.querySelector('textarea[placeholder*="message"], textarea[placeholder*="Message"]') as HTMLTextAreaElement;
                                                  if (chatInput) {
                                                    setTimeout(() => {
                                                      chatInput.focus();
                                                    }, 0);
                                                  }
                                                });
                                              } else {
                                                console.error("[MessageList] handleNavigateToEdit is not defined!");
                                              }
                                            }
                                            return;
                                          }

                                          if (e.key === "Enter" && !e.shiftKey) {
                                            e.preventDefault();
                                            handleSaveEdit();
                                          } else if (e.key === "Escape") {
                                            handleCancelEdit();
                                          }
                                        }}
                                        onKeyDownCapture={(e) => {
                                          // Try capture phase to catch events earlier
                                          if ((e.key === "ArrowUp" || e.key === "ArrowDown") && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
                                            console.log("[MessageList] Key captured in edit input (capture phase)", {
                                              key: e.key,
                                              target: e.target,
                                              currentTarget: e.currentTarget
                                            });
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
                                      {message.quotedMessageId && message.quotedBody && (
                                        <div
                                          className="mb-2 pl-3 border-l-4 border-primary/50 py-1 cursor-pointer hover:bg-muted/50 rounded transition-colors text-left"
                                          onClick={() => {
                                            const quotedElement = messageElementsRef.current.get(message.quotedMessageId || "");
                                            if (quotedElement && scrollContainerRef.current) {
                                              quotedElement.scrollIntoView({ behavior: "smooth", block: "center" });
                                            }
                                          }}
                                        >
                                          <div className="text-xs font-medium opacity-80 text-left">
                                            {message.quotedSenderName || (message.isFromMe ? t("you") : t("contact"))}
                                          </div>
                                          <div className="text-sm opacity-70 line-clamp-2 text-left">
                                            <MessageText
                                              text={message.quotedBody}
                                              providerInstanceId={selectedConversation.linkedAccounts[0]?.providerInstanceId}
                                              isSlack={selectedConversation.linkedAccounts[0]?.protocol === "slack"}
                                              emojiSize={14}
                                              isFromMe={message.isFromMe}
                                              participantNames={participantNames}
                                              allMessages={mainMessages}
                                            />
                                          </div>
                                        </div>
                                      )}
                                      {message.body && message.body.trim() !== "" && (
                                        <MessageText
                                          text={message.body}
                                          providerInstanceId={selectedConversation.linkedAccounts[0]?.providerInstanceId}
                                          isSlack={selectedConversation.linkedAccounts[0]?.protocol === "slack"}
                                          className="whitespace-pre-wrap"
                                          isFromMe={message.isFromMe}
                                          participantNames={participantNames}
                                          allMessages={mainMessages}
                                        />
                                      )}
                                    </>
                                  )}
                                  {message.attachments &&
                                    message.attachments.trim() !== "" && (
                                      <MessageAttachments
                                        attachments={message.attachments}
                                        isFromMe={message.isFromMe}
                                        layout="bubble"
                                        conversationID={conversationId}
                                        messageID={String(message.id)}
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
                              </div>
                              {message.isEdited && (
                                <span className={cn(
                                  "text-xs text-muted-foreground italic",
                                  message.isFromMe ? "self-end" : "self-start"
                                )}>
                                  {t("edited")}
                                </span>
                              )}
                              {message.reactions && message.reactions.length > 0 && (
                                <MessageReactions
                                  reactions={message.reactions}
                                  isGroup={isGroupConversation}
                                  participantNames={participantNames}
                                  currentUserId={currentUserId}
                                  providerInstanceId={selectedConversation.linkedAccounts[0]?.providerInstanceId}
                                  allMessages={mainMessages}
                                  onReactionClick={(emoji) => handleReaction(message, emoji)}
                                  className={message.isFromMe ? "self-end" : "self-start"}
                                />
                              )}
                              <div className={message.isFromMe ? "self-end" : "self-start"}>
                                <MessageStatus
                                  message={message}
                                  isGroup={isGroupConversation}
                                  allMessages={mainMessages}
                                  layout="bubble"
                                />
                              </div>
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
                                <div className="text-sm text-muted-foreground truncate">
                                  <MessageText
                                    text={lastThreadMsg.body}
                                    providerInstanceId={selectedConversation.linkedAccounts[0]?.providerInstanceId}
                                    isSlack={selectedConversation.linkedAccounts[0]?.protocol === "slack"}
                                    emojiSize={14}
                                    preview={true}
                                    isFromMe={lastThreadMsg.isFromMe}
                                    participantNames={participantNames}
                                    allMessages={mainMessages}
                                  />
                                </div>
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
                      messageId === firstUnreadMessageId && isUnread && !isTypingInInput;

                    const messageDate = timeToDate(message.timestamp);
                    const prevMessageDate = prevMessage ? timeToDate(prevMessage.timestamp) : null;
                    const showDateSeparator = isDifferentDay(messageDate, prevMessageDate);
                    const isDeletedRevealed =
                      isDeleted && revealedDeletedMessages.has(messageId);

                    // Check if this is a call message
                    if (message.callType) {
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
                              className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-primary my-2"
                              role="separator"
                              aria-label={t("new") || "New"}
                            >
                              <span className="h-px flex-1 bg-border" />
                              <span className="px-2">{t("new") || "New"}</span>
                              <span className="h-px flex-1 bg-border" />
                            </div>
                          )}
                          <div
                            ref={registerMessageNode(messageId)}
                            data-message-id={messageId}
                            className="scroll-mt-28"
                          >
                            <CallMessage
                              message={message}
                              layout="irc"
                              isGroup={isGroupConversation}
                            />
                          </div>
                        </div>
                      );
                    }
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
                          <div className="flex flex-col items-start ml-5 flex-1 min-w-0 relative group">
                            {showSender && (
                              <div className="w-full flex items-center relative">
                                <span
                                  className="font-semibold text-sm text-left h-6 flex items-center mt-2.5"
                                  style={{ color: senderColor }}
                                >
                                  {displayName}
                                </span>
                                {!isDeleted && editingMessageId !== messageId && (
                                  <div className="absolute right-0 top-2.5 opacity-0 group-hover:opacity-100 transition-opacity z-50">
                                    <MessageActions
                                      isFromMe={message.isFromMe}
                                      hasAttachments={Boolean(message.attachments && message.attachments.trim() !== "")}
                                      onEdit={() => handleEditMessage(message)}
                                      onDelete={() => handleDeleteClick(message)}
                                      onReply={() => handleReplyClick(message)}
                                      onReact={(emoji) => handleReaction(message, emoji)}
                                      currentReactions={(message.reactions || [])
                                        .filter((r) => r.userId === currentUserId)
                                        .map((r) => r.emoji)}
                                      messageId={messageId}
                                      openActionsMessageId={openActionsMessageId}
                                    />
                                  </div>
                                )}
                              </div>
                            )}
                            {!showSender && !isDeleted && editingMessageId !== messageId && (
                              <div className="absolute right-0 top-0 opacity-0 group-hover:opacity-100 transition-opacity z-50">
                                <MessageActions
                                  isFromMe={message.isFromMe}
                                  hasAttachments={Boolean(message.attachments && message.attachments.trim() !== "")}
                                  onEdit={() => handleEditMessage(message)}
                                  onDelete={() => handleDeleteClick(message)}
                                  onReply={() => handleReplyClick(message)}
                                  onReact={(emoji) => handleReaction(message, emoji)}
                                  currentReactions={(message.reactions || [])
                                    .filter((r) => r.userId === currentUserId)
                                    .map((r) => r.emoji)}
                                  messageId={messageId}
                                  openActionsMessageId={openActionsMessageId}
                                />
                              </div>
                            )}
                            <div className="w-full flex items-start gap-2">
                              <div className="flex-1 rounded-md transition-colors hover:bg-muted/50 -ml-2 pl-2 -mr-2 pr-2 relative">
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
                                          {message.quotedMessageId && message.quotedBody && (
                                            <div
                                              className="mb-2 pl-3 border-l-4 border-primary/50 py-1 cursor-pointer hover:bg-muted/50 rounded transition-colors text-left"
                                              onClick={() => {
                                                const quotedElement = messageElementsRef.current.get(message.quotedMessageId || "");
                                                if (quotedElement && scrollContainerRef.current) {
                                                  quotedElement.scrollIntoView({ behavior: "smooth", block: "center" });
                                                }
                                              }}
                                            >
                                              <div className="text-xs font-medium opacity-80 text-left">
                                                {message.quotedSenderName || (message.isFromMe ? t("you") : t("contact"))}
                                              </div>
                                              <div className="text-sm opacity-70 line-clamp-2 text-left">
                                                <MessageText
                                                  text={message.quotedBody}
                                                  providerInstanceId={selectedConversation.linkedAccounts[0]?.providerInstanceId}
                                                  isSlack={selectedConversation.linkedAccounts[0]?.protocol === "slack"}
                                                  emojiSize={14}
                                                  isFromMe={message.isFromMe}
                                                  participantNames={participantNames}
                                                  allMessages={mainMessages}
                                                />
                                              </div>
                                            </div>
                                          )}
                                          {!showSender && message.body && (
                                            <div
                                              className="text-foreground text-left m-0 leading-none"
                                              style={{ marginTop: message.quotedMessageId ? "0" : "10px" }}
                                            >
                                              <MessageText
                                                text={message.body}
                                                providerInstanceId={selectedConversation.linkedAccounts[0]?.providerInstanceId}
                                                isSlack={selectedConversation.linkedAccounts[0]?.protocol === "slack"}
                                                emojiSize={16}
                                                isFromMe={message.isFromMe}
                                                participantNames={participantNames}
                                                allMessages={mainMessages}
                                              />
                                              {message.isEdited && (
                                                <span className="text-muted-foreground ml-1 text-xs italic">
                                                  ({t("edited")})
                                                </span>
                                              )}
                                            </div>
                                          )}
                                          {showSender && message.body && message.body.trim() !== "" && (
                                            <div className="text-foreground text-left m-0">
                                              <MessageText
                                                text={message.body}
                                                providerInstanceId={selectedConversation.linkedAccounts[0]?.providerInstanceId}
                                                isSlack={selectedConversation.linkedAccounts[0]?.protocol === "slack"}
                                                emojiSize={16}
                                                isFromMe={message.isFromMe}
                                                participantNames={participantNames}
                                                allMessages={mainMessages}
                                              />
                                              {message.isEdited && (
                                                <span className="text-muted-foreground ml-1 text-xs italic">
                                                  ({t("edited")})
                                                </span>
                                              )}
                                            </div>
                                          )}
                                          {message.attachments &&
                                            message.attachments.trim() !== "" && (
                                              <MessageAttachments
                                                attachments={message.attachments}
                                                isFromMe={message.isFromMe}
                                                conversationID={conversationId}
                                                messageID={String(message.id)}
                                                layout={messageLayout as "bubble" | "irc"}
                                              />
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
                                <div className="self-end">
                                  <MessageStatus
                                    message={message}
                                    isGroup={isGroupConversation}
                                    allMessages={mainMessages}
                                    layout="irc"
                                  />
                                </div>
                              )}
                            </div>
                            {message.reactions && message.reactions.length > 0 && (
                              <MessageReactions
                                reactions={message.reactions}
                                isGroup={isGroupConversation}
                                participantNames={participantNames}
                                currentUserId={currentUserId}
                                providerInstanceId={selectedConversation.linkedAccounts[0]?.providerInstanceId}
                                allMessages={mainMessages}
                                onReactionClick={(emoji) => handleReaction(message, emoji)}
                              />
                            )}
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
                              <div className="text-sm text-muted-foreground truncate">
                                <MessageText
                                  text={lastThreadMsg.body}
                                  providerInstanceId={selectedConversation.linkedAccounts[0]?.providerInstanceId}
                                  isSlack={selectedConversation.linkedAccounts[0]?.protocol === "slack"}
                                  emojiSize={14}
                                  preview={true}
                                  isFromMe={lastThreadMsg.isFromMe}
                                  participantNames={participantNames}
                                  allMessages={mainMessages}
                                />
                              </div>
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
