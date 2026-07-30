import { useEffect, useMemo, useState } from "react";

import { EventsOn } from "../../wailsjs/runtime/runtime";
import { GetConfiguredProviders } from "../../wailsjs/go/main/App";
import { AlertTriangle, Layers, Settings } from "lucide-react";
import { ProtocolIcon } from "./ProtocolIcon";
import { cn } from "@/lib/utils";
import type { core } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useTranslation } from "react-i18next";

const COLOR_VARIATIONS = [
  { filter: "hue-rotate(0deg)" },
  { filter: "hue-rotate(60deg)" },
  { filter: "hue-rotate(120deg)" },
  { filter: "hue-rotate(180deg)" },
  { filter: "hue-rotate(240deg)" },
  { filter: "hue-rotate(300deg)" },
];

interface ProviderFilterBarProps {
  onOpenSettings: () => void;
}

export function ProviderFilterBar({
  onOpenSettings,
}: ProviderFilterBarProps) {
  const { t } = useTranslation();
  const [configuredProviders, setConfiguredProviders] = useState<
    core.ProviderInfo[]
  >([]);
  const selectedProviderFilter = useAppStore(
    (state) => state.selectedProviderFilter
  );
  const setSelectedProviderFilter = useAppStore(
    (state) => state.setSelectedProviderFilter
  );
  const metaContacts = useAppStore((state) => state.metaContacts);
  const syncErrors = useAppStore((state) => state.syncErrors);
  const setSyncError = useAppStore((state) => state.setSyncError);
  const clearSyncError = useAppStore((state) => state.clearSyncError);
  const readStateByConversation = useMessageReadStore(
    (state) => state.readByConversation
  );

  const unreadByInstance = useMemo(() => {
    const counts: Record<string, number> = {};
    const countedConversations = new Set<string>();
    metaContacts.forEach((contact) => {
      contact.linkedAccounts.forEach((account) => {
        // A userId identifies a contact, not a conversation. In particular the
        // same WhatsApp JID can exist in two instances. Only a namespaced
        // conversationId is valid for unread state.
        const conversationId = account.conversationId;
        if (!conversationId || countedConversations.has(conversationId)) return;
        const conversationState = readStateByConversation[conversationId];
        if (!conversationState) return;
        const unread = Object.entries(conversationState).filter(
          ([key, isRead]) => !key.startsWith("_") && !isRead
        ).length;
        if (unread > 0) {
          countedConversations.add(conversationId);
          counts[account.providerInstanceId] =
            (counts[account.providerInstanceId] ?? 0) + unread;
        }
      });
    });
    return counts;
  }, [metaContacts, readStateByConversation]);

  const loadProviders = async () => {
    try {
      const providers = await GetConfiguredProviders();
      setConfiguredProviders(providers);
      // Seed the sync-error store from the value the backend computed at startup.
      // This is the reliable path: no event timing required.
      providers.forEach((p) => {
        const id = p.instanceId || p.id;
        if (p.syncError) {
          setSyncError(id, p.syncError);
        } else {
          clearSyncError(id);
        }
      });
    } catch (error) {
      console.error("Failed to load providers:", error);
    }
  };

  useEffect(() => {
    loadProviders();
  }, []);

  useEffect(() => {
    if (selectedProviderFilter && configuredProviders.length > 0) {
      const providerExists = configuredProviders.some(
        (p) => (p.instanceId || p.id) === selectedProviderFilter
      );
      if (!providerExists) {
        setSelectedProviderFilter(null);
      }
    }
  }, [configuredProviders, selectedProviderFilter, setSelectedProviderFilter]);

  useEffect(() => {
    const unsubscribe = EventsOn("contacts-refresh", () => {
      loadProviders();
    });
    return () => {
      if (unsubscribe) unsubscribe();
    };
  }, []);

  useEffect(() => {
    const unsubscribe = EventsOn("sync-status", (statusJSON: string) => {
      try {
        const parsed = JSON.parse(statusJSON);
        const instanceId = parsed.InstanceID || parsed.instanceId;
        const status = (parsed.Status || parsed.status || "").toLowerCase();
        const message = parsed.Message || parsed.message || "";
        if (!instanceId) return;
        if (status === "error") {
          setSyncError(instanceId, message);
        } else {
          clearSyncError(instanceId);
        }
      } catch (e) {
        console.error("Failed to parse sync status in ProviderFilterBar:", e);
      }
    });
    return () => {
      if (unsubscribe) unsubscribe();
    };
  }, [setSyncError, clearSyncError]);

  const providersByType = useMemo(() => {
    const groups: Record<string, core.ProviderInfo[]> = {};
    configuredProviders.forEach((provider) => {
      const key = provider.id;
      if (!groups[key]) groups[key] = [];
      groups[key].push(provider);
    });
    return groups;
  }, [configuredProviders]);

  const getColorVariation = (provider: core.ProviderInfo) => {
    const instances = providersByType[provider.id] || [];
    if (instances.length <= 1) return null;
    const index = instances.findIndex(
      (p) => (p.instanceId || p.id) === (provider.instanceId || provider.id)
    );
    return index >= 0 && index < COLOR_VARIATIONS.length
      ? COLOR_VARIATIONS[index]
      : null;
  };

  const totalUnread = Object.values(unreadByInstance).reduce(
    (sum, n) => sum + n,
    0
  );

  const railButtonClass =
    "provider-filter-bar__rail-btn h-9 w-9 flex items-center justify-center rounded-lg relative transition-colors hover:bg-black/5 dark:hover:bg-white/10 text-sidebar-rail-foreground cursor-pointer border-0 bg-transparent";

  return (
    <div className="provider-filter-bar w-14 flex-none flex flex-col items-center gap-1 py-3 bg-sidebar-rail border-r border-black/20">
      {/* App logo */}
      <div className="provider-filter-bar__logo mb-2">
        <img
          src="/appicon.png"
          alt="Loom"
          className="h-8 w-8 rounded-lg opacity-90"
        />
      </div>

      <div className="w-8 h-px bg-white/10 mb-1" />

      {/* "All" button — only when 2+ providers */}
      {configuredProviders.length >= 2 && (
        <button
          className={cn(
            railButtonClass,
            "provider-filter-bar__all-button",
            selectedProviderFilter === null &&
              "bg-black/10 text-foreground dark:bg-white/15 dark:text-white"
          )}
          onClick={() => setSelectedProviderFilter(null)}
          title={t("all") || "All"}
        >
          <Layers className="h-5 w-5" />
          {totalUnread > 0 && selectedProviderFilter !== null && (
            <span className="provider-filter-bar__unread-badge absolute -top-1 -right-1 h-4 min-w-4 px-0.5 rounded-full bg-destructive text-destructive-foreground text-[10px] font-bold leading-4 text-center pointer-events-none">
              {totalUnread > 99 ? "99+" : totalUnread}
            </span>
          )}
        </button>
      )}

      {/* Provider buttons */}
      {configuredProviders.map((provider) => {
        const instanceId = provider.instanceId || provider.id;
        const isSelected = selectedProviderFilter === instanceId;
        const colorVariation = getColorVariation(provider);
        const displayName = provider.instanceName || provider.name;
        const unreadCount = unreadByInstance[instanceId] ?? 0;
        const syncError = syncErrors[instanceId];

        return (
          <button
            key={instanceId}
            className={cn(
              railButtonClass,
              "provider-filter-bar__provider-button",
              isSelected && "bg-black/10 text-foreground dark:bg-white/15 dark:text-white"
            )}
            onClick={() => setSelectedProviderFilter(instanceId)}
            title={syncError ? `${displayName} — ${syncError}` : displayName}
          >
            <div
              className="provider-filter-bar__provider-icon h-6 w-6 flex items-center justify-center"
              style={colorVariation || undefined}
            >
              <ProtocolIcon protocol={provider.id} size={24} />
            </div>
            {unreadCount > 0 && (
              <span className="provider-filter-bar__unread-badge absolute -top-1 -right-1 h-4 min-w-4 px-0.5 rounded-full bg-destructive text-destructive-foreground text-[10px] font-bold leading-4 text-center pointer-events-none">
                {unreadCount > 99 ? "99+" : unreadCount}
              </span>
            )}
            {syncError && (
              <span className="provider-filter-bar__sync-error-badge absolute -bottom-1 -right-1 h-4 w-4 flex items-center justify-center rounded-full bg-orange-500 pointer-events-none">
                <AlertTriangle className="h-2.5 w-2.5 text-white" />
              </span>
            )}
          </button>
        );
      })}

      {/* Spacer */}
      <div className="flex-1" />

      {/* Bottom actions */}
      <button
        className={cn(railButtonClass, "provider-filter-bar__settings-button")}
        onClick={onOpenSettings}
        title={t("settings") || "Settings"}
      >
        <Settings className="h-4 w-4" />
      </button>
    </div>
  );
}
