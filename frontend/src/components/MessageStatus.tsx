import { Suspense, useEffect, useMemo, useRef, useState } from "react";
import { Check, CheckCheck } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn, timeToDate } from "@/lib/utils";
import { getUserDisplayName } from "@/lib/userDisplayNames";
import type { models } from "../../wailsjs/go/models";
import { useTranslation } from "react-i18next";

interface MessageStatusProps {
  message: models.Message;
  isGroup: boolean;
  groupParticipants?: models.GroupParticipant[];
  allMessages?: models.Message[];
  participantNames?: Map<string, string>;
  layout: "irc" | "bubble";
}

type MessageStatusType = "sent" | "delivered" | "read";

interface ParticipantStatus {
  userId: string;
  userName?: string;
  status: MessageStatusType;
  timestamp?: Date;
}

function getMessageStatus(
  message: models.Message,
  isGroup: boolean,
  allMessages?: models.Message[],
  participantStatuses?: ParticipantStatus[]
): MessageStatusType {
  if (isGroup) {
    if (!participantStatuses || participantStatuses.length === 0) {
      return "sent";
    }
    // In a group, check if ALL other participants have read the message
    const allRead = participantStatuses.length > 0 && participantStatuses.every((p) => p.status === "read");
    if (allRead) {
      return "read";
    }
    // Delivered if at least one person received/read it
    const anyDeliveredOrRead = participantStatuses.some(
      (p) => p.status === "delivered" || p.status === "read"
    );
    if (anyDeliveredOrRead) {
      return "delivered";
    }
    return "sent";
  }

  // 1-on-1 (DM) conversation:
  const receipts = message.receipts;
  const senderId = message.senderId;
  const reactions = message.reactions;

  // 1. Direct reactions from someone else imply the message was read
  const otherReactions = reactions?.filter((r) => r.userId !== senderId);
  if (otherReactions && otherReactions.length > 0) {
    return "read";
  }

  // 2. Direct read receipt (highest explicit priority)
  const otherReceipts = receipts?.filter((r) => r.userId !== senderId) || [];
  const hasReadReceipt = otherReceipts.some((r) => r.receiptType === "read");
  if (hasReadReceipt) {
    return "read";
  }

  // 3. In a 1-on-1 chat (DM), check if there are subsequent messages from the other person
  if (allMessages && allMessages.length > 0) {
    const currentMsgIndex = allMessages.findIndex(
      (m) => m.id === message.id || (m.protocolMsgId && m.protocolMsgId === message.protocolMsgId)
    );
    if (currentMsgIndex !== -1) {
      const subsequentMessages = allMessages.slice(currentMsgIndex + 1);
      const hasSubsequentIncomingMessage = subsequentMessages.some((m) => !m.isFromMe);
      if (hasSubsequentIncomingMessage) {
        return "read";
      }
    }
  }

  // 4. Check if any delivery receipt exists
  const hasDeliveryReceipt = otherReceipts.some((r) => r.receiptType === "delivery");
  if (hasDeliveryReceipt) {
    return "delivered";
  }

  return "sent";
}

