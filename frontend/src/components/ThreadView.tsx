import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { useMemo, useState, useEffect, useRef, useCallback } from "react";

import { Button } from "@/components/ui/button";
import { ChatInput } from "./ChatInput";
import { AddReaction, DeleteMessage, GetThreadMessages, RemoveReaction } from "../../wailsjs/go/main/App";
import { MessageActions } from "./MessageActions";
import { MessageAttachments } from "./MessageAttachments";
import { MessageReactions } from "./MessageReactions";
import { MessageText } from "./MessageText";
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
import { X } from "lucide-react";
import type { models } from "../../wailsjs/go/models";
import { getColorFromString, getMessageDomId, getSenderDisplayName } from "@/lib/messageUtils";
import { cn, timeToDate } from "@/lib/utils";
import { unicodeEmojiMap, unicodeToEmojiName } from "@/lib/emojiMap";
import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useMessageEdit } from "@/hooks/useMessageEdit";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

const fetchThreads = async (conversationID: string, threadID: string) => {
  return GetThreadMessages(conversationID, threadID);
};

export function ThreadView() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { toasts, showToast, closeToast } = useToast();

  const selectedThreadId = useAppStore((state) => state.selectedThreadId);
  const setSelectedThreadId = useAppStore((state) => state.setSelectedThreadId);
  const setShowThreads = useAppStore((state) => state.setShowThreads);
  const messageLayout = useAppStore((state) => state.messageLayout);
  const setSelectedAvatarUrl = useAppStore(
    (state) => state.setSelectedAvatarUrl
  );
  const selectedContact = useAppStore((state) => state.selectedContact);

  const activeAccount = selectedContact?.linkedAccounts[0];
  const protocol = activeAccount?.protocol;
  const providerInstanceId = activeAccount?.providerInstanceId;

  // Get conversation ID from selected contact
  const conversationId =
    activeAccount?.conversationId ??
    activeAccount?.userId ??
    "";

  const scrollContainerRef = useRef<HTMLDivElement>(null);

  // State for reply input & hover actions
  const [replyingToMessage, setReplyingToMessage] = useState<models.Message | null>(null);
  const [openActionsMessageId, setOpenActionsMessageId] = useState<string | null>(null);
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [messageToDelete, setMessageToDelete] = useState<models.Message | null>(null);

  const handleClose = () => {
    setSelectedThreadId(null);
    setShowThreads(false);
    setReplyingToMessage(null);
  };

  const handleAvatarClick = (avatarUrl: string | undefined, displayName?: string) => {
    // Use avatar URL if available, otherwise use a placeholder based on display name
    const urlToShow = avatarUrl || (displayName ? `https://api.dicebear.com/7.x/initials/svg?seed=${encodeURIComponent(displayName)}` : null);
    if (urlToShow) {
      setSelectedAvatarUrl(urlToShow);
    }
  };

  // Use useQuery instead of useSuspenseQuery to handle conditional rendering
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

  // Sort thread messages by timestamp and filter out empty messages
  const sortedThreadMessages = useMemo(() => {
    if (!threadMessages || threadMessages.length === 0) return [];
    // Filter out empty messages (no body and no attachments)
    const filtered = threadMessages.filter((msg) => {
      const hasBody = msg.body && msg.body.trim() !== "";
      const hasAttachments = msg.attachments && msg.attachments.trim() !== "";
      return hasBody || hasAttachments;
    });
    return [...filtered].sort(
      (a, b) =>
        timeToDate(a.timestamp).getTime() - timeToDate(b.timestamp).getTime()
    );
  }, [threadMessages]);

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
      const useNativeEmoji = protocol === "googlechat" || protocol === "whatsapp";

      const getCleanName = (emojiStr: string): string => {
        const clean = emojiStr.startsWith(":") && emojiStr.endsWith(":") ? emojiStr.slice(1, -1) : emojiStr;
        const unicode = unicodeEmojiMap[clean] || clean;
        const name = unicodeToEmojiName(unicode);
        return name || clean;
      };

      const targetName = getCleanName(emoji);
      let apiEmoji: string;
      if (useNativeEmoji) {
        const clean = emoji.startsWith(":") && emoji.endsWith(":") ? emoji.slice(1, -1) : emoji;
        const resolvedUnicode = unicodeEmojiMap[clean];
        apiEmoji = resolvedUnicode ? resolvedUnicode : clean;
      } else {
        apiEmoji = targetName;
      }

      const hasReaction = messageReactions.some((r) => {
        return getCleanName(r.emoji) === targetName && (currentUserId ? r.userId === currentUserId : false);
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
    [conversationId, selectedThreadId, protocol, currentUserId, queryClient]
  );

  // Scroll to bottom when new messages arrive (e.g. after sending)
  useEffect(() => {
    if (!scrollContainerRef.current || sortedThreadMessages.length === 0) return;
    const el = scrollContainerRef.current;
    const isNearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 100;
    const lastMsg = sortedThreadMessages[sortedThreadMessages.length - 1];
    if (isNearBottom || lastMsg?.isFromMe) {
      el.scrollTop = el.scrollHeight;
    }
  }, [sortedThreadMessages]);

  // Mark all unread thread messages as read as soon as the panel is visible with content.
  useEffect(() => {
    if (!selectedThreadId || !conversationId || sortedThreadMessages.length === 0) return;
    const unreadIds = sortedThreadMessages
      .filter((msg) => !msg.isFromMe)
      .map((msg) => getMessageDomId(msg));
    if (unreadIds.length > 0) markMultipleAsRead(conversationId, unreadIds);
  }, [selectedThreadId, conversationId, sortedThreadMessages, markMultipleAsRead]);

  if (!selectedThreadId) return null;

  if (isLoading) {
    return (
      <div className="flex-1 flex items-center justify-center text-muted-foreground">
        {t("loading") || "Loading…"}
      </div>
    );
  }

  return (
    <div className="thread-view flex flex-col h-full overflow-hidden">
      <div className="thread-view__header px-4 py-3 border-b flex items-center justify-between shrink-0">
        <h3 className="thread-view__title font-semibold text-base">Thread</h3>
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
                    {/* Left column */}
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
                    {/* Right column */}
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
      {/* Reply input for thread */}
      <div className="border-t shrink-0">
        <ChatInput
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
