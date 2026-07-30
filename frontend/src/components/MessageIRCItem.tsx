import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import type { KeyboardEvent, RefObject } from "react";
import { cn, timeToDate, extractFirstUrl } from "@/lib/utils";
import { LinkPreviewCard } from "./LinkPreviewCard";
import { getColorFromString, getMessageDomId, getSenderDisplayName, isDifferentDay, normalizeSlackQuotedReply } from "@/lib/messageUtils";

import { CallMessage } from "./CallMessage";
import { Input } from "@/components/ui/input";
import { MessageActions } from "./MessageActions";
import { MessageAttachments } from "./MessageAttachments";
import { MessageDateSeparator } from "./MessageDateSeparator";
import type { MessageHandlers } from "./MessageBubbleItem";
import { MessageReactions } from "./MessageReactions";
import { MessageStatus } from "./MessageStatus";
import { MessageText } from "./MessageText";
import { MessageThreadPreview } from "./MessageThreadPreview";
import { MessageUnreadDivider } from "./MessageUnreadDivider";
import type { VirtuosoHandle } from "react-virtuoso";
import { models } from "../../wailsjs/go/models";
import { useTranslation } from "react-i18next";

interface MessageIRCItemProps {
  message: models.Message;
  index: number;
  mainMessages: models.Message[];
  conversationId: string;
  providerInstanceId: string | undefined;
  protocol: string | undefined;
  messageLayout: string;
  isGroupConversation: boolean;
  conversationReadState: Record<string, boolean>;
  firstUnreadMessageId: string | null;
  isTypingInInput: boolean;
  separatorDismissed: boolean;
  revealedDeletedMessages: Set<string>;
  editingMessageId: string | null;
  editingText: string;
  setEditingText: (text: string) => void;
  openActionsMessageId: string | null;
  currentUserId: string | undefined;
  participantNames: Map<string, string>;
  threadsByParent: Record<string, models.Message[]>;
  virtuosoRef: RefObject<VirtuosoHandle | null>;
  handlers: MessageHandlers;
}

