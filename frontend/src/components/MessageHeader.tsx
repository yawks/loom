import { Button } from "@/components/ui/button";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Calendar, Info, MessageSquare } from "lucide-react";
import { ProtocolSwitcher } from "./ProtocolSwitcher";
import { ProtocolIcon } from "./ProtocolIcon";
import type { models } from "../../wailsjs/go/models";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { useAppStore } from "@/lib/store";
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { GetConfiguredProviders } from "../../wailsjs/go/main/App";
import { TypingIndicator } from "./TypingIndicator";
import { useTypingStore } from "@/lib/typingStore";
import { usePresenceStore } from "@/lib/presenceStore";

export function MessageHeader({
  displayName,
  avatarUrl,
  conversationId,
  linkedAccounts,
  onToggleThreads,
  onToggleDetails,
}: {
  displayName: string;
  avatarUrl?: string;
  conversationId: string;
  linkedAccounts: models.LinkedAccount[];
  onToggleThreads: () => void;
  onToggleDetails: () => void;
}) {
  const { t } = useTranslation();
  const capabilities = useAppStore((state) => state.capabilities);
  const showThreads = useAppStore((state) => state.showThreads);
  const isTyping = useTypingStore(
    (state) => (state.typingByConversation[conversationId]?.length ?? 0) > 0
  );
  const presenceMap = usePresenceStore((state) => state.presenceMap);

  const instanceId = linkedAccounts[0]?.providerInstanceId;
  const supportsThreads = instanceId ? capabilities[instanceId]?.supportsThreads ?? false : false;
  const isGroup = linkedAccounts[0]?.isGroup ?? false;
  const accountStatus = linkedAccounts.find(
    (account) => account.status && account.status !== "offline"
  )?.status;
  const hasOnlinePresence = linkedAccounts.some(
    (account) => presenceMap[account.userId] === true
  );
  const status = accountStatus || (hasOnlinePresence ? "online" : null);

  const { data: configuredProviders = [] } = useQuery({
    queryKey: ["configuredProviders"],
    queryFn: () => GetConfiguredProviders().catch(() => []),
  });

  const providerEntries = useMemo(() => {
    const byInstanceId = new Map(
      configuredProviders.map((p) => [p.instanceId || p.id, p])
    );
    return linkedAccounts
      .map((a) => {
        const p = byInstanceId.get(a.providerInstanceId);
        return p ? { name: p.instanceName || p.name, protocolId: p.id } : null;
      })
      .filter(Boolean) as { name: string; protocolId: string }[];
  }, [configuredProviders, linkedAccounts]);

  return (
    <div className={cn("message-header p-4 border-b flex justify-between items-center shrink-0 transition-opacity duration-200", showThreads && "opacity-20")}>
      <div className="message-header__identity flex items-center gap-3 min-w-0">
        <div className="relative shrink-0">
          <Avatar className="message-header__avatar h-9 w-9">
            <AvatarImage src={avatarUrl} alt={displayName} />
            <AvatarFallback>{displayName.substring(0, 2).toUpperCase()}</AvatarFallback>
          </Avatar>
          {!isGroup && !isTyping && status && (
            status === "meeting" ? (
              <div
                className="absolute -bottom-0.5 -right-0.5 h-3.5 w-3.5 rounded bg-blue-500 border-2 border-background flex items-center justify-center"
                title={t("meeting") || "En réunion"}
              >
                <Calendar className="h-2 w-2 text-white" />
              </div>
            ) : (
              <div
                className={cn(
                  "absolute -bottom-0.5 -right-0.5 h-3 w-3 rounded-full border-2 border-background",
                  status === "online" && "bg-green-500",
                  status === "away" && "bg-yellow-500",
                  (status === "busy" || status === "dnd") && "bg-red-500",
                  status === "holiday" && "bg-purple-500",
                  !["online", "away", "busy", "dnd", "holiday"].includes(status) && "bg-gray-500"
                )}
                title={t(status) || status}
              />
            )
          )}
        </div>
        <div className="message-header__name-block flex flex-col min-w-0">
          <h2 className="message-header__display-name text-lg font-semibold leading-tight truncate">{displayName}</h2>
          {isTyping ? (
            <TypingIndicator conversationId={conversationId} variant="header" />
          ) : providerEntries.length > 0 && (
            <span className="message-header__provider-names flex items-center gap-1 text-xs opacity-40 truncate">
              {providerEntries.map((e, i) => (
                <span key={e.protocolId + i} className="message-header__provider-entry flex items-center gap-0.5">
                  <ProtocolIcon protocol={e.protocolId} size={12} />
                  {e.name}
                  {i < providerEntries.length - 1 && <span className="mx-0.5">·</span>}
                </span>
              ))}
            </span>
          )}
        </div>
      </div>
      <div className="flex items-center gap-2 shrink-0">
        <Button
          variant="ghost"
          size="icon"
          onClick={onToggleDetails}
          title="Conversation Details"
        >
          <Info className="h-4 w-4" />
        </Button>
        {supportsThreads && (
          <Button
            variant="ghost"
            size="icon"
            onClick={onToggleThreads}
            title={t("threads")}
          >
            <MessageSquare className="h-4 w-4" />
          </Button>
        )}
        <ProtocolSwitcher linkedAccounts={linkedAccounts} />
      </div>
    </div>
  );
}
