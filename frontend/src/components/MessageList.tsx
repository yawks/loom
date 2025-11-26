import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { useEffect, useMemo, useRef } from "react";

import { ChatInput } from "./ChatInput";
import { GetMessagesForConversation } from "../../wailsjs/go/main/App";
import { MessageAttachments } from "./MessageAttachments";
import { MessageHeader } from "./MessageHeader";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useSuspenseQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

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
    // Format phone number: add spaces every 2 digits (French format)
    // Example: 33631207926 -> +33 6 31 20 79 26
    if (phoneNumber.startsWith("33") && phoneNumber.length >= 10) {
      // French number: +33 followed by 9 digits (without leading 0)
      const countryCode = phoneNumber.substring(0, 2);
      const rest = phoneNumber.substring(2);
      // Format as +33 X XX XX XX XX
      const formatted = `+${countryCode} ${rest.substring(0, 1)} ${rest.substring(1, 3)} ${rest.substring(3, 5)} ${rest.substring(5, 7)} ${rest.substring(7)}`;
      return formatted;
    } else {
      // Other format: just add spaces every 2 digits
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

export function MessageList({
  selectedConversation,
}: {
  selectedConversation: models.MetaContact;
}) {
  const { t } = useTranslation();
  const { data: messages } = useSuspenseQuery<models.Message[], Error>({
    queryKey: ["messages", selectedConversation.id],
    queryFn: () =>
      fetchMessages(selectedConversation.linkedAccounts[0].userId),
  });
  const showThreads = useAppStore((state) => state.showThreads);
  const setShowThreads = useAppStore((state) => state.setShowThreads);
  const setSelectedThreadId = useAppStore((state) => state.setSelectedThreadId);
  const messageLayout = useAppStore((state) => state.messageLayout);

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

  const scrollContainerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const node = scrollContainerRef.current;
    if (!node) {
      return;
    }
    requestAnimationFrame(() => {
      node.scrollTop = node.scrollHeight;
    });
  }, [messages]);

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

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <MessageHeader
        displayName={selectedConversation.displayName}
        linkedAccounts={selectedConversation.linkedAccounts}
        onToggleThreads={handleToggleThreads}
        onToggleDetails={handleToggleDetails}
      />
      <div className="flex-1 overflow-y-auto p-4 min-h-0 scroll-area" ref={scrollContainerRef}>
        {messageLayout === "bubble" ? (
          <div className="space-y-4">
            {mainMessages.map((message) => {
              const lastThreadMsg = getLastThreadMessage(message.protocolMsgId);
              const threadCount = getThreadCount(message.protocolMsgId);
              const hasThread = threadCount > 0;
              const displayName = getSenderDisplayName(
                message.senderName,
                message.senderId,
                message.isFromMe,
                t
              );

              return (
                <div key={message.protocolMsgId || `msg-${message.id}`} className="space-y-2">
                  <div
                    className={`flex items-start gap-3 ${
                      message.isFromMe ? "justify-end" : ""
                    }`}
                  >
                    {!message.isFromMe && (
                      <button
                        onClick={() => handleAvatarClick(message.senderAvatarUrl, displayName)}
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
                      className={`rounded-lg p-3 ${
                        message.isFromMe
                          ? "bg-blue-600 text-white"
                          : "bg-muted text-foreground"
                      }`}
                    >
                      {message.body && message.body.trim() !== "" && <p>{message.body}</p>}
                      {message.attachments && message.attachments.trim() !== "" && (
                        <MessageAttachments
                          attachments={message.attachments}
                          isFromMe={message.isFromMe}
                        />
                      )}
                      {(!message.body || message.body.trim() === "") && 
                       (!message.attachments || message.attachments.trim() === "") && (
                        <p className="text-sm opacity-70 italic">{t("empty_message")}</p>
                      )}
                      <p className={`text-xs mt-1 ${
                        message.isFromMe
                          ? "text-blue-100"
                          : "text-muted-foreground"
                      }`}>
                        {new Date(message.timestamp).toLocaleTimeString()}
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
                      onClick={() => handleThreadClick(message.protocolMsgId)}
                      className={`ml-15 flex items-center gap-2 p-2 rounded-lg bg-muted/50 hover:bg-muted transition-colors cursor-pointer text-left ${
                        message.isFromMe ? "ml-auto max-w-[80%]" : "mr-auto max-w-[80%]"
                      }`}
                    >
                      <button
                        onClick={() => handleAvatarClick(
                          lastThreadMsg.senderAvatarUrl,
                          getSenderDisplayName(
                            lastThreadMsg.senderName,
                            lastThreadMsg.senderId,
                            lastThreadMsg.isFromMe,
                            t
                          )
                        )}
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
                            ? lastThreadMsg.body.substring(0, 50) + "..."
                            : lastThreadMsg.body}
                        </p>
                        <div className="flex items-center gap-2 mt-1">
                          <p className="text-xs text-muted-foreground/70">
                            {new Date(lastThreadMsg.timestamp).toLocaleTimeString()}
                          </p>
                          {threadCount > 1 && (
                            <span className="text-xs text-muted-foreground/70">
                              · {threadCount} {threadCount === 1 ? "reply" : "replies"}
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
        ) : (
          <div className="space-y-1 text-sm">
            {mainMessages.map((message, index) => {
              const lastThreadMsg = getLastThreadMessage(message.protocolMsgId);
              const threadCount = getThreadCount(message.protocolMsgId);
              const hasThread = threadCount > 0;
              const prevMessage = index > 0 ? mainMessages[index - 1] : null;
              const timestamp = new Date(message.timestamp);
              const prevTimestamp = prevMessage ? new Date(prevMessage.timestamp) : null;
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
              const timeString = `${timestamp.getHours().toString().padStart(2, "0")}:${timestamp.getMinutes().toString().padStart(2, "0")}`;

              return (
                <div key={message.protocolMsgId || `msg-${message.id}`} className="space-y-1">
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
                                {message.isFromMe
                                  ? t("me")
                                  : displayName.substring(0, 2).toUpperCase()}
                              </AvatarFallback>
                            </Avatar>
                          </button>
                          <span className="text-xs text-muted-foreground mt-1">{timeString}</span>
                        </>
                      ) : (
                        <span className="text-xs text-muted-foreground leading-none" style={{ marginTop: '10px' }}>{timeString}</span>
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
                            <p className="text-foreground text-left m-0">{message.body}</p>
                          )}
                          {message.attachments && message.attachments.trim() !== "" && (
                            <MessageAttachments
                              attachments={message.attachments}
                              isFromMe={message.isFromMe}
                            />
                          )}
                        </>
                      ) : (
                        <>
                          {message.body && <p className="text-foreground text-left m-0 leading-none" style={{ marginTop: '10px' }}>{message.body}</p>}
                          <MessageAttachments
                            attachments={message.attachments || ""}
                            isFromMe={message.isFromMe}
                          />
                        </>
                      )}
                    </div>
                  </div>
                  {hasThread && lastThreadMsg && (
                    <button
                      onClick={() => handleThreadClick(message.protocolMsgId)}
                      className="ml-[80px] flex items-center gap-2 p-2 rounded-lg bg-muted/50 hover:bg-muted transition-colors cursor-pointer text-left max-w-[80%]"
                    >
                      <button
                        onClick={() => handleAvatarClick(
                          lastThreadMsg.senderAvatarUrl,
                          getSenderDisplayName(
                            lastThreadMsg.senderName,
                            lastThreadMsg.senderId,
                            lastThreadMsg.isFromMe,
                            t
                          )
                        )}
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
                            ? lastThreadMsg.body.substring(0, 50) + "..."
                            : lastThreadMsg.body}
                        </p>
                        <div className="flex items-center gap-2 mt-1">
                          <p className="text-xs text-muted-foreground/70">
                            {new Date(lastThreadMsg.timestamp).toLocaleTimeString()}
                          </p>
                          {threadCount > 1 && (
                            <span className="text-xs text-muted-foreground/70">
                              · {threadCount} {threadCount === 1 ? "reply" : "replies"}
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
        <ChatInput />
      </div>
    </div>
  );
}
