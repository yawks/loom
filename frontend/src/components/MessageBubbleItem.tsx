import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import type { KeyboardEvent, RefObject } from "react";
import type { VirtuosoHandle } from "react-virtuoso";

import { CallMessage } from "./CallMessage";
import { Input } from "@/components/ui/input";
import { MessageActions } from "./MessageActions";
import { MessageAttachments } from "./MessageAttachments";
import { MessageDateSeparator } from "./MessageDateSeparator";
import { MessageReactions } from "./MessageReactions";
import { MessageStatus } from "./MessageStatus";
import { MessageText } from "./MessageText";
import { MessageThreadPreview } from "./MessageThreadPreview";
import { MessageUnreadDivider } from "./MessageUnreadDivider";
import { cn, timeToDate, extractFirstUrl } from "@/lib/utils";
import { getMessageDomId, getSenderDisplayName, isDifferentDay, normalizeSlackQuotedReply } from "@/lib/messageUtils";
import { LinkPreviewCard } from "./LinkPreviewCard";
import { models } from "../../wailsjs/go/models";
import { useTranslation } from "react-i18next";

export interface MessageHandlers {
  onToggleDeletedMessage: (id: string) => void;
  onEditMessage: (message: models.Message) => void;
  onDeleteClick: (message: models.Message) => void;
  onReplyClick: (message: models.Message) => void;
  onForwardClick: (message: models.Message) => void;
  onReaction: (message: models.Message, emoji: string) => void;
  onRetrySend: (message: models.Message) => void;
  onDeleteLocalMessage: (message: models.Message) => void;
  onSaveEdit: (skipValidation?: boolean) => void;
  onCancelEdit: () => void;
  onThreadClick: (parentMsgId: string, message: models.Message) => void;
  onAvatarClick: (url: string | undefined, name?: string) => void;
  onNavigateToEdit: (direction: "up" | "down", returnFocusToInput?: () => void) => void;
  setOpenActionsMessageId: (id: string | null) => void;
  showToast: (message: string, type?: "error" | "success" | "info", action?: { label: string; onClick: () => void }) => void;
}

interface MessageBubbleItemProps {
  message: models.Message;
  index: number;
  mainMessages: models.Message[];
  conversationId: string;
  providerInstanceId: string | undefined;
  protocol: string | undefined;
  isGroupConversation: boolean;
  conversationReadState: Record<string, boolean>;
  firstUnreadMessageId: string | null;
  isTypingInInput: boolean;
  separatorDismissed: boolean;
  revealedDeletedMessages: Set<string>;
  editingMessageId: string | null;
  editingText: string;
  setEditingText: (text: string) => void;
  editingInputRef: RefObject<HTMLInputElement | null>;
  openActionsMessageId: string | null;
  currentUserId: string | undefined;
  participantNames: Map<string, string>;
  threadsByParent: Record<string, models.Message[]>;
  virtuosoRef: RefObject<VirtuosoHandle | null>;
  handlers: MessageHandlers;
}

