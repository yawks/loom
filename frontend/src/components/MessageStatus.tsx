import { Suspense, useEffect, useMemo, useRef, useState } from "react";
import { Check, CheckCheck } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn, timeToDate } from "@/lib/utils";
import type { models } from "../../wailsjs/go/models";
import { useTranslation } from "react-i18next";

interface MessageStatusProps {
  message: models.Message;
  isGroup: boolean;
  groupParticipants?: models.GroupParticipant[];
  allMessages?: models.Message[];
  layout: "irc" | "bubble";
}

type MessageStatusType = "sent" | "delivered" | "read";

interface ParticipantStatus {
  userId: string;
  userName?: string;
  status: MessageStatusType;
  timestamp?: Date;
}

function getMessageStatus(receipts: models.MessageReceipt[] | undefined, senderId: string): MessageStatusType {
  if (!receipts || receipts.length === 0) {
    return "sent";
  }
  
  // Filter out receipts from the sender (we don't count ourselves)
  const otherReceipts = receipts.filter((r) => r.userId !== senderId);
  
  if (otherReceipts.length === 0) {
    return "sent";
  }
  
  // Check if any receipt is a read receipt (highest priority)
  const hasReadReceipt = otherReceipts.some((r) => r.receiptType === "read");
  if (hasReadReceipt) {
    return "read";
  }
  
  // Check if any receipt is a delivery receipt
  const hasDeliveryReceipt = otherReceipts.some((r) => r.receiptType === "delivery");
  if (hasDeliveryReceipt) {
    return "delivered";
  }
  
  return "sent";
}

function getParticipantStatuses(
  receipts: models.MessageReceipt[] | undefined,
  groupParticipants: models.GroupParticipant[] | undefined,
  allMessages: models.Message[] | undefined,
  senderId: string
): ParticipantStatus[] {
  if (!receipts || receipts.length === 0) {
    return [];
  }
  
  const participantMap = new Map<string, ParticipantStatus>();
  
  // Create a map of userId to userName from messages
  const userIdToName = new Map<string, string>();
  if (allMessages) {
    allMessages.forEach((msg) => {
      if (msg.senderName && msg.senderName.trim() !== "") {
        userIdToName.set(msg.senderId, msg.senderName);
      }
    });
  }
  
  // Process all receipts, excluding the sender
  receipts.forEach((receipt) => {
    // Skip receipts from the sender (we don't count ourselves)
    if (receipt.userId === senderId) {
      return;
    }
    
    const existing = participantMap.get(receipt.userId);
    const receiptTimestamp = timeToDate(receipt.timestamp);
    
    // Read receipt takes precedence over delivery receipt
    // If someone has read, they have also been delivered and sent
    if (receipt.receiptType === "read") {
      if (!existing || existing.status !== "read" || (existing.timestamp && receiptTimestamp > existing.timestamp)) {
        participantMap.set(receipt.userId, {
          userId: receipt.userId,
          userName: userIdToName.get(receipt.userId),
          status: "read",
          timestamp: receiptTimestamp,
        });
      }
    } else if (receipt.receiptType === "delivery") {
      // Only set delivered if not already read (read implies delivered)
      if (!existing || existing.status === "sent") {
        participantMap.set(receipt.userId, {
          userId: receipt.userId,
          userName: userIdToName.get(receipt.userId),
          status: "delivered",
          timestamp: receiptTimestamp,
        });
      }
    }
  });
  
  // Add group participants who haven't sent receipts (status: sent)
  // Exclude the sender from this list
  if (groupParticipants) {
    groupParticipants.forEach((participant) => {
      // Skip the sender
      if (participant.userId === senderId) {
        return;
      }
      
      if (!participantMap.has(participant.userId)) {
        participantMap.set(participant.userId, {
          userId: participant.userId,
          userName: userIdToName.get(participant.userId),
          status: "sent",
        });
      }
    });
  }
  
  return Array.from(participantMap.values());
}

function StatusIcon({ status, className }: { status: MessageStatusType; className?: string }) {
  if (status === "read") {
    return (
      <CheckCheck
        className={cn("h-3 w-3 text-blue-500 dark:text-blue-400", className)}
        aria-hidden="true"
      />
    );
  }
  if (status === "delivered") {
    return (
      <CheckCheck
        className={cn("h-3 w-3 text-muted-foreground", className)}
        aria-hidden="true"
      />
    );
  }
  return (
    <Check
      className={cn("h-3 w-3 text-muted-foreground", className)}
      aria-hidden="true"
    />
  );
}

