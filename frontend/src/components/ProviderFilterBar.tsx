import { useEffect, useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { GetConfiguredProviders } from "../../wailsjs/go/main/App";
import { Layers } from "lucide-react";
import { ProtocolIcon } from "./ProtocolIcon";
import { cn } from "@/lib/utils";
import type { core } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useTranslation } from "react-i18next";

// Color variations for multiple instances of the same provider
const COLOR_VARIATIONS = [
  { filter: "hue-rotate(0deg)" },
  { filter: "hue-rotate(60deg)" },
  { filter: "hue-rotate(120deg)" },
  { filter: "hue-rotate(180deg)" },
  { filter: "hue-rotate(240deg)" },
  { filter: "hue-rotate(300deg)" },
];

export function ProviderFilterBar() {
  const { t } = useTranslation();
  const [configuredProviders, setConfiguredProviders] = useState<core.ProviderInfo[]>([]);
  const selectedProviderFilter = useAppStore((state) => state.selectedProviderFilter);
  const setSelectedProviderFilter = useAppStore((state) => state.setSelectedProviderFilter);
  const metaContacts = useAppStore((state) => state.metaContacts);
  const readStateByConversation = useMessageReadStore((state) => state.readByConversation);

  // Unread count per providerInstanceId
  const unreadByInstance = useMemo(() => {
    const counts: Record<string, number> = {};
    metaContacts.forEach((contact) => {
      const account = contact.linkedAccounts[0];
      if (!account) return;
      const conversationId = account.conversationId ?? account.userId;
      if (!conversationId) return;
      const conversationState = readStateByConversation[conversationId];
      if (!conversationState) return;
      const unread = Object.entries(conversationState).filter(
        ([key, isRead]) => !key.startsWith("_") && !isRead
      ).length;
      if (unread > 0) {
        counts[account.providerInstanceId] = (counts[account.providerInstanceId] ?? 0) + unread;
      }
    });
    return counts;
  }, [metaContacts, readStateByConversation]);

  const loadProviders = async () => {
    try {
      const providers = await GetConfiguredProviders();
      setConfiguredProviders(providers);
    } catch (error) {
      console.error("Failed to load providers:", error);
    }
  };

  useEffect(() => {
    loadProviders();
  }, []);

  // Check if selected provider still exists when providers list changes
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

  // Listen for provider changes (when providers are added/removed)
  useEffect(() => {
    const unsubscribe = EventsOn("contacts-refresh", () => {
      // Refresh providers list when contacts are refreshed (usually means providers changed)
      loadProviders();
    });

    return () => {
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, []);

  // Group providers by providerId to determine color variations
  const providersByType = useMemo(() => {
    const groups: Record<string, core.ProviderInfo[]> = {};
    configuredProviders.forEach((provider) => {
      const key = provider.id;
      if (!groups[key]) {
        groups[key] = [];
      }
      groups[key].push(provider);
    });
    return groups;
  }, [configuredProviders]);

  // Get color variation for a provider instance
  const getColorVariation = (provider: core.ProviderInfo) => {
    const instances = providersByType[provider.id] || [];
    if (instances.length <= 1) {
      return null; // No variation needed for single instance
    }
    const index = instances.findIndex(
      (p) => (p.instanceId || p.id) === (provider.instanceId || provider.id)
    );
    return index >= 0 && index < COLOR_VARIATIONS.length
      ? COLOR_VARIATIONS[index]
      : null;
  };

  // Only show filter bar if there are 2+ providers
  if (configuredProviders.length < 2) {
    return null;
  }

  const totalUnread = Object.values(unreadByInstance).reduce((sum, n) => sum + n, 0);

  return (
    <div className="provider-filter-bar flex flex-col items-center gap-2 p-2 border-r bg-muted/30">
      {/* All button */}
      <Button
        variant="ghost"
        size="icon"
        className={cn(
          "provider-filter-bar__all-button h-10 w-10 relative",
          selectedProviderFilter === null && "bg-primary/15 text-primary ring-1 ring-primary/40"
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
      </Button>

      {/* Provider buttons */}
      {configuredProviders.map((provider) => {
        const instanceId = provider.instanceId || provider.id;
        const isSelected = selectedProviderFilter === instanceId;
        const colorVariation = getColorVariation(provider);
        const displayName = provider.instanceName || provider.name;
        const unreadCount = unreadByInstance[instanceId] ?? 0;

        return (
          <Button
            key={instanceId}
            variant="ghost"
            size="icon"
            className={cn(
              "provider-filter-bar__provider-button h-10 w-10 relative",
              isSelected && "bg-primary/15 text-primary ring-1 ring-primary/40"
            )}
            onClick={() => setSelectedProviderFilter(instanceId)}
            title={displayName}
          >
            <div
              className="provider-filter-bar__provider-icon h-5 w-5"
              style={colorVariation || undefined}
            >
              <ProtocolIcon protocol={provider.id} size={20} />
            </div>
            {unreadCount > 0 && (
              <span className="provider-filter-bar__unread-badge absolute -top-1 -right-1 h-4 min-w-4 px-0.5 rounded-full bg-destructive text-destructive-foreground text-[10px] font-bold leading-4 text-center pointer-events-none">
                {unreadCount > 99 ? "99+" : unreadCount}
              </span>
            )}
          </Button>
        );
      })}
    </div>
  );
}