export function MessageBubbleItem({
  message,
  index,
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
  editingInputRef,
  openActionsMessageId,
  currentUserId,
  participantNames,
  threadsByParent,
  virtuosoRef,
  handlers,
}: MessageBubbleItemProps) {
  const { t } = useTranslation();
  // The cache can briefly contain Slack's transport representation alongside
  // valid reply metadata. Normalize at the final render boundary so the raw
  // quote is never rendered below the reply card.
  message = normalizeSlackQuotedReply(message);
  const messageId = getMessageDomId(message);
  const prevMessage = index > 0 ? mainMessages[index - 1] : null;
  const prevMessageDate = prevMessage ? timeToDate(prevMessage.timestamp) : null;
  const messageDate = timeToDate(message.timestamp);
  const showDateSeparator = isDifferentDay(messageDate, prevMessageDate);

  const timestamp = timeToDate(message.timestamp);
  const timeString = `${timestamp.getHours().toString().padStart(2, "0")}:${timestamp.getMinutes().toString().padStart(2, "0")}`;

  const isUnread = conversationReadState[messageId] === false;
  const showUnreadDivider = messageId === firstUnreadMessageId && isUnread && !isTypingInInput && !separatorDismissed;

  const isDeleted = Boolean(message.isDeleted);
  const isDeletedRevealed = isDeleted && revealedDeletedMessages.has(messageId);
  const showDeletedPlaceholder = isDeleted && !isDeletedRevealed;
  const isPending = Boolean((message as unknown as Record<string, unknown>).isPending);
  const sendFailed = Boolean((message as unknown as Record<string, unknown>).sendFailed);

  const resolvedSenderName = (!message.isFromMe && message.senderId)
    ? (participantNames.get(message.senderId) || message.senderName)
    : message.senderName;
  const displayName = getSenderDisplayName(resolvedSenderName, message.senderId, message.isFromMe, t);

  const threadMessages = threadsByParent[message.protocolMsgId];
  const hasThread = (threadMessages?.length ?? 0) > 0;
  const hasUnreadInThread = hasThread && threadMessages.some(
    (msg) => !msg.isFromMe && conversationReadState[getMessageDomId(msg)] === false
  );
  const previewUrl = (!isDeleted && message.body) ? extractFirstUrl(message.body) : null;

  const baseBubbleColorClass = message.isFromMe ? "bg-blue-600 text-white" : "bg-muted text-foreground";
  const deletedPlaceholderClass = message.isFromMe ? "bg-blue-950/80 text-blue-100" : "bg-muted/70 text-muted-foreground";
  const deletedRevealedClass = message.isFromMe ? "bg-blue-600/80 text-white" : "bg-muted text-foreground";
  const bubbleClass = cn(
    "rounded-lg p-3 transition-colors border border-transparent text-left",
    isDeleted ? (isDeletedRevealed ? deletedRevealedClass : deletedPlaceholderClass) : baseBubbleColorClass,
    isPending && "opacity-70",
    sendFailed && "border border-destructive bg-destructive/10 opacity-80",
    isUnread && "ring-2 ring-primary/70 bg-primary/10 shadow-lg",
    isDeleted && "border-dashed border-destructive/60 cursor-pointer group"
  );

  const deletedInteractionHandlers = isDeleted
    ? {
        role: "button" as const,
        tabIndex: 0,
        onClick: () => handlers.onToggleDeletedMessage(messageId),
        onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            handlers.onToggleDeletedMessage(messageId);
          }
        },
      }
    : {};

  if (message.callType) {
    return (
      <div className="space-y-2">
        {showDateSeparator && <MessageDateSeparator date={messageDate} />}
        {showUnreadDivider && <MessageUnreadDivider />}
        <div data-message-id={messageId} className="scroll-mt-28">
          <CallMessage message={message} layout="bubble" isGroup={isGroupConversation} />
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-2">
      {showDateSeparator && <MessageDateSeparator date={messageDate} />}
      {showUnreadDivider && <MessageUnreadDivider />}
      <div
        data-message-id={messageId}
        className="space-y-2 scroll-mt-28 group"
        onMouseEnter={() => handlers.setOpenActionsMessageId(messageId)}
        onMouseLeave={() => handlers.setOpenActionsMessageId(null)}
      >
        <div className={cn("flex items-start gap-3", message.isFromMe && "justify-end")}>
          {!message.isFromMe && (
            <div className="flex flex-col items-center shrink-0">
              <button onClick={() => handlers.onAvatarClick(message.senderAvatarUrl, displayName)} className="shrink-0">
                <Avatar className="cursor-pointer hover:opacity-80 transition-opacity">
                  <AvatarImage src={message.senderAvatarUrl} />
                  <AvatarFallback>{displayName.substring(0, 2).toUpperCase()}</AvatarFallback>
                </Avatar>
              </button>
              <span className="text-xs text-muted-foreground mt-1">{timeString}</span>
            </div>
          )}
          <div className="flex flex-col items-start gap-1 relative group/bubble">
            {!isDeleted && editingMessageId !== messageId && (
              <div className={cn("opacity-0 group-hover/bubble:opacity-100 transition-opacity mb-1", message.isFromMe ? "self-end" : "self-start")}>
                <MessageActions
                  isFromMe={message.isFromMe}
                  hasAttachments={Boolean(message.attachments?.trim())}
                  onEdit={() => handlers.onEditMessage(message)}
                  onDelete={() => handlers.onDeleteClick(message)}
                  onReply={() => handlers.onReplyClick(message)}
                  onForward={() => handlers.onForwardClick(message)}
                  onReact={(emoji) => handlers.onReaction(message, emoji)}
                  onStartThread={() => handlers.onThreadClick(message.protocolMsgId, message)}
                  currentReactions={(message.reactions || []).filter((r) => r.userId === currentUserId).map((r) => r.emoji)}
                  messageId={messageId}
                  openActionsMessageId={openActionsMessageId}
                  provider={protocol}
                  instanceId={providerInstanceId}
                />
              </div>
            )}
            <div className="flex items-start gap-2 relative w-full">
              <div className={bubbleClass} aria-live="polite" aria-label={isUnread ? t("unread_message_label") : undefined} {...deletedInteractionHandlers}>
                {sendFailed && (
                  <div className="text-xs text-destructive mb-1 flex items-center gap-2">
                    <span>{t("send_failed") || "Envoi échoué"}</span>
                    <button className="px-2 py-1 text-xs rounded bg-primary text-primary-foreground hover:bg-primary/90" onClick={() => handlers.onRetrySend(message)}>
                      {t("resend") || "Renvoyer"}
                    </button>
                    <button className="px-2 py-1 text-xs rounded border border-destructive text-destructive hover:bg-destructive/10" onClick={() => handlers.onDeleteLocalMessage(message)}>
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
                        if (e.key === "ArrowUp" && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
                          const input = e.currentTarget;
                          if (input.selectionStart === 0 || editingText.trim() === "") {
                            e.preventDefault();
                            e.stopPropagation();
                            handlers.onNavigateToEdit("up");
                          }
                          return;
                        }
                        if (e.key === "ArrowDown" && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
                          const input = e.currentTarget;
                          if (input.selectionStart === input.value.length || editingText.trim() === "") {
                            e.preventDefault();
                            e.stopPropagation();
                            handlers.onNavigateToEdit("down", () => {
                              const chatInput = document.querySelector('textarea[placeholder*="message"], textarea[placeholder*="Message"]') as HTMLTextAreaElement;
                              if (chatInput) setTimeout(() => chatInput.focus(), 0);
                            });
                          }
                          return;
                        }
                        if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); handlers.onSaveEdit(); }
                        else if (e.key === "Escape") { handlers.onCancelEdit(); }
                      }}
                      onBlur={(e) => {
                        const relatedTarget = e.relatedTarget as HTMLElement | null;
                        if (!relatedTarget || (!relatedTarget.closest("button") && !relatedTarget.closest('[role="button"]'))) {
                          handlers.onSaveEdit(false);
                        }
                      }}
                      className="text-foreground"
                      autoFocus
                    />
                    <div className="flex gap-2 justify-end">
                      <button onClick={handlers.onCancelEdit} className="text-xs px-2 py-1 rounded hover:bg-muted">{t("cancel")}</button>
                      <button onClick={() => handlers.onSaveEdit(false)} className="text-xs px-2 py-1 rounded bg-primary text-primary-foreground hover:bg-primary/90">{t("save")}</button>
                    </div>
                  </div>
                ) : (
                  <>
                    {message.quotedMessageId && message.quotedBody && (
                      <div
                        className={cn(
                          "mb-2 pl-3 pr-2 py-1.5 border-l-[3px] cursor-pointer rounded-r transition-colors text-left",
                          message.isFromMe
                            ? "border-white/70 bg-black/10 hover:bg-black/20"
                            : "border-purple-600 dark:border-purple-400 bg-muted/40 hover:bg-muted/70"
                        )}
                        onClick={() => {
                          const quotedId = message.quotedMessageId || "";
                          const quotedIdx = mainMessages.findIndex((m) => m.protocolMsgId === quotedId || getMessageDomId(m) === quotedId);
                          if (quotedIdx >= 0) virtuosoRef.current?.scrollToIndex({ index: quotedIdx, behavior: "smooth", align: "center" });
                        }}
                      >
                        <div className={cn(
                          "text-xs font-semibold mb-0.5 text-left flex items-center gap-1.5",
                          message.isFromMe ? "text-white/90" : "text-purple-700 dark:text-purple-400"
                        )}>
                          {message.quotedSenderName || (message.isFromMe ? t("you") : t("contact"))}
                        </div>
                        <div className={cn("text-xs md:text-sm line-clamp-2 text-left", message.isFromMe ? "text-white/80" : "text-foreground/80")}>
                          <MessageText text={message.quotedBody} providerInstanceId={providerInstanceId} emojiSize={14} isFromMe={message.isFromMe} />
                        </div>
                      </div>
                    )}
                    {message.body?.trim() && (
                      <>
                        <MessageText text={message.body} providerInstanceId={providerInstanceId} className="whitespace-pre-wrap" isFromMe={message.isFromMe} />
                        {previewUrl && <LinkPreviewCard url={previewUrl} isFromMe={message.isFromMe} />}
                      </>
                    )}
                  </>
                )}
                {message.attachments?.trim() && (
                  <MessageAttachments attachments={message.attachments} isFromMe={message.isFromMe} layout="bubble" conversationID={conversationId} messageID={String(message.id)} showToast={handlers.showToast} />
                )}
                {!message.body?.trim() && !message.attachments?.trim() && (
                  <p className="text-sm opacity-70 italic">{t("empty_message")}</p>
                )}
                <div className="flex flex-col mt-1">
                  {showDeletedPlaceholder && (
                    <div className="text-xs italic text-muted-foreground/80 flex items-center gap-2 leading-none">
                      <span>{t("message_deleted")}</span>
                      <span className="text-[10px] uppercase tracking-wide hidden group-hover:inline">{t("click_to_view_deleted")}</span>
                    </div>
                  )}
                  {isDeleted && isDeletedRevealed && (
                    <span className="text-[11px] font-semibold uppercase tracking-wide text-destructive/80">{t("deleted_message_badge")}</span>
                  )}
                </div>
                {message.isEdited && editingMessageId !== messageId && (
                  <span className="text-xs opacity-40 italic">({t("edited")})</span>
                )}
              </div>
            </div>
            <div className={message.isFromMe ? "self-end" : "self-start"}>
              <MessageStatus message={message} isGroup={isGroupConversation} allMessages={mainMessages} layout="bubble" />
            </div>
          </div>
          {message.isFromMe && (
            <div className="flex flex-col items-center shrink-0">
              <button onClick={() => handlers.onAvatarClick(message.senderAvatarUrl || "", t("you"))} className="shrink-0">
                <Avatar className="cursor-pointer hover:opacity-80 transition-opacity">
                  <AvatarImage src={message.senderAvatarUrl || ""} />
                  <AvatarFallback>{t("me")}</AvatarFallback>
                </Avatar>
              </button>
              <span className="text-xs text-muted-foreground mt-1">{timeString}</span>
            </div>
          )}
        </div>
        {(hasThread || (message.reactions && message.reactions.length > 0)) && (
          <div className={cn("flex items-stretch flex-wrap gap-1 mt-1", message.isFromMe ? "ml-auto" : "ml-15")}>
            {hasThread && (
              <MessageThreadPreview
                threadMessages={threadMessages}
                hasUnread={hasUnreadInThread}
                onThreadClick={() => handlers.onThreadClick(message.protocolMsgId, message)}
                onAvatarClick={handlers.onAvatarClick}
              />
            )}
            {message.reactions && message.reactions.length > 0 && (
              <MessageReactions
                reactions={message.reactions}
                isGroup={isGroupConversation}
                participantNames={participantNames}
                currentUserId={currentUserId}
                providerInstanceId={providerInstanceId}
                allMessages={mainMessages}
                onReactionClick={(emoji) => handlers.onReaction(message, emoji)}
                className="mt-0"
              />
            )}
          </div>
        )}
      </div>
    </div>
  );
}