export function MessageIRCItem({
  message,
  index,
  mainMessages,
  conversationId,
  providerInstanceId,
  protocol,
  messageLayout,
  isGroupConversation,
  conversationReadState,
  firstUnreadMessageId,
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
}: MessageIRCItemProps) {
  const { t } = useTranslation();
  message = normalizeSlackQuotedReply(message);
  const messageId = getMessageDomId(message);
  const prevMessage = index > 0 ? mainMessages[index - 1] : null;
  const nextMessage = index < mainMessages.length - 1 ? mainMessages[index + 1] : null;
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
  const senderColor = getColorFromString(message.senderId);


  const prevTimestamp = prevMessage ? timeToDate(prevMessage.timestamp) : null;
  const timeDiffMinutes = prevTimestamp
    ? (timestamp.getTime() - prevTimestamp.getTime()) / (1000 * 60)
    : Infinity;

  const shouldShowSenderForDeleted = isDeleted && nextMessage?.senderId === message.senderId && nextMessage?.isFromMe === message.isFromMe;
  const showSender = !prevMessage || prevMessage.senderId !== message.senderId || prevMessage.isFromMe !== message.isFromMe || timeDiffMinutes >= 5 || shouldShowSenderForDeleted;
  const threadMessages = threadsByParent[message.protocolMsgId];
  const hasThread = (threadMessages?.length ?? 0) > 0;
  const hasUnreadInThread = hasThread && threadMessages.some(
    (msg) => !msg.isFromMe && conversationReadState[getMessageDomId(msg)] === false
  );
  const previewUrl = (!isDeleted && message.body) ? extractFirstUrl(message.body) : null;

  const deletedListWrapperClass = cn(
    "w-full flex flex-col gap-1 message",
    isDeleted && "group",
    showDeletedPlaceholder && "cursor-pointer text-muted-foreground/80"
  );
  const deletedListHandlers = isDeleted
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
      <div className="space-y-1">
        {showDateSeparator && <MessageDateSeparator date={messageDate} />}
        {showUnreadDivider && <MessageUnreadDivider />}
        <div data-message-id={messageId} className="scroll-mt-28">
          <CallMessage message={message} layout="irc" isGroup={isGroupConversation} />
        </div>
      </div>
    );
  }

  return (
    <div>
      {showDateSeparator && <MessageDateSeparator date={messageDate} className="mt-2" />}
      {showUnreadDivider && <MessageUnreadDivider />}
      <div
        className={cn(
          "flex items-start scroll-mt-28 group relative",
          isUnread && "border border-primary/30 bg-primary/5 px-2",
          isPending && "opacity-70",
          sendFailed && "border-l-2 border-destructive pl-1"
        )}
        data-message-id={messageId}
        onMouseEnter={() => handlers.setOpenActionsMessageId(messageId)}
        onMouseLeave={() => handlers.setOpenActionsMessageId(null)}
      >
        {/* Left column: avatar + timestamp */}
        <div className="flex flex-col items-center min-w-[60px]">
          {showSender ? (
            <>
              <button onClick={() => handlers.onAvatarClick(message.senderAvatarUrl, displayName)} className="shrink-0">
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
            <span className="text-xs text-muted-foreground" style={{ marginTop: "10px", lineHeight: "inherit" }}>
              {timeString}
            </span>
          )}
        </div>

        {/* Right column */}
        <div className="flex flex-col items-start ml-5 flex-1 min-w-0 relative">
          {showSender && (
            <div className="w-full flex items-center">
              <span className="font-semibold text-sm text-left h-6 flex items-center mt-2.5" style={{ color: senderColor }}>
                {displayName}
              </span>
            </div>
          )}
          <div className="w-full flex items-start gap-2">
            <div className="flex-1 rounded-md transition-colors hover:bg-muted/50 -ml-2 pl-2 pr-2 relative">
              {showDeletedPlaceholder ? (
                <div className="flex flex-col gap-1">
                  <div
                    className="text-xs italic text-muted-foreground/80 flex items-center gap-2 leading-none text-left cursor-pointer"
                    style={{ marginTop: "10px" }}
                    {...deletedListHandlers}
                  >
                    <span>{t("message_deleted")}</span>
                    <span className="text-[10px] uppercase tracking-wide hidden group-hover:inline">{t("click_to_view_deleted")}</span>
                  </div>
                </div>
              ) : (
                <>
                  <div className={deletedListWrapperClass} {...deletedListHandlers}>
                    {isDeleted && (
                      <span className="text-[11px] font-semibold uppercase tracking-wide text-destructive/80">{t("deleted_message_badge")}</span>
                    )}
                    {editingMessageId === messageId ? (
                      <div className="flex flex-col gap-2 w-full">
                        <Input
                          value={editingText}
                          onChange={(e) => setEditingText(e.target.value)}
                          onKeyDown={(e) => {
                            if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); handlers.onSaveEdit(false); }
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
                        {message.isForwarded && (
                          <div className="mb-1 text-xs italic text-muted-foreground">{t("forwarded")}</div>
                        )}
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
                        {message.quotedMessageId && message.quotedBody && (
                          <div
                            className="mb-2 pl-3 pr-2 py-1.5 border-l-[3px] border-purple-600 dark:border-purple-400 bg-muted/40 hover:bg-muted/70 cursor-pointer rounded-r transition-colors text-left"
                            onClick={() => {
                              const quotedId = message.quotedMessageId || "";
                              const quotedIdx = mainMessages.findIndex((m) => m.protocolMsgId === quotedId || getMessageDomId(m) === quotedId);
                              if (quotedIdx >= 0) virtuosoRef.current?.scrollToIndex({ index: quotedIdx, behavior: "smooth", align: "center" });
                            }}
                          >
                            <div className="text-xs font-semibold text-purple-700 dark:text-purple-400 mb-0.5 text-left flex items-center gap-1.5">
                              {message.quotedSenderName || (message.isFromMe ? t("you") : t("contact"))}
                            </div>
                            <div className="text-xs md:text-sm text-foreground/80 line-clamp-2 text-left">
                              <MessageText text={message.quotedBody} providerInstanceId={providerInstanceId} emojiSize={14} isFromMe={message.isFromMe} />
                            </div>
                          </div>
                        )}
                        {!showSender && message.body && (
                          <div className="text-foreground text-left m-0 break-words min-w-0" style={{ marginTop: message.quotedMessageId ? "0" : "10px" }}>
                            <MessageText text={message.body} providerInstanceId={providerInstanceId} emojiSize={16} isFromMe={message.isFromMe} />
                            {message.isEdited && <span className="ml-1 text-xs italic opacity-40">({t("edited")})</span>}
                          </div>
                        )}
                        {showSender && message.body?.trim() && (
                          <div className="text-foreground text-left m-0 break-words min-w-0">
                            <MessageText text={message.body} providerInstanceId={providerInstanceId} emojiSize={16} isFromMe={message.isFromMe} />
                            {message.isEdited && <span className="ml-1 text-xs italic opacity-40">({t("edited")})</span>}
                          </div>
                        )}
                        {previewUrl && <LinkPreviewCard url={previewUrl} isFromMe={message.isFromMe} />}
                        {message.attachments?.trim() && (
                          <MessageAttachments
                            attachments={message.attachments}
                            isFromMe={message.isFromMe}
                            conversationID={conversationId}
                            messageID={String(message.id)}
                            layout={messageLayout as "bubble" | "irc"}
                            showToast={handlers.showToast}
                          />
                        )}
                        {!message.body?.trim() && !message.attachments?.trim() && (
                          <p className="text-sm opacity-70 italic">{t("empty_message")}</p>
                        )}
                      </>
                    )}
                  </div>
                </>
              )}
            </div>
            {message.isFromMe && (
              <div className="self-end mr-4">
                <MessageStatus message={message} isGroup={isGroupConversation} allMessages={mainMessages} layout="irc" />
              </div>
            )}
          </div>
          {isUnread && (
            <span className="text-[10px] font-semibold uppercase tracking-wide text-primary mt-1">{t("unread_indicator")}</span>
          )}
          {!isDeleted && editingMessageId !== messageId && (
            <div className={cn("absolute right-4 top-1 z-10 transition-opacity", openActionsMessageId === messageId ? "opacity-100" : "opacity-0 pointer-events-none")}>
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
        </div>
      </div>
      {(hasThread || (message.reactions && message.reactions.length > 0)) && (
        <div className="flex items-stretch flex-wrap gap-1 mt-1 ml-[80px]">
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
  );
}
