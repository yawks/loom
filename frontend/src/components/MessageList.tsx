import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { GetMessagesForConversation, SendFile } from "../../wailsjs/go/main/App";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient, useSuspenseQuery } from "@tanstack/react-query";

import { ChatInput } from "./ChatInput";
import { FileUploadModal } from "./FileUploadModal";
import { MessageAttachments } from "./MessageAttachments";
import { MessageHeader } from "./MessageHeader";
import { cn } from "@/lib/utils";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useTranslation } from "react-i18next";

// Declare SendFileFromPath as it will be available after Wails bindings are regenerated
declare const SendFileFromPath: ((conversationID: string, filePath: string) => Promise<models.Message>) | undefined;


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

// Wrapper function to use Wails with React Query's suspense mode
const fetchMessages = async (conversationID: string) => {
  return GetMessagesForConversation(conversationID);
};

const getMessageDomId = (message: models.Message): string => {
  if (message.protocolMsgId && message.protocolMsgId.trim().length > 0) {
    return message.protocolMsgId;
  }
  if (message.id) {
    return `message-${message.id}`;
  }
  return `ts-${new Date(message.timestamp).getTime()}`;
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
  const { data: messages } = useSuspenseQuery<models.Message[], Error>({
    queryKey: ["messages", conversationId],
    queryFn: () => fetchMessages(conversationId),
  });
  const syncConversation = useMessageReadStore(
    (state) => state.syncConversation
  );
  const markMessageAsRead = useMessageReadStore((state) => state.markAsRead);
  const readByConversation = useMessageReadStore(
    (state) => state.readByConversation
  );
  const conversationReadState = useMemo(
    () => readByConversation[conversationId] ?? {},
    [readByConversation, conversationId]
  );
  const showThreads = useAppStore((state) => state.showThreads);
  const setShowThreads = useAppStore((state) => state.setShowThreads);
  const setSelectedThreadId = useAppStore((state) => state.setSelectedThreadId);
  const messageLayout = useAppStore((state) => state.messageLayout);
  const hasScrolledToUnreadRef = useRef<string | null>(null);
  const [isFileUploadModalOpen, setIsFileUploadModalOpen] = useState(false);
  const [pendingFiles, setPendingFiles] = useState<File[]>([]);
  const [pendingFilePaths, setPendingFilePaths] = useState<string[]>([]);
  const [isDragging, setIsDragging] = useState(false);

  const handleToggleThreads = () => {
    if (showThreads) {
      // When closing the panel, also clear the selected thread
      setSelectedThreadId(null);
    }
    setShowThreads(!showThreads);
  };

  // Filter out thread messages and group threads by parent message
  const { mainMessages, threadsByParent } = useMemo(() => {
    const main: models.Message[] = [];
    const threads: Record<string, models.Message[]> = {};

    messages.forEach((msg) => {
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
        new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime()
    );

    return { mainMessages: main, threadsByParent: threads };
  }, [messages]);

  useEffect(() => {
    if (!conversationId) {
      return;
    }
    syncConversation(conversationId, mainMessages);
  }, [conversationId, mainMessages, syncConversation]);

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
  }, [conversationId]);

  useEffect(() => {
    const container = scrollContainerRef.current;
    if (!container) {
      return;
    }
    requestAnimationFrame(() => {
      if (
        firstUnreadMessageId &&
        hasScrolledToUnreadRef.current !== firstUnreadMessageId
      ) {
        const target = messageElementsRef.current.get(firstUnreadMessageId);
        if (target) {
          target.scrollIntoView({ block: "center", behavior: "smooth" });
          hasScrolledToUnreadRef.current = firstUnreadMessageId;
          return;
        }
      }
      container.scrollTop = container.scrollHeight;
    });
  }, [firstUnreadMessageId, messages]);

  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const messageElementsRef = useRef<Map<string, HTMLElement>>(new Map());
  const observerRef = useRef<IntersectionObserver | null>(null);
  const [hasWindowFocus, setHasWindowFocus] = useState<boolean>(() =>
    typeof document === "undefined" ? true : document.hasFocus()
  );
  const focusStateRef = useRef<boolean>(hasWindowFocus);

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
        new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime()
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
    for (const file of files) {
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
      <div className="flex-1 overflow-y-auto p-4 min-h-0 scroll-area" ref={scrollContainerRef}>
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
                messageId === firstUnreadMessageId && isUnread;
              const timestampLabel = new Date(
                message.timestamp
              ).toLocaleTimeString();
              
              const messageDate = new Date(message.timestamp);
              const prevMessage = index > 0 ? mainMessages[index - 1] : null;
              const prevMessageDate = prevMessage ? new Date(prevMessage.timestamp) : null;
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
                    className="space-y-2 scroll-mt-28"
                  >
                    <div
                      className={cn(
                        "flex items-start gap-3",
                        message.isFromMe && "justify-end"
                      )}
                    >
                      {!message.isFromMe && (
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
                      )}
                      <div
                        className={cn(
                          "rounded-lg p-3 transition-colors",
                          message.isFromMe
                            ? "bg-blue-600 text-white"
                            : "bg-muted text-foreground",
                          isUnread &&
                            "ring-2 ring-primary/70 bg-primary/10 shadow-lg"
                        )}
                        aria-live="polite"
                        aria-label={
                          isUnread ? t("unread_message_label") : undefined
                        }
                      >
                        {message.body && message.body.trim() !== "" && (
                          <p>{message.body}</p>
                        )}
                        {message.attachments &&
                          message.attachments.trim() !== "" && (
                            <MessageAttachments
                              attachments={message.attachments}
                              isFromMe={message.isFromMe}
                            />
                          )}
                        {(!message.body || message.body.trim() === "") &&
                          (!message.attachments ||
                            message.attachments.trim() === "") && (
                            <p className="text-sm opacity-70 italic">
                              {t("empty_message")}
                            </p>
                          )}
                        <p
                          className={cn(
                            "text-xs mt-1 flex items-center gap-2",
                            message.isFromMe
                              ? "text-blue-100 justify-end"
                              : "text-muted-foreground"
                          )}
                        >
                          {timestampLabel}
                          {isUnread && (
                            <span className="text-[10px] font-semibold uppercase tracking-wide text-primary">
                              {t("unread_indicator")}
                            </span>
                          )}
                        </p>
                      </div>
                      {message.isFromMe && (
                        <button
                          onClick={() => handleAvatarClick("", t("you"))}
                          className="shrink-0"
                        >
                          <Avatar className="cursor-pointer hover:opacity-80 transition-opacity">
                            <AvatarImage src="" />
                            <AvatarFallback>{t("me")}</AvatarFallback>
                          </Avatar>
                        </button>
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
                              {new Date(
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
              const timestamp = new Date(message.timestamp);
              const prevTimestamp = prevMessage
                ? new Date(prevMessage.timestamp)
                : null;
              const timeDiffMinutes = prevTimestamp
                ? (timestamp.getTime() - prevTimestamp.getTime()) / (1000 * 60)
                : Infinity;
              const showSender =
                !prevMessage ||
                prevMessage.senderId !== message.senderId ||
                prevMessage.isFromMe !== message.isFromMe ||
                timeDiffMinutes >= 5;
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
              
              const messageDate = new Date(message.timestamp);
              const prevMessageDate = prevMessage ? new Date(prevMessage.timestamp) : null;
              const showDateSeparator = isDifferentDay(messageDate, prevMessageDate);

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
                      "flex items-start py-1 scroll-mt-28",
                      isUnread && "rounded-md border border-primary/30 bg-primary/5 px-2"
                    )}
                    ref={registerMessageNode(messageId)}
                    data-message-id={messageId}
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
                    <div className="flex flex-col items-start ml-5 flex-1 min-w-0">
                      {showSender ? (
                        <>
                          <span
                            className="font-semibold text-sm text-left h-6 flex items-center mt-2.5"
                            style={{ color: senderColor }}
                          >
                            {displayName}
                          </span>
                          {message.body && message.body.trim() !== "" && (
                            <p className="text-foreground text-left m-0">
                              {message.body}
                            </p>
                          )}
                          {message.attachments &&
                            message.attachments.trim() !== "" && (
                              <MessageAttachments
                                attachments={message.attachments}
                                isFromMe={message.isFromMe}
                              />
                            )}
                        </>
                      ) : (
                        <>
                          {message.body && (
                            <p
                              className="text-foreground text-left m-0 leading-none"
                              style={{ marginTop: "10px" }}
                            >
                              {message.body}
                            </p>
                          )}
                          <MessageAttachments
                            attachments={message.attachments || ""}
                            isFromMe={message.isFromMe}
                          />
                        </>
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
                        <p className="text-sm text-muted-foreground truncate">
                          {lastThreadMsg.body.length > 50
                            ? `${lastThreadMsg.body.substring(0, 50)}...`
                            : lastThreadMsg.body}
                        </p>
                        <div className="flex items-center gap-2 mt-1">
                          <p className="text-xs text-muted-foreground/70">
                            {new Date(
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
    </div>
  );
}
