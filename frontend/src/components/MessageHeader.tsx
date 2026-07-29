import { Button } from "@/components/ui/button";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Info, MessageSquare } from "lucide-react";
import { ProtocolSwitcher } from "./ProtocolSwitcher";
import { ProtocolIcon } from "./ProtocolIcon";
import type { models } from "../../wailsjs/go/models";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { useAppStore } from "@/lib/store";
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { GetConfiguredProviders } from "../../wailsjs/go/main/App";

export function MessageHeader({
  displayName,
  avatarUrl,
  linkedAccounts,
  onToggleThreads,
  onToggleDetails,
}: {
  displayName: string;
  avatarUrl?: string;
  linkedAccounts: models.LinkedAccount[];
  onToggleThreads: () => void;
  onToggleDetails: () => void;
}) {
  const { t } = useTranslation();
  const capabilities = useAppStore((state) => state.capabilities);
  const showThreads = useAppStore((state) => state.showThreads);

  const instanceId = linkedAccounts[0]?.providerInstanceId;
  const supportsThreads = instanceId ? capabilities[instanceId]?.supportsThreads ?? true : true;

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
        <Avatar className="message-header__avatar h-9 w-9 shrink-0">
          <AvatarImage src={avatarUrl} alt={displayName} />
          <AvatarFallback>{displayName.substring(0, 2).toUpperCase()}</AvatarFallback>
        </Avatar>
        <div className="message-header__name-block flex flex-col min-w-0">
          <h2 className="message-header__display-name text-lg font-semibold leading-tight truncate">{displayName}</h2>
          {providerEntries.length > 0 && (
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
