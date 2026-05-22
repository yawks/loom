import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";

import { MessageText } from "./MessageText";
import { models } from "../../wailsjs/go/models";
import { getSenderDisplayName } from "@/lib/messageUtils";
import { timeToDate } from "@/lib/utils";
import { useTranslation } from "react-i18next";

interface MessageThreadPreviewProps {
  lastThreadMsg: models.Message;
  threadCount: number;
  providerInstanceId: string | undefined;
  className?: string;
  onThreadClick: () => void;
  onAvatarClick: (url: string | undefined, name?: string) => void;
}

export function MessageThreadPreview({
  lastThreadMsg,
  threadCount,
  providerInstanceId,
  className,
  onThreadClick,
  onAvatarClick,
}: MessageThreadPreviewProps) {
  const { t } = useTranslation();
  const senderName = getSenderDisplayName(
    lastThreadMsg.senderName,
    lastThreadMsg.senderId,
    lastThreadMsg.isFromMe,
    t
  );

  return (
    <button
      onClick={onThreadClick}
      className={`flex items-center gap-2 p-2 rounded-lg bg-muted/50 hover:bg-muted transition-colors cursor-pointer text-left max-w-[80%] ${className ?? ""}`}
    >
      <div
        role="button"
        tabIndex={0}
        className="shrink-0 cursor-pointer"
        onClick={(e) => { e.stopPropagation(); onAvatarClick(lastThreadMsg.senderAvatarUrl, senderName); }}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.stopPropagation();
            e.preventDefault();
            onAvatarClick(lastThreadMsg.senderAvatarUrl, senderName);
          }
        }}
      >
        <Avatar className="h-5 w-5 shrink-0 cursor-pointer hover:opacity-80 transition-opacity">
          <AvatarImage src={lastThreadMsg.senderAvatarUrl} />
          <AvatarFallback className="text-xs">{senderName.substring(0, 2).toUpperCase()}</AvatarFallback>
        </Avatar>
      </div>
      <div className="flex-1 min-w-0">
        <div className="text-sm text-muted-foreground truncate">
          <MessageText
            text={lastThreadMsg.body}
            providerInstanceId={providerInstanceId}
            emojiSize={14}
            preview={true}
            isFromMe={lastThreadMsg.isFromMe}
          />
        </div>
        <div className="flex items-center gap-2 mt-1">
          <p className="text-xs text-muted-foreground/70">
            {timeToDate(lastThreadMsg.timestamp).toLocaleTimeString()}
          </p>
          {threadCount > 0 && (
            <span className="text-xs text-muted-foreground/70">
              · {threadCount === 1 ? t("single_reply") : t("multiple_replies", { count: threadCount })}
            </span>
          )}
        </div>
      </div>
    </button>
  );
}
