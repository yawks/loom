import { CalendarClock, Loader2, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { CancelScheduledMessage, GetScheduledMessages, ScheduleMessage } from "../../wailsjs/go/main/App";
import type { models } from "../../wailsjs/go/models";

interface ScheduledMessagesDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  conversationId: string;
  draft: string;
  onScheduled: () => void;
  supportsListScheduledMessages?: boolean;
}

const defaultScheduleValue = () => {
  const date = new Date(Date.now() + 60 * 60 * 1000);
  date.setMinutes(Math.ceil(date.getMinutes() / 5) * 5, 0, 0);
  const offset = date.getTimezoneOffset() * 60_000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 16);
};

export function ScheduledMessagesDialog({ open, onOpenChange, conversationId, draft, onScheduled, supportsListScheduledMessages = true }: ScheduledMessagesDialogProps) {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const [scheduledAt, setScheduledAt] = useState(defaultScheduleValue);
  const queryKey = useMemo(() => ["scheduled-messages", conversationId], [conversationId]);
  const { data: messages = [], isLoading, error } = useQuery<models.ScheduledMessage[]>({
    queryKey,
    queryFn: () => GetScheduledMessages(conversationId),
    enabled: open && Boolean(conversationId) && supportsListScheduledMessages,
  });
  const schedule = useMutation({
    mutationFn: () => ScheduleMessage(conversationId, draft.trim(), new Date(scheduledAt).toISOString(), ""),
    onSuccess: async () => {
      onScheduled();
      await queryClient.invalidateQueries({ queryKey });
    },
  });
  const cancel = useMutation({
    mutationFn: (message: models.ScheduledMessage) => CancelScheduledMessage(message.protocolConvId, message.id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey }),
  });
  const scheduleDate = new Date(scheduledAt);
  const canSchedule = draft.trim().length > 0 && !Number.isNaN(scheduleDate.getTime()) && scheduleDate.getTime() > Date.now();

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2"><CalendarClock className="h-5 w-5" />{t("scheduled_messages_title")}</DialogTitle>
          <DialogDescription>{t("scheduled_messages_description")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 rounded-lg border p-3">
          <p className="line-clamp-3 whitespace-pre-wrap text-sm text-muted-foreground">{draft.trim() || t("scheduled_messages_enter_draft")}</p>
          <div className="flex items-center gap-2">
            <input type="datetime-local" value={scheduledAt} min={defaultScheduleValue()} onChange={(event) => setScheduledAt(event.target.value)} className="h-9 flex-1 rounded-md border border-input bg-background px-3 text-sm" />
            <Button disabled={!canSchedule || schedule.isPending} onClick={() => schedule.mutate()}>
              {schedule.isPending && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}{t("schedule_message")}
            </Button>
          </div>
          {schedule.error && <p className="text-sm text-destructive">{String(schedule.error)}</p>}
        </div>
        <div className="max-h-80 space-y-2 overflow-y-auto">
          {isLoading && <div className="flex justify-center p-6"><Loader2 className="h-5 w-5 animate-spin" /></div>}
          {error && <p className="p-4 text-sm text-destructive">{String(error)}</p>}
          {!isLoading && !error && messages.length === 0 && <p className="p-6 text-center text-sm text-muted-foreground">{t("scheduled_messages_empty")}</p>}
          {messages.map((message) => (
            <div key={message.id} className="flex items-start gap-3 rounded-lg border p-3">
              <div className="min-w-0 flex-1">
                <p className="whitespace-pre-wrap text-sm">{message.body}</p>
                <p className="mt-1 text-xs text-muted-foreground">{new Date(message.scheduledAt as unknown as string).toLocaleString(i18n.language, { dateStyle: "medium", timeStyle: "short" })}</p>
              </div>
              <Button variant="ghost" size="icon" title={t("cancel_scheduled_message")} disabled={cancel.isPending} onClick={() => cancel.mutate(message)}><Trash2 className="h-4 w-4" /></Button>
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}
