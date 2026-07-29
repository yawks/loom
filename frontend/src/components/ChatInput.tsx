import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Paperclip, Send, Smile, X } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { GetCustomEmojis, SendMessage, SendReply, SendThreadMessage, SendThreadReply } from "../../wailsjs/go/main/App";
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Theme } from "emoji-picker-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import type { InfiniteData } from "@tanstack/react-query";
import { cn } from "@/lib/utils";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useTranslation } from "react-i18next";
import { htmlFragmentToText } from "@/lib/messageUtils";

interface ChatInputProps {
  onFileUploadRequest?: (files: File[], filePaths?: string[]) => void;
  replyingToMessage?: models.Message | null;
  onCancelReply?: () => void;
  onNavigateToEdit?: (direction: "up" | "down", returnFocusToInput?: () => void) => void;
  threadId?: string;
  currentUserName?: string;
  currentUserAvatarUrl?: string;
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

export function ChatInput({ onFileUploadRequest, replyingToMessage, onCancelReply, onNavigateToEdit, threadId, currentUserName, currentUserAvatarUrl }: ChatInputProps) {
  const { t } = useTranslation();
  const [isEmojiPickerOpen, setIsEmojiPickerOpen] = useState(false);
  const [customEmojis, setCustomEmojis] = useState<CustomEmoji[]>([]);
  const [isDragging, setIsDragging] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const selectedContact = useAppStore((state) => state.selectedContact);
  const selectedProviderFilter = useAppStore((state) => state.selectedProviderFilter);
  const theme = useAppStore((state) => state.theme);
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
  const conversationId = activeAccount?.conversationId || activeAccount?.userId;
  const draftStorageKey = useMemo(
    () => getDraftStorageKey(conversationId, activeAccount?.providerInstanceId, threadId),
    [conversationId, activeAccount?.providerInstanceId, threadId]
  );
  const [message, setMessage] = useState(() => readDraft(draftStorageKey));
  const queryClient = useQueryClient();
  
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
    mutationFn: async ({ conversationId, text, quotedMessageId }: { conversationId: string; text: string; quotedMessageId?: string; quotedBody?: string; quotedSenderName?: string }) => {
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
    onMutate: async ({ conversationId, text, quotedMessageId }) => {
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

  // Auto-resize textarea based on content
  const adjustTextareaHeight = useCallback(() => {
    const textarea = textareaRef.current;
    if (textarea) {
      textarea.style.height = "auto";
      const maxHeight = 200; // Maximum height in pixels
      const newHeight = Math.min(textarea.scrollHeight, maxHeight);
      textarea.style.height = `${newHeight}px`;
    }
  }, []);

  // Restore the draft associated with the currently displayed conversation/thread.
  useEffect(() => {
    const draft = readDraft(draftStorageKey);
    setMessage(draft);
    setIsTypingInInput(draft.trim().length > 0);
    requestAnimationFrame(adjustTextareaHeight);
  }, [adjustTextareaHeight, draftStorageKey, setIsTypingInInput]);

  const handleMessageChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const newValue = e.target.value;
    setMessage(newValue);
    saveDraft(draftStorageKey, newValue);
    adjustTextareaHeight();
    
    // Hide unread divider when user starts typing
    if (newValue.trim().length > 0) {
      setIsTypingInInput(true);
    } else {
      setIsTypingInInput(false);
    }
  };

  const handleSendMessage = async () => {
    if (message.trim() && selectedContact) {
      const text = message.trim();
      const quotedMessageId = replyingToMessage?.protocolMsgId;
      setMessage("");
      saveDraft(draftStorageKey, "");
      setIsTypingInInput(false); // Reset typing state after sending
      if (textareaRef.current) {
        textareaRef.current.style.height = "auto";
      }
      // Clear reply state after sending
      if (onCancelReply) {
        onCancelReply();
      }
      try {
        await sendMessageMutation.mutateAsync({
          conversationId: activeAccount?.conversationId || activeAccount?.userId,
          text,
          quotedMessageId,
        });
      } catch (error) {
        // Error handling is done in onError
        console.error("Failed to send message:", error);
      }
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    // Handle navigation to edit previous/next message
    if (e.key === "ArrowUp" && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
      // Only navigate if cursor is at the start of the textarea or textarea is empty
      const textarea = textareaRef.current;
      const canNavigate = textarea && (textarea.selectionStart === 0 || message.trim() === "");
      console.log("[ChatInput] ArrowUp pressed", {
        hasTextarea: !!textarea,
        selectionStart: textarea?.selectionStart,
        messageLength: message.length,
        messageTrimmed: message.trim().length,
        canNavigate,
        hasOnNavigateToEdit: !!onNavigateToEdit
      });
      
      if (canNavigate) {
        e.preventDefault();
        if (onNavigateToEdit) {
          console.log("[ChatInput] Calling onNavigateToEdit('up')");
          onNavigateToEdit("up", () => {
            // Return focus to textarea
            console.log("[ChatInput] Returning focus to textarea");
            setTimeout(() => {
              textareaRef.current?.focus();
            }, 0);
          });
        } else {
          console.warn("[ChatInput] onNavigateToEdit is not defined");
        }
        return;
      }
    }

    if (e.key === "ArrowDown" && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
      // Only navigate if cursor is at the end of the textarea
      const textarea = textareaRef.current;
      const canNavigate = textarea && (textarea.selectionStart === textarea.value.length || message.trim() === "");
      console.log("[ChatInput] ArrowDown pressed", {
        hasTextarea: !!textarea,
        selectionStart: textarea?.selectionStart,
        valueLength: textarea?.value.length,
        messageLength: message.length,
        messageTrimmed: message.trim().length,
        canNavigate,
        hasOnNavigateToEdit: !!onNavigateToEdit
      });
      
      if (canNavigate) {
        e.preventDefault();
        if (onNavigateToEdit) {
          console.log("[ChatInput] Calling onNavigateToEdit('down')");
          onNavigateToEdit("down", () => {
            // Return focus to textarea
            console.log("[ChatInput] Returning focus to textarea");
            setTimeout(() => {
              textareaRef.current?.focus();
            }, 0);
          });
        } else {
          console.warn("[ChatInput] onNavigateToEdit is not defined");
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
        setCustomEmojis(emojis);
      })
      .catch(() => {
        // Silently ignore
      });
  }, [isEmojiPickerOpen, selectedContact, activeAccount, customEmojis.length]);

  const handleEmojiClick = (emojiData: any) => {
    const emojiText = emojiData.isCustom ? `:${emojiData.unified}:` : emojiData.emoji;
    setMessage((prev) => {
      const newMessage = prev + emojiText;
      saveDraft(draftStorageKey, newMessage);
      return newMessage;
    });
    setIsTypingInInput(true);
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

  return (
    <div className="flex flex-col">
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

          <textarea
            ref={textareaRef}
            value={message}
            onChange={handleMessageChange}
            onKeyDown={handleKeyDown}
            onPaste={handlePaste}
            disabled={isThreadOpen}
            placeholder={t("type_a_message")}
            className="flex-1 min-h-[40px] max-h-[200px] resize-none rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
            rows={1}
            autoCorrect="off"
            autoComplete="off"
            spellCheck="false"
          />
        </div>

        {hasMessage && (
          <Button
            onClick={handleSendMessage}
            size="icon"
            className="shrink-0"
            title={t("send")}
          >
            <Send className="h-5 w-5" />
          </Button>
        )}
      </div>
    </div>
  );
}
