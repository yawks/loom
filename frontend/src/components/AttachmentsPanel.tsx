import { FileText, Loader2, X } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import { timeToDate } from "@/lib/utils";
import type { models } from "../../wailsjs/go/models";
import { MessageAttachments } from "./MessageAttachments";

type AttachmentEntry = {
  key: string;
  serializedAttachment: string;
  message: models.Message;
  timestamp: Date;
};

export function AttachmentsPanel({ messages, loading, onClose }: {
  messages: models.Message[];
  loading: boolean;
  onClose: () => void;
}) {
  const { t, i18n } = useTranslation();
  const entries = useMemo(() => messages.flatMap((message) => {
    try {
      const attachments = JSON.parse(message.attachments) as unknown;
      if (!Array.isArray(attachments)) return [];
      return attachments.map((attachment, index): AttachmentEntry => ({
        key: `${message.protocolMsgId || message.id}-${index}`,
        serializedAttachment: JSON.stringify([attachment]),
        message,
        timestamp: timeToDate(message.timestamp),
      }));
    } catch {
      return [];
    }
  }).sort((left, right) => right.timestamp.getTime() - left.timestamp.getTime()), [messages]);

  const dateFormatter = useMemo(() => new Intl.DateTimeFormat(i18n.language, {
    dateStyle: "long",
  }), [i18n.language]);
  const timeFormatter = useMemo(() => new Intl.DateTimeFormat(i18n.language, {
    timeStyle: "short",
  }), [i18n.language]);

  let previousDate = "";

  return (
    <aside className="absolute bottom-0 right-0 top-[73px] z-40 flex w-[min(420px,90%)] flex-col border-l bg-background shadow-xl">
      <div className="flex h-[73px] shrink-0 items-center justify-between border-b px-4">
        <div>
          <h3 className="font-semibold">{t("attachments")}</h3>
          <p className="text-xs text-muted-foreground">{t("attachments_help")}</p>
        </div>
        <Button variant="ghost" size="icon" onClick={onClose} title={t("close")} aria-label={t("close")}>
          <X className="h-4 w-4" />
        </Button>
      </div>
      <div className="flex-1 overflow-y-auto px-4 pb-4">
        {loading ? (
          <div className="flex h-32 items-center justify-center"><Loader2 className="h-5 w-5 animate-spin" /></div>
        ) : entries.length === 0 ? (
          <div className="flex h-48 flex-col items-center justify-center gap-2 text-center text-muted-foreground">
            <FileText className="h-7 w-7" />
            <p className="text-sm">{t("no_attachments")}</p>
          </div>
        ) : entries.map((entry) => {
          const dateKey = `${entry.timestamp.getFullYear()}-${entry.timestamp.getMonth()}-${entry.timestamp.getDate()}`;
          const showDate = dateKey !== previousDate;
          previousDate = dateKey;
          return (
            <div key={entry.key}>
              {showDate && (
                <div className="relative my-5 flex items-center" role="separator">
                  <hr className="w-full border-t-2 border-border" />
                  <span className="absolute left-1/2 -translate-x-1/2 bg-background px-3 text-xs font-semibold text-muted-foreground">
                    {dateFormatter.format(entry.timestamp)}
                  </span>
                </div>
              )}
              <article className="mb-5">
                <time className="mb-1 block text-xs font-medium text-muted-foreground" dateTime={entry.timestamp.toISOString()}>
                  {timeFormatter.format(entry.timestamp)}
                </time>
                <MessageAttachments
                  attachments={entry.serializedAttachment}
                  conversationID={entry.message.protocolConvId}
                  messageID={entry.message.protocolMsgId || String(entry.message.id)}
                  isFromMe={entry.message.isFromMe}
                  layout="bubble"
                />
              </article>
            </div>
          );
        })}
      </div>
    </aside>
  );
}
