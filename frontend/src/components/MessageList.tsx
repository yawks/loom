import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";

import { Button } from "@/components/ui/button";
import { ChatInput } from "./ChatInput";
import { GetMessagesForConversation } from "../../wailsjs/go/main/App";
import { MessageSquare } from "lucide-react";
import { ProtocolSwitcher } from "./ProtocolSwitcher";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useEffect, useMemo, useRef } from "react";
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
  isFromMe: boolean
): string {
  if (isFromMe) return "You";
  if (senderName && senderName.trim().length > 0) {
    return senderName;
  }
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

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <div className="p-4 border-b flex justify-between items-center shrink-0">
        <h2 className="text-lg font-semibold">
          {selectedConversation.displayName}
        </h2>
        <div className="flex items-center gap-2">
          <Button
            variant="ghost"
            size="icon"
            onClick={handleToggleThreads}
            title={t("threads")}
          >
            <MessageSquare className="h-4 w-4" />
          </Button>
          <ProtocolSwitcher
            linkedAccounts={selectedConversation.linkedAccounts}
          />
        </div>
      </div>
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
                message.isFromMe
              );

              return (
                <div key={message.protocolMsgId || `msg-${message.id}`} className="space-y-2">
                  <div
                    className={`flex items-start gap-3 ${
                      message.isFromMe ? "justify-end" : ""
                    }`}
                  >
                    {!message.isFromMe && (
                      <Avatar>
                        <AvatarImage src="" />
                        <AvatarFallback>
                          {displayName.substring(0, 2).toUpperCase()}
                        </AvatarFallback>
                      </Avatar>
                    )}
                    <div
                      className={`rounded-lg p-3 ${
                        message.isFromMe
                          ? "bg-blue-600 text-white"
                          : "bg-muted text-foreground"
                      }`}
                    >
                      <p>{message.body}</p>
                      <p className={`text-xs mt-1 ${
                        message.isFromMe
                          ? "text-blue-100"
                          : "text-muted-foreground"
                      }`}>
                        {new Date(message.timestamp).toLocaleTimeString()}
                      </p>
                    </div>
                    {message.isFromMe && (
                      <Avatar>
                        <AvatarImage src="" />
                        <AvatarFallback>ME</AvatarFallback>
                      </Avatar>
                    )}
                  </div>
                  {hasThread && lastThreadMsg && (
                    <button
                      onClick={() => handleThreadClick(message.protocolMsgId)}
                      className={`ml-15 flex items-center gap-2 p-2 rounded-lg bg-muted/50 hover:bg-muted transition-colors cursor-pointer text-left ${
                        message.isFromMe ? "ml-auto max-w-[80%]" : "mr-auto max-w-[80%]"
                      }`}
                    >
                      <Avatar className="h-5 w-5 shrink-0">
                        <AvatarImage src="" />
                        <AvatarFallback className="text-xs">
                        {getSenderDisplayName(
                          lastThreadMsg.senderName,
                          lastThreadMsg.senderId,
                          lastThreadMsg.isFromMe
                        )
                          .substring(0, 2)
                          .toUpperCase()}
                        </AvatarFallback>
                      </Avatar>
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
                message.isFromMe
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
                          <Avatar className="h-6 w-6 mt-2.5">
                            <AvatarImage src="" />
                            <AvatarFallback className="text-xs">
                              {message.isFromMe
                                ? "ME"
                                : displayName.substring(0, 2).toUpperCase()}
                            </AvatarFallback>
                          </Avatar>
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
                          <p className="text-foreground text-left m-0">{message.body}</p>
                        </>
                      ) : (
                        <p className="text-foreground text-left m-0 leading-none" style={{ marginTop: '10px' }}>{message.body}</p>
                      )}
                    </div>
                  </div>
                  {hasThread && lastThreadMsg && (
                    <button
                      onClick={() => handleThreadClick(message.protocolMsgId)}
                      className="ml-[80px] flex items-center gap-2 p-2 rounded-lg bg-muted/50 hover:bg-muted transition-colors cursor-pointer text-left max-w-[80%]"
                    >
                      <Avatar className="h-5 w-5 shrink-0">
                        <AvatarImage src="" />
                        <AvatarFallback className="text-xs">
                          {getSenderDisplayName(
                            lastThreadMsg.senderName,
                            lastThreadMsg.senderId,
                            lastThreadMsg.isFromMe
                          )
                            .substring(0, 2)
                            .toUpperCase()}
                        </AvatarFallback>
                      </Avatar>
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