function StatusTooltipContent({
  message,
  isGroup,
  participantStatuses,
  status,
}: {
  message: models.Message;
  isGroup: boolean;
  participantStatuses: ParticipantStatus[];
  status: MessageStatusType;
}) {
  const { t } = useTranslation();
  
  if (!message.isFromMe) {
    return null;
  }
  
  if (isGroup && participantStatuses.length > 0) {
    // Group conversation: show list of participants
    // Respect hierarchy: read > delivered > sent
    const readParticipants = participantStatuses.filter((p) => p.status === "read");
    const deliveredParticipants = participantStatuses.filter((p) => p.status === "delivered");
    const sentParticipants = participantStatuses.filter((p) => p.status === "sent");
    
    return (
      <div className="max-h-64 overflow-y-auto">
        <div className="space-y-2">
          {readParticipants.length > 0 && (
            <div>
              <div className="text-xs font-semibold text-muted-foreground mb-1">
                {t("status_read")}
              </div>
              <div className="space-y-1">
                {readParticipants.map((p) => (
                  <div key={p.userId} className="text-sm flex items-center gap-2">
                    <StatusIcon status="read" />
                    <span>{p.userName || p.userId}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
          {deliveredParticipants.length > 0 && (
            <div>
              <div className="text-xs font-semibold text-muted-foreground mb-1">
                {t("status_delivered")}
              </div>
              <div className="space-y-1">
                {deliveredParticipants.map((p) => (
                  <div key={p.userId} className="text-sm flex items-center gap-2">
                    <StatusIcon status="delivered" />
                    <span>{p.userName || p.userId}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
          {/* Only show sent participants if there are no delivered or read (sent is the base state) */}
          {sentParticipants.length > 0 && deliveredParticipants.length === 0 && readParticipants.length === 0 && (
            <div>
              <div className="text-xs font-semibold text-muted-foreground mb-1">
                {t("status_sent")}
              </div>
              <div className="space-y-1">
                {sentParticipants.map((p) => (
                  <div key={p.userId} className="text-sm flex items-center gap-2">
                    <StatusIcon status="sent" />
                    <span>{p.userName || p.userId}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>
    );
  }
  
  // Individual conversation: show simple status
  return (
    <div className="text-sm">
      {status === "read" && t("status_read_detail")}
      {status === "delivered" && t("status_delivered_detail")}
      {status === "sent" && t("status_sent_detail")}
    </div>
  );
}

export function MessageStatus({
  message,
  isGroup,
  groupParticipants,
  allMessages,
  layout,
}: MessageStatusProps) {
  const [isAnimating, setIsAnimating] = useState(false);
  const previousStatusRef = useRef<MessageStatusType | null>(null);
  
  const status = useMemo(() => getMessageStatus(message.receipts, message.senderId), [message.receipts, message.senderId]);
  const participantStatuses = useMemo(
    () => getParticipantStatuses(message.receipts, groupParticipants, allMessages, message.senderId),
    [message.receipts, groupParticipants, allMessages, message.senderId]
  );
  
  // Detect status change and trigger animation
  useEffect(() => {
    if (previousStatusRef.current !== null && previousStatusRef.current !== status) {
      setIsAnimating(true);
      const timer = setTimeout(() => setIsAnimating(false), 500);
      return () => clearTimeout(timer);
    }
    previousStatusRef.current = status;
  }, [status]);
  
  // Only show status for messages sent by the user
  if (!message.isFromMe) {
    return null;
  }
  
  const { t } = useTranslation();
  
  const statusLabel = useMemo(() => {
    if (isGroup && participantStatuses.length > 0) {
      const readCount = participantStatuses.filter((p) => p.status === "read").length;
      const deliveredCount = participantStatuses.filter((p) => p.status === "delivered").length;
      const sentCount = participantStatuses.filter((p) => p.status === "sent").length;
      
      // Build label respecting the hierarchy: read > delivered > sent
      // Don't show "sent" count if there are delivered or read (they imply sent)
      // Don't show "delivered" count if there are read (they imply delivered)
      const parts: string[] = [];
      
      if (readCount > 0) {
        parts.push(`${readCount} ${t("status_read")}`);
      }
      if (deliveredCount > 0) {
        parts.push(`${deliveredCount} ${t("status_delivered")}`);
      }
      // Only show sent if there are no delivered or read (sent is the base state)
      if (sentCount > 0 && deliveredCount === 0 && readCount === 0) {
        parts.push(`${sentCount} ${t("status_sent")}`);
      }
      
      return parts.join(", ") || t("status_sent_detail");
    }
    if (status === "read") return t("status_read_detail");
    if (status === "delivered") return t("status_delivered_detail");
    return t("status_sent_detail");
  }, [isGroup, participantStatuses, status, t]);
  
  const iconElement = (
    <div
      className={cn(
        "flex items-center justify-center",
        layout === "irc" ? "ml-2" : "mt-1",
        isAnimating && "animate-pulse"
      )}
      aria-label={statusLabel}
    >
      <StatusIcon status={status} />
    </div>
  );
  
  if (isGroup && participantStatuses.length > 0) {
    return (
      <Popover>
        <PopoverTrigger asChild>
          <button
            type="button"
            className="focus:outline-none focus:ring-2 focus:ring-primary focus:ring-offset-2 rounded"
            aria-label={statusLabel}
            title={statusLabel}
          >
            {iconElement}
          </button>
        </PopoverTrigger>
        <PopoverContent className="w-64" align="end">
          <Suspense fallback={<div className="text-sm">{statusLabel}</div>}>
            <StatusTooltipContent
              message={message}
              isGroup={isGroup}
              participantStatuses={participantStatuses}
              status={status}
            />
          </Suspense>
        </PopoverContent>
      </Popover>
    );
  }
  
  // For individual conversations, use a simple tooltip with title attribute
  return (
    <div
      className={cn(
        "flex items-center justify-center",
        layout === "irc" ? "ml-2" : "mt-1",
        isAnimating && "animate-pulse"
      )}
      title={statusLabel}
      aria-label={statusLabel}
    >
      <StatusIcon status={status} />
    </div>
  );
}

