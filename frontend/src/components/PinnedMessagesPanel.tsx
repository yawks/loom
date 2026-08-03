import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { ArrowLeft, FileText, Loader2, Pin, Trash2, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { MessageText } from "./MessageText";
import { models } from "../../wailsjs/go/models";
import { timeToDate } from "@/lib/utils";
import { useState } from "react";
import { useTranslation } from "react-i18next";

type PinnedAttachment = {
  fileName?: string;
  filename?: string;
  name?: string;
};

function getPinnedAttachmentName(message?: models.Message): string | null {
  if (!message?.attachments) return null;
  try {
    const attachments = JSON.parse(message.attachments) as Array<PinnedAttachment | string>;
    const first = attachments[0];
    if (typeof first === "string") return first.split(/[\\/]/).pop() || null;
    return first?.fileName || first?.filename || first?.name || null;
  } catch {
    // The message is still resolved even if an older attachment payload cannot
    // be decoded by this frontend version.
    return null;
  }
}

export function PinnedMessagesPanel({ pins, loading, onClose, onOpen, onUnpin }: {
  pins: models.MessagePin[];
  loading: boolean;
  onClose: () => void;
  onOpen: (pin: models.MessagePin) => void | Promise<void>;
  onUnpin: (pin: models.MessagePin) => void;
}) {
  const { t } = useTranslation();
  const [pinToUnpin, setPinToUnpin] = useState<models.MessagePin | null>(null);
  const [openingPinId, setOpeningPinId] = useState<string | null>(null);

  const handleOpen = async (pin: models.MessagePin) => {
    if (openingPinId) return;
    setOpeningPinId(pin.protocolMsgId);
    try {
      await onOpen(pin);
    } finally {
      setOpeningPinId(null);
    }
  };
  return (
    <>
    <aside className="absolute inset-y-0 right-0 z-40 flex w-[min(380px,90%)] flex-col border-l bg-background shadow-xl">
      <div className="flex h-[73px] shrink-0 items-center justify-between border-b px-4">
        <div><h3 className="font-semibold">{t("pinned_messages")}</h3><p className="text-xs text-muted-foreground">{t("pinned_messages_help")}</p></div>
        <Button variant="ghost" size="icon" onClick={onClose}><X className="h-4 w-4" /></Button>
      </div>
      <div className="flex-1 overflow-y-auto p-3">
        {loading ? <div className="flex h-32 items-center justify-center"><Loader2 className="h-5 w-5 animate-spin" /></div> : pins.length === 0 ? (
          <div className="flex h-48 flex-col items-center justify-center gap-2 text-center text-muted-foreground"><Pin className="h-7 w-7" /><p className="text-sm">{t("no_pinned_messages")}</p></div>
        ) : pins.map((pin) => {
          const attachmentName = getPinnedAttachmentName(pin.message);
          const hasAttachment = Boolean(pin.message?.attachments);
          return (
          <div key={pin.protocolMsgId} className="mb-2 rounded-lg border p-3">
            <div>
              <div className="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
                <span>{pin.scope === "personal" ? t("personal_pin") : t("shared_pin")}</span>
                {pin.messageTimestamp && <span>· {timeToDate(pin.messageTimestamp).toLocaleDateString()}</span>}
              </div>
              {pin.message?.body ? (
                <MessageText
                  text={pin.message.body}
                  providerInstanceId={pin.providerInstanceId}
                  className="line-clamp-4 text-sm"
                  emojiSize={14}
                  isFromMe={pin.message.isFromMe}
                />
              ) : hasAttachment ? (
                <p className="flex items-center gap-2 text-sm"><FileText className="h-4 w-4 shrink-0" /><span className="truncate">{attachmentName || t("pinned_attachment")}</span></p>
              ) : (
                <p className="line-clamp-4 text-sm">{t(pin.resolution === "unavailable" ? "pinned_message_unavailable" : "pinned_message_not_synced")}</p>
              )}
              {pin.message?.senderName && <p className="mt-2 text-xs font-medium">{pin.message.senderName}</p>}
            </div>
            <div className="mt-2 flex items-center justify-between border-t pt-2">
              <Button variant="link" size="sm" className="h-auto px-0 cursor-pointer" disabled={openingPinId !== null} onClick={() => void handleOpen(pin)}>
                {openingPinId === pin.protocolMsgId
                  ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" />
                  : <ArrowLeft className="mr-1 h-3.5 w-3.5" />}
                {t(openingPinId === pin.protocolMsgId ? "opening_pinned_message" : "view_pinned_message")}
              </Button>
              <Button variant="ghost" size="sm" disabled={openingPinId !== null} onClick={() => setPinToUnpin(pin)}><Trash2 className="mr-1 h-3.5 w-3.5" />{t("unpin")}</Button>
            </div>
          </div>
          );
        })}
      </div>
    </aside>
    <AlertDialog open={pinToUnpin !== null} onOpenChange={(open) => { if (!open) setPinToUnpin(null); }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("unpin_message_confirm_title")}</AlertDialogTitle>
          <AlertDialogDescription>{t("unpin_message_confirm_description")}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
          <AlertDialogAction
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            onClick={() => {
              if (pinToUnpin) onUnpin(pinToUnpin);
              setPinToUnpin(null);
            }}
          >
            {t("unpin")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
    </>
  );
}