function getParticipantStatuses(
  message: models.Message,
  groupParticipants: models.GroupParticipant[] | undefined,
  allMessages: models.Message[] | undefined,
  participantNames?: Map<string, string>
): ParticipantStatus[] {
  const receipts = message.receipts;
  const senderId = message.senderId;
  const reactions = message.reactions;

  if (
    (!receipts || receipts.length === 0) &&
    (!reactions || reactions.length === 0) &&
    (!groupParticipants || groupParticipants.length === 0) &&
    (!allMessages || allMessages.length === 0)
  ) {
    return [];
  }

  const participantMap = new Map<string, ParticipantStatus>();

  const resolveName = (id: string, fallback?: string) => {
    const resolved = getUserDisplayName(id, { participantNames, allMessages });
    if (resolved && resolved !== id) {
      return resolved;
    }
    return fallback || resolved;
  };

  const isSameSender = (id1?: string, id2?: string) => {
    if (!id1 || !id2) return false;
    if (id1 === id2) return true;
    const raw1 = id1.includes("::") ? id1.split("::")[1] : id1;
    const raw2 = id2.includes("::") ? id2.split("::")[1] : id2;
    const clean1 = raw1.replace(/:\d+@/, "@");
    const clean2 = raw2.replace(/:\d+@/, "@");
    if (clean1 === clean2) return true;
    return false;
  };

  const findExistingKey = (id: string): string | undefined => {
    if (participantMap.has(id)) return id;
    for (const key of participantMap.keys()) {
      if (isSameSender(key, id)) return key;
    }
    const name = resolveName(id);
    if (name && name !== id) {
      for (const [key, val] of participantMap.entries()) {
        if (val.userName === name || resolveName(key) === name) {
          return key;
        }
      }
    }
    return undefined;
  };

  const setStatus = (id: string, status: MessageStatusType, timestamp?: Date, fallbackName?: string) => {
    if (isSameSender(id, senderId)) {
      return;
    }
    const existingKey = findExistingKey(id);
    const resolved = resolveName(id, fallbackName);

    if (existingKey) {
      const existing = participantMap.get(existingKey)!;
      if (status === "read") {
        existing.status = "read";
        if (timestamp && (!existing.timestamp || timestamp > existing.timestamp)) {
          existing.timestamp = timestamp;
        }
      } else if (status === "delivered" && existing.status === "sent") {
        existing.status = "delivered";
        if (timestamp && (!existing.timestamp || timestamp > existing.timestamp)) {
          existing.timestamp = timestamp;
        }
      }
      if (resolved && resolved !== id && (!existing.userName || existing.userName === existing.userId)) {
        existing.userName = resolved;
      }
      return;
    }

    participantMap.set(id, {
      userId: id,
      userName: resolved,
      status,
      timestamp,
    });
  };

  // 1. Process groupParticipants first as base "sent" status
  if (groupParticipants) {
    groupParticipants.forEach((p) => {
      setStatus(p.userId, "sent");
    });
  }

  // 2. Process delivery and read receipts
  if (receipts) {
    receipts.forEach((receipt) => {
      const receiptTimestamp = timeToDate(receipt.timestamp);
      if (receipt.receiptType === "read") {
        setStatus(receipt.userId, "read", receiptTimestamp);
      } else if (receipt.receiptType === "delivery") {
        setStatus(receipt.userId, "delivered", receiptTimestamp);
      }
    });
  }

  // 3. Process reactions (imply read)
  if (reactions) {
    reactions.forEach((reaction) => {
      const reactionTimestamp = timeToDate(reaction.createdAt || reaction.updatedAt);
      setStatus(reaction.userId, "read", reactionTimestamp);
    });
  }

  // 4. Process subsequent messages from participants (imply read)
  if (allMessages && allMessages.length > 0) {
    const currentMsgIndex = allMessages.findIndex(
      (m) => m.id === message.id || (m.protocolMsgId && m.protocolMsgId === message.protocolMsgId)
    );
    if (currentMsgIndex !== -1) {
      const subsequentMessages = allMessages.slice(currentMsgIndex + 1);
      subsequentMessages.forEach((subMsg) => {
        if (!subMsg.isFromMe && subMsg.senderId) {
          const msgTimestamp = timeToDate(subMsg.timestamp);
          setStatus(subMsg.senderId, "read", msgTimestamp, subMsg.senderName);
        }
      });
    }
  }

  return Array.from(participantMap.values());
}

function StatusIcon({ status, className }: { status: MessageStatusType; className?: string }) {
  if (status === "read") {
    return (
      <CheckCheck
        className={cn("h-3 w-3 text-green-500 dark:text-green-400", className)}
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
  participantNames,
  layout,
}: MessageStatusProps) {
  const [isAnimating, setIsAnimating] = useState(false);
  const previousStatusRef = useRef<MessageStatusType | null>(null);

  const participantStatuses = useMemo(
    () => getParticipantStatuses(message, groupParticipants, allMessages, participantNames),
    [message, groupParticipants, allMessages, participantNames]
  );

  const status = useMemo(
    () => getMessageStatus(message, isGroup, allMessages, participantStatuses),
    [message, isGroup, allMessages, participantStatuses]
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
