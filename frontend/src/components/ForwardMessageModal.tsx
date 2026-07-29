import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { CheckCheck, Search } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { MessageText } from "./MessageText";
import { ProtocolIcon } from "./ProtocolIcon";
import { cn, timeToDate } from "@/lib/utils";
import { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useTranslation } from "react-i18next";
import { GetAllLastMessageTimestamps, GetAttachmentData, GetConfiguredProviders, SendFile, SendMessage } from "../../wailsjs/go/main/App";

interface ForwardMessageModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  message: models.Message | null;
  providerInstanceId?: string;
}

interface ConversationEntry {
  contact: models.MetaContact;
  account: models.LinkedAccount;
  conversationId: string;
  providerLabel: string;
  protocolId: string;
  activityTime: number;
}

export function ForwardMessageModal({
  open,
  onOpenChange,
  message,
  providerInstanceId,
}: ForwardMessageModalProps) {
  const { t } = useTranslation();
  const [searchQuery, setSearchQuery] = useState("");
  const [sentIds, setSentIds] = useState<Set<string>>(new Set());
  const [sendingIds, setSendingIds] = useState<Set<string>>(new Set());
  const inputRef = useRef<HTMLInputElement>(null);

  const contacts = useAppStore((state) => state.metaContacts);

  const { data: configuredProviders = [] } = useQuery({
    queryKey: ["configuredProviders"],
    queryFn: () => GetConfiguredProviders().catch(() => []),
  });

  const { data: allLastMessageTimestamps = {} } = useQuery<Record<string, unknown>>({
    queryKey: ["allLastMessageTimestamps"],
    queryFn: async () => {
      const ts = await GetAllLastMessageTimestamps().catch(() => ({}));
      return ts || {};
    },
    staleTime: 30000,
  });

  const lastMessageDates = useMemo(() => {
    const dates: Record<string, number> = {};
    for (const [convId, timestamp] of Object.entries(allLastMessageTimestamps)) {
      if (timestamp) {
        dates[convId] =
          typeof timestamp === "number"
            ? timestamp * 1000
            : timeToDate(timestamp as string).getTime();
      }
    }
    return dates;
  }, [allLastMessageTimestamps]);

  const providerNameById = useMemo(() => {
    const map = new Map<string, string>();
    for (const p of configuredProviders) {
      const id = p.instanceId || p.id;
      map.set(id, p.instanceName || p.name);
    }
    return map;
  }, [configuredProviders]);

  const providerProtocolById = useMemo(() => {
    const map = new Map<string, string>();
    for (const p of configuredProviders) {
      map.set(p.instanceId || p.id, p.id);
    }
    return map;
  }, [configuredProviders]);

  const allEntries = useMemo<ConversationEntry[]>(() => {
    const entries: ConversationEntry[] = [];
    for (const contact of contacts) {
      for (const account of contact.linkedAccounts ?? []) {
        const convId = account.conversationId || account.userId;
        if (!convId) continue;

        const providerName = providerNameById.get(account.providerInstanceId) ?? account.protocol;
        const accountLabel = account.username || account.userId || "";
        const providerLabel = accountLabel ? `${providerName} (${accountLabel})` : providerName;

        const activityTime = lastMessageDates[convId] ?? lastMessageDates[account.userId] ?? 0;

        entries.push({
          contact,
          account,
          conversationId: convId,
          providerLabel,
          protocolId: providerProtocolById.get(account.providerInstanceId) ?? account.protocol ?? "",
          activityTime,
        });
      }
    }
    return entries.sort((a, b) => b.activityTime - a.activityTime);
  }, [contacts, providerNameById, providerProtocolById, lastMessageDates]);

  const displayedEntries = useMemo(() => {
    if (!searchQuery.trim()) {
      return allEntries.slice(0, 20);
    }
    const q = searchQuery.trim().toLowerCase();
    return allEntries
      .filter(
        (e) =>
          e.contact.displayName.toLowerCase().includes(q) ||
          e.providerLabel.toLowerCase().includes(q)
      )
      .slice(0, 20);
  }, [allEntries, searchQuery]);

  useEffect(() => {
    if (open) {
      setSearchQuery("");
      setSentIds(new Set());
      setSendingIds(new Set());
      setTimeout(() => inputRef.current?.focus(), 100);
    }
  }, [open]);

  const handleSend = async (entry: ConversationEntry) => {
    if (!message) return;
    const key = `${entry.account.id}:${entry.conversationId}`;
    setSendingIds((prev) => new Set(prev).add(key));
    try {
      if (message.body) {
        await SendMessage(entry.conversationId, message.body);
      }
      if (message.attachments) {
        let parsed: Array<{ type: string; url: string; fileName: string; fileSize: number; mimeType: string; thumbnail?: string }> = [];
        try {
          parsed = JSON.parse(message.attachments);
        } catch {
          // malformed attachments JSON — skip
        }
        for (const att of parsed) {
          const url = att.url || att.thumbnail;
          if (!url) continue;
          const base64Data = await GetAttachmentData(url);
          if (!base64Data) continue;
          await SendFile(entry.conversationId, base64Data, att.fileName || "file", att.mimeType || "application/octet-stream");
        }
      }
      setSentIds((prev) => new Set(prev).add(key));
    } catch (err) {
      console.error("Forward failed:", err);
    } finally {
      setSendingIds((prev) => {
        const next = new Set(prev);
        next.delete(key);
        return next;
      });
    }
  };

  const senderName = message?.senderName || t("unknown");
  const initials = senderName.substring(0, 2).toUpperCase();

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="forward-message-modal max-w-lg max-h-[85vh] flex flex-col p-0 overflow-hidden">
        <DialogHeader className="forward-message-modal__header px-6 pt-6 pb-4 border-b">
          <DialogTitle>{t("forward_message_title")}</DialogTitle>
          {message && (
            <div className="forward-message-modal__preview mt-3">
              <p className="text-xs text-muted-foreground mb-2">{t("message_preview")}</p>
              <div className="forward-message-modal__preview-card flex gap-3 p-3 rounded-lg bg-muted/50 border">
                <Avatar className="h-8 w-8 shrink-0">
                  <AvatarImage src={message.senderAvatarUrl || ""} alt={senderName} />
                  <AvatarFallback className="text-xs bg-primary text-primary-foreground">
                    {initials}
                  </AvatarFallback>
                </Avatar>
                <div className="min-w-0 flex-1">
                  <p className="forward-message-modal__preview-sender text-sm font-semibold text-primary mb-0.5">
                    {senderName}
                  </p>
                  {message.body ? (
                    <div className="forward-message-modal__preview-body text-sm text-foreground line-clamp-3">
                      <MessageText
                        text={message.body}
                        providerInstanceId={providerInstanceId}
                        preview
                      />
                    </div>
                  ) : (
                    <p className="text-sm text-muted-foreground italic">{t("attachment")}</p>
                  )}
                </div>
              </div>
            </div>
          )}
        </DialogHeader>

        <div className="forward-message-modal__search px-4 pt-3 pb-2">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
            <Input
              ref={inputRef}
              placeholder={t("forward_search_placeholder")}
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="pl-10"
              autoCorrect="off"
              autoComplete="off"
              spellCheck={false}
            />
            {searchQuery && (
              <button
                type="button"
                onClick={() => setSearchQuery("")}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              >
                ×
              </button>
            )}
          </div>
        </div>

        <div className="forward-message-modal__list flex-1 overflow-y-auto px-4 pb-4 space-y-1 min-h-0">
          {displayedEntries.length === 0 ? (
            <div className="py-8 text-center text-sm text-muted-foreground">
              {t("search_modal_no_results")}
            </div>
          ) : (
            displayedEntries.map((entry) => {
              const key = `${entry.account.id}:${entry.conversationId}`;
              const isSent = sentIds.has(key);
              const isSending = sendingIds.has(key);
              return (
                <div
                  key={key}
                  className="forward-message-modal__item flex items-center gap-3 p-2.5 rounded-lg hover:bg-muted/60 transition-colors"
                >
                  <Avatar className="h-10 w-10 shrink-0">
                    <AvatarImage src={entry.contact.avatarUrl} alt={entry.contact.displayName} />
                    <AvatarFallback>
                      {entry.contact.displayName.substring(0, 2).toUpperCase()}
                    </AvatarFallback>
                  </Avatar>
                  <div className="forward-message-modal__item-info flex-1 min-w-0">
                    <p className="forward-message-modal__item-name font-medium truncate text-sm">
                      {entry.contact.displayName}
                    </p>
                    <p className="forward-message-modal__item-provider flex items-center gap-1 text-xs text-muted-foreground truncate">
                      {entry.protocolId && <ProtocolIcon protocol={entry.protocolId} size={12} />}
                      {entry.providerLabel}
                    </p>
                  </div>
                  {isSent ? (
                    <span className={cn("forward-message-modal__item-sent flex items-center gap-1 text-xs font-medium text-green-600 dark:text-green-400 px-3 py-1.5")}>
                      <CheckCheck className="h-3.5 w-3.5" />
                      {t("sent")}
                    </span>
                  ) : (
                    <Button
                      size="sm"
                      variant="outline"
                      className="forward-message-modal__item-send shrink-0 h-8 px-3 text-xs"
                      disabled={isSending}
                      onClick={() => handleSend(entry)}
                    >
                      {isSending ? "..." : t("send")}
                    </Button>
                  )}
                </div>
              );
            })
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
