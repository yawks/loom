import { useMemo } from "react";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { MessageSquare } from "lucide-react";
import { models } from "../../wailsjs/go/models";
import { getSenderDisplayName } from "@/lib/messageUtils";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

const MAX_AVATARS = 5;

interface MessageThreadPreviewProps {
  readonly threadMessages: models.Message[];
  readonly className?: string;
  readonly hasUnread?: boolean;
  readonly onThreadClick: () => void;
  readonly onAvatarClick: (url: string | undefined, name?: string) => void;
}

export function MessageThreadPreview({
  threadMessages,
  className,
  hasUnread = false,
  onThreadClick,
  onAvatarClick,
}: MessageThreadPreviewProps) {
  const { t } = useTranslation();

  const uniqueParticipants = useMemo(() => {
    const seen = new Set<string>();
    return threadMessages.filter((msg) => {
      const key = msg.senderId || msg.senderName || "";
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    });
  }, [threadMessages]);

  const visibleParticipants = uniqueParticipants.slice(0, MAX_AVATARS);
  const overflow = uniqueParticipants.length - MAX_AVATARS;
  const replyCount = threadMessages.length;

  return (
    <button
      onClick={onThreadClick}
      style={{ fontFamily: '"Inter Variable", Arial, Helvetica, sans-serif', fontSize: "0.55rem" }}
      className={cn(
        "message-thread-preview flex items-center gap-1.5 p-1 leading-none rounded-md transition-colors cursor-pointer text-left max-w-[80%]",
        hasUnread
          ? "bg-primary/10 hover:bg-primary/15 ring-1 ring-primary/30"
          : "bg-muted/50 hover:bg-muted",
        className
      )}
    >
      <div className="relative shrink-0">
        <MessageSquare
          className={cn(
            "message-thread-preview__icon h-3 w-3",
            hasUnread ? "text-primary" : "text-muted-foreground/60"
          )}
        />
        {hasUnread && (
          <span className="message-thread-preview__unread-dot absolute -top-0.5 -right-0.5 h-1.5 w-1.5 rounded-full bg-primary" />
        )}
      </div>
      <div className="message-thread-preview__avatars flex items-center relative">
        {visibleParticipants.map((participant, i) => {
          const name = getSenderDisplayName(participant.senderName, participant.senderId, participant.isFromMe, t);
          return (
            <button
              key={participant.senderId || i}
              type="button"
              className="shrink-0 cursor-pointer bg-transparent border-0 p-0"
              style={{ marginLeft: i === 0 ? 0 : "-4px", zIndex: visibleParticipants.length - i }}
              onClick={(e) => {
                e.stopPropagation();
                onAvatarClick(participant.senderAvatarUrl, name);
              }}
            >
              <Avatar className="h-3.5 w-3.5 ring-1 ring-background hover:opacity-80 transition-opacity">
                <AvatarImage src={participant.senderAvatarUrl} />
                <AvatarFallback className="text-[0.4rem]">{name.substring(0, 2).toUpperCase()}</AvatarFallback>
              </Avatar>
            </button>
          );
        })}
        {overflow > 0 && (
          <span className="message-thread-preview__overflow text-[0.5rem] text-muted-foreground ml-1">
            +{overflow}
          </span>
        )}
      </div>
      <span
        className={cn(
          "message-thread-preview__count text-[0.55rem]",
          hasUnread ? "text-primary font-semibold" : "text-muted-foreground"
        )}
      >
        {replyCount === 1 ? `1 ${t("single_reply")}` : t("multiple_replies", { count: replyCount })}
      </span>
    </button>
  );
}
