import { useEffect, useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { GetConfiguredProviders } from "../../wailsjs/go/main/App";
import { Layers } from "lucide-react";
import { ProtocolIcon } from "./ProtocolIcon";
import { cn } from "@/lib/utils";
import type { core } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";

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
  const [configuredProviders, setConfiguredProviders] = useState<core.ProviderInfo[]>([]);
  const selectedProviderFilter = useAppStore((state) => state.selectedProviderFilter);
  const setSelectedProviderFilter = useAppStore((state) => state.setSelectedProviderFilter);

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

  return (
    <div className="flex flex-col items-center gap-2 p-2 border-r bg-muted/30">
      {/* All button */}
      <Button
        variant={selectedProviderFilter === null ? "default" : "ghost"}
        size="icon"
        className={cn(
          "h-10 w-10",
          selectedProviderFilter === null && "bg-primary text-primary-foreground"
        )}
        onClick={() => setSelectedProviderFilter(null)}
        title="All"
      >
        <Layers className="h-5 w-5" />
      </Button>

      {/* Provider buttons */}
      {configuredProviders.map((provider) => {
        const instanceId = provider.instanceId || provider.id;
        const isSelected = selectedProviderFilter === instanceId;
        const colorVariation = getColorVariation(provider);
        const displayName = provider.instanceName || provider.name;

        return (
          <Button
            key={instanceId}
            variant={isSelected ? "default" : "ghost"}
            size="icon"
            className={cn(
              "h-10 w-10 relative",
              isSelected && "bg-primary text-primary-foreground"
            )}
            onClick={() => setSelectedProviderFilter(instanceId)}
            title={displayName}
          >
            <div
              className="h-5 w-5"
              style={colorVariation || undefined}
            >
              <ProtocolIcon protocol={provider.id} size={20} />
            </div>
          </Button>
        );
      })}
    </div>
  );
}
