import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Bell, BellOff, Calendar, Info, MessageSquare, Pin, Search } from "lucide-react";

import { Button } from "@/components/ui/button";
import { GetConfiguredProviders, GetConversationState, SetConversationMuted } from "../../wailsjs/go/main/App";
import { ProtocolIcon } from "./ProtocolIcon";
import { ProtocolSwitcher } from "./ProtocolSwitcher";
import { TypingIndicator } from "./TypingIndicator";
import { ConversationSearchModal } from "./ConversationSearchModal";
import { cn } from "@/lib/utils";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useEffect, useMemo, useState } from "react";
import { usePresenceStore } from "@/lib/presenceStore";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useTypingStore } from "@/lib/typingStore";

export function MessageHeader({
  displayName,
  avatarUrl,
  conversationId,
  contactUserId,
  linkedAccounts,
  activeAccount,
  onToggleThreads,
  onToggleDetails,
  onTogglePins,
  pinCount,
  metaContactId,
}: {
  displayName: string;
  avatarUrl?: string;
  conversationId: string;
  contactUserId?: string;
  linkedAccounts: models.LinkedAccount[];
  activeAccount?: models.LinkedAccount;
  onToggleThreads: () => void;
  onToggleDetails: () => void;
  onTogglePins: () => void;
  pinCount: number;
  metaContactId: number;
}) {
  const { t } = useTranslation();
  const capabilities = useAppStore((state) => state.capabilities);
  const showThreads = useAppStore((state) => state.showThreads);
  const isBadgeTracked = useAppStore(
    (state) => !state.badgeUntrackedConversationIds[conversationId]
  );
  const setConversationBadgeTracked = useAppStore(
    (state) => state.setConversationBadgeTracked
  );
  const setSelectedContactProfile = useAppStore((state) => state.setSelectedContactProfile);
  const isTyping = useTypingStore(
    (state) => (state.typingByConversation[conversationId]?.length ?? 0) > 0
  );
  const presenceMap = usePresenceStore((state) => state.presenceMap);
  const [isConversationSearchOpen, setIsConversationSearchOpen] = useState(false);

  const selectedAccount = activeAccount ?? linkedAccounts[0];
  const instanceId = selectedAccount?.providerInstanceId;
  const supportsThreads = instanceId ? capabilities[instanceId]?.supportsThreads ?? false : false;
  const supportsNativeMute = instanceId
    ? capabilities[instanceId]?.supportsMuteConversation ?? false
    : false;

  useEffect(() => {
    if (!supportsNativeMute || !conversationId) return;
    let cancelled = false;
    GetConversationState(conversationId)
      .then((conversation) => {
        if (!cancelled) {
          setConversationBadgeTracked(conversationId, !conversation.isMuted);
        }
      })
      .catch((error: unknown) => {
        console.debug("Failed to load native conversation mute state:", error);
      });
    return () => {
      cancelled = true;
    };
  }, [conversationId, setConversationBadgeTracked, supportsNativeMute]);

  useEffect(() => {
    const handleConversationSearchShortcut = (event: KeyboardEvent) => {
      const isMac = navigator.platform.toUpperCase().includes("MAC");
      if ((isMac ? event.metaKey : event.ctrlKey) && event.key.toLowerCase() === "f") {
        event.preventDefault();
        setIsConversationSearchOpen(true);
      }
    };
    window.addEventListener("keydown", handleConversationSearchShortcut);
    return () => window.removeEventListener("keydown", handleConversationSearchShortcut);
  }, []);
  const isGroup = selectedAccount?.isGroup ?? false;
  const accountStatus = linkedAccounts.find(
    (account) => account.status && account.status !== "offline"
  )?.status;
  const hasOnlinePresence = linkedAccounts.some(
    (account) => presenceMap[account.userId] === true
  );
  const status = accountStatus || (hasOnlinePresence ? "online" : null);
  const profileAccount = linkedAccounts.find((account) => !account.isGroup) ?? linkedAccounts[0];

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
    <>
    <div className={cn("message-header p-4 border-b flex justify-between items-center shrink-0 transition-opacity duration-200", showThreads && "opacity-20")}>
      <div className="message-header__identity flex items-center gap-3 min-w-0">
        <button
          type="button"
          className="relative shrink-0"
          disabled={isGroup || !profileAccount}
          onClick={() => {
            if (!profileAccount || isGroup) return;
            setSelectedContactProfile({
              conversationId,
              userId: contactUserId || profileAccount.userId,
              displayName,
              avatarUrl,
              status: status || "offline",
            });
          }}
          aria-label={t("contact_profile.title")}
        >
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
        </button>
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
          onClick={() => setIsConversationSearchOpen(true)}
          title={t("search_in_conversation")}
          aria-label={t("search_in_conversation")}
        >
          <Search className="h-4 w-4" />
        </Button>
        <Button
          variant={isBadgeTracked ? "ghost" : "secondary"}
          size="icon"
          onClick={() => {
            const nextTracked = !isBadgeTracked;
            setConversationBadgeTracked(conversationId, nextTracked);
            if (supportsNativeMute) {
              SetConversationMuted(conversationId, !nextTracked).catch((error: unknown) => {
                // Badge tracking is intentionally kept as a local fallback if
                // the provider is temporarily unavailable or rejects the mute.
                console.error("Failed to synchronize conversation mute state:", error);
              });
            }
          }}
          title={t(isBadgeTracked ? "exclude_from_app_badge" : "include_in_app_badge")}
          aria-label={t(isBadgeTracked ? "exclude_from_app_badge" : "include_in_app_badge")}
          aria-pressed={!isBadgeTracked}
        >
          {isBadgeTracked ? <Bell className="h-4 w-4" /> : <BellOff className="h-4 w-4" />}
        </Button>
        {instanceId && capabilities[instanceId]?.supportsListMessagePins && (
          <Button variant="ghost" size="icon" className="relative" onClick={onTogglePins} title={t("pinned_messages")}>
            <Pin className="h-4 w-4" />
            {pinCount > 0 && <span className="absolute right-0 top-0 min-w-4 rounded-full bg-primary px-1 text-[10px] leading-4 text-primary-foreground">{pinCount}</span>}
          </Button>
        )}
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
    <ConversationSearchModal
      open={isConversationSearchOpen}
      onOpenChange={setIsConversationSearchOpen}
      metaContactId={metaContactId}
      displayName={displayName}
      avatarUrl={avatarUrl}
    />
    </>
  );
}
