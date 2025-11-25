import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";

import { Button } from "@/components/ui/button";
import { GetThreads } from "../../wailsjs/go/main/App";
import { X } from "lucide-react";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";

// Generate a deterministic color from a string (username)
function getColorFromString(str: string): string {
  let hash = 0;
  for (let i = 0; i < str.length; i++) {
    hash = str.charCodeAt(i) + ((hash << 5) - hash);
  }
  
  // Generate a hue between 0 and 360
  const hue = Math.abs(hash) % 360;
  
  // Use a moderate saturation and lightness for good contrast
  return `hsl(${hue}, 70%, 50%)`;
}

// Get display name for a message sender
function getSenderDisplayName(senderId: string, isFromMe: boolean): string {
  if (isFromMe) return "You";
  // Extract a readable name from senderId (e.g., "user-alice" -> "Alice")
  return senderId
    .replace(/^user-/, "")
    .replace(/^whatsapp-/, "")
    .replace(/^slack-/, "")
    .split("-")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

const fetchThreads = async (parentMessageID: string) => {
  return GetThreads(parentMessageID);
};

export function ThreadView() {
  const selectedThreadId = useAppStore((state) => state.selectedThreadId);
  const setSelectedThreadId = useAppStore((state) => state.setSelectedThreadId);
  const setShowThreads = useAppStore((state) => state.setShowThreads);
  const messageLayout = useAppStore((state) => state.messageLayout);

  const handleClose = () => {
    setSelectedThreadId(null);
    setShowThreads(false);
  };

  // Use useQuery instead of useSuspenseQuery to handle conditional rendering
  const { data: threadMessages, isLoading } = useQuery<models.Message[], Error>({
    queryKey: ["threads", selectedThreadId || ""],
    queryFn: () => {
      if (!selectedThreadId) {
        return Promise.resolve([]);
      }
      return fetchThreads(selectedThreadId);
    },
    enabled: !!selectedThreadId,
  });

  // Sort thread messages by timestamp
  const sortedThreadMessages = useMemo(() => {
    if (!threadMessages || threadMessages.length === 0) return [];
    return [...threadMessages].sort(
      (a, b) =>
        new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime()
    );
  }, [threadMessages]);

  if (!selectedThreadId) {
    return (
      <div className="h-full flex items-center justify-center text-muted-foreground">
        Select a thread to view
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className="h-full flex items-center justify-center text-muted-foreground">
        Loading thread...
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      <div className="p-4 border-b flex justify-between items-center shrink-0">
        <h3 className="text-md font-semibold">Thread</h3>
        <Button
          variant="ghost"
          size="icon"
          onClick={handleClose}
        >
          <X className="h-4 w-4" />
        </Button>
      </div>
      <div className="flex-1 overflow-y-auto p-4 min-h-0">
        {sortedThreadMessages.length === 0 ? (
          <div className="text-center text-muted-foreground py-8">
            No messages in this thread
          </div>
        ) : messageLayout === "bubble" ? (
          <div className="space-y-4">
            {sortedThreadMessages.map((message) => (
              <div
                key={message.protocolMsgId || `thread-${message.id}`}
                className={`flex items-start gap-3 ${
                  message.isFromMe ? "justify-end" : ""
                }`}
              >
                {!message.isFromMe && (
                  <Avatar className="h-6 w-6">
                    <AvatarImage src="" />
                    <AvatarFallback className="text-xs">
                      {message.senderId.substring(0, 2).toUpperCase()}
                    </AvatarFallback>
                  </Avatar>
                )}
                <div
                  className={`rounded-lg p-2 text-sm ${
                    message.isFromMe
                      ? "bg-blue-600 text-white"
                      : "bg-muted text-foreground"
                  }`}
                >
                  <p>{message.body}</p>
                  <p className={`text-xs mt-1 ${
                    message.isFromMe ? "text-blue-100" : "text-muted-foreground"
                  }`}>
                    {new Date(message.timestamp).toLocaleTimeString()}
                  </p>
                </div>
                {message.isFromMe && (
                  <Avatar className="h-6 w-6">
                    <AvatarImage src="" />
                    <AvatarFallback className="text-xs">ME</AvatarFallback>
                  </Avatar>
                )}
              </div>
            ))}
          </div>
        ) : (
          <div className="space-y-1 text-sm">
            {sortedThreadMessages.map((message, index) => {
              const prevMessage = index > 0 ? sortedThreadMessages[index - 1] : null;
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
              const displayName = getSenderDisplayName(message.senderId, message.isFromMe);
              const senderColor = getColorFromString(message.senderId);
              const timeString = `${timestamp.getHours().toString().padStart(2, "0")}:${timestamp.getMinutes().toString().padStart(2, "0")}`;

              return (
                <div key={message.protocolMsgId || `thread-${message.id}`} className="space-y-1">
                  <div className="flex items-start py-1">
                    {/* Left column */}
                    <div className="flex flex-col items-center min-w-[60px]">
                      {showSender ? (
                        <>
                          <Avatar className="h-6 w-6 mt-2.5">
                            <AvatarImage src="" />
                            <AvatarFallback className="text-xs">
                              {message.isFromMe ? "ME" : message.senderId.substring(0, 2).toUpperCase()}
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
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}

