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
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  GetAvailableProviders,
  GetConfiguredProviders,
  RemoveProvider,
  SyncProvider,
} from "../../wailsjs/go/main/App";
import { AlertTriangle, RefreshCw, Settings, Trash2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { ProviderConfigForm } from "@/components/ProviderConfigForm";
import { ProtocolIcon } from "@/components/ProtocolIcon";
import type { core } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useTranslation } from "react-i18next";

interface ProviderSettingsProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

type ViewState = "list" | "config";

interface SyncStatusPayload {
  status: "fetching_contacts" | "fetching_history" | "fetching_avatars" | "completed" | "error" | "needs_reauth" | null;
  message: string;
}

export function ProviderSettings({ open, onOpenChange }: ProviderSettingsProps) {
  const { t } = useTranslation();
  const [view, setView] = useState<ViewState>("list");
  const [availableProviders, setAvailableProviders] = useState<core.ProviderInfo[]>([]);
  const [configuredProviders, setConfiguredProviders] = useState<core.ProviderInfo[]>([]);
  const [selectedProvider, setSelectedProvider] = useState<core.ProviderInfo | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [isRemoving, setIsRemoving] = useState<string | null>(null);
  const [providerToDelete, setProviderToDelete] = useState<string | null>(null);
  const [syncingInstances, setSyncingInstances] = useState<Set<string>>(new Set());
  const prevOpenRef = useRef(false);
  const selectedContact = useAppStore((state) => state.selectedContact);
  const setSelectedContact = useAppStore((state) => state.setSelectedContact);
  const removeCapabilities = useAppStore((state) => state.removeCapabilities);

  const refreshProviders = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [available, configured] = await Promise.all([
        GetAvailableProviders(),
        GetConfiguredProviders(),
      ]);
      setAvailableProviders(available);
      setConfiguredProviders(configured);

      // Update selectedProvider if it exists to get the latest instanceId
      // Use a ref or state to avoid dependency issues
      setSelectedProvider(current => {
        if (!current) return current;
        const updatedProvider = configured.find(p =>
          (p.instanceId || p.id) === (current.instanceId || current.id)
        );
        return updatedProvider || current;
      });
    } catch (err) {
      console.error("Failed to load providers:", err);
      setError(t("providers_modal_load_error"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    const wasOpen = prevOpenRef.current;
    prevOpenRef.current = open;

    if (open && !wasOpen) {
      // Modal just opened - reset to list view
      console.log("ProvidersModal: modal opened, refreshing providers");
      refreshProviders();
      setView("list");
      setSelectedProvider(null);
    } else if (!open && wasOpen) {
      // Modal just closed - reset state
      console.log("ProvidersModal: modal closed");
      setView("list");
      setSelectedProvider(null);
    } else if (open) {
      // Modal is already open - just refresh providers without resetting view
      console.log("ProvidersModal: modal already open, refreshing providers");
      refreshProviders();
    }
  }, [open, refreshProviders]);

  useEffect(() => {
    console.log("ProvidersModal: view changed to", view, "selectedProvider:", selectedProvider?.id);
  }, [view, selectedProvider]);

  // Track syncing state per provider instance (for all instances, not just the selected one)
  useEffect(() => {
    if (!open) return;

    const unsubscribe = EventsOn("sync-status", (payload: string) => {
      try {
        const raw = JSON.parse(payload);
        const instanceId: string = raw.InstanceID || raw.instanceId || "";
        const status: string = (raw.Status || raw.status || "").toLowerCase();

        if (!instanceId) return;

        if (status === "fetching_contacts" || status === "fetching_history" || status === "fetching_avatars") {
          setSyncingInstances((prev) => new Set([...prev, instanceId]));
        } else if (status === "completed" || status === "error" || status === "needs_reauth") {
          setSyncingInstances((prev) => {
            const next = new Set(prev);
            next.delete(instanceId);
            return next;
          });
          if (status === "needs_reauth") {
            // Refresh immediately so the orange badge appears without requiring
            // the user to close and reopen the modal.
            refreshProviders();
          }
        }
      } catch {
        // ignore
      }
    });

    return () => {
      if (unsubscribe) unsubscribe();
    };
  }, [open, refreshProviders]);

  useEffect(() => {
    if (!open || view !== "config" || !selectedProvider) {
      return;
    }

    const unsubscribe = EventsOn("sync-status", (payload: string) => {
      try {
        const status: SyncStatusPayload = JSON.parse(payload);
        // Close modal when WhatsApp QR code is scanned and synchronization starts
        // "fetching_contacts" indicates the QR code was scanned and sync is beginning
        if (status.status === "fetching_contacts" && selectedProvider.id === "whatsapp") {
          console.log("ProvidersModal: WhatsApp QR code scanned, synchronization starting, closing modal");
          onOpenChange(false);
          setView("list");
          setSelectedProvider(null);
        }
        // Also close on completed status as fallback (in case fetching_contacts was missed)
        else if (status.status === "completed" && selectedProvider.id === "whatsapp") {
          console.log("ProvidersModal: WhatsApp sync completed, closing modal");
          onOpenChange(false);
          setView("list");
          setSelectedProvider(null);
        }
      } catch (error) {
        console.error("Failed to parse sync status payload:", error);
      }
    });

    return () => {
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, [open, view, selectedProvider, onOpenChange]);

  const [isReauth, setIsReauth] = useState(false);

  const handleEdit = (provider: core.ProviderInfo) => {
    setIsReauth(false);
    setSelectedProvider(provider);
    setView("config");
  };

  const handleReauth = (provider: core.ProviderInfo) => {
    setIsReauth(true);
    setSelectedProvider(provider);
    setView("config");
  };

  const handleAddNew = (provider: core.ProviderInfo) => {
    console.log("ProvidersModal.handleAddNew: clicked on provider", provider.id, provider.name);
    setSelectedProvider(provider);
    setView("config");
    console.log("ProvidersModal.handleAddNew: set view to config, selectedProvider:", provider);
  };

  const handleRemoveClick = (provider: core.ProviderInfo) => {
    // Use instanceId if available, otherwise fall back to id (for backward compatibility)
    const instanceID = provider.instanceId || provider.id;
    setProviderToDelete(instanceID);
  };

  const handleRemoveConfirm = async () => {
    if (!providerToDelete) return;

    setIsRemoving(providerToDelete);
    try {
      await RemoveProvider(providerToDelete);
      // Clear selected contact if it belongs to the removed provider
      if (selectedContact?.linkedAccounts?.some((a) => a.providerInstanceId === providerToDelete)) {
        setSelectedContact(null);
      }
      removeCapabilities(providerToDelete);
      await refreshProviders();
      setProviderToDelete(null);
    } catch (err) {
      console.error("Failed to remove provider:", err);
      setError(t("providers_modal_remove_error"));
    } finally {
      setIsRemoving(null);
    }
  };

  const handleRemoveCancel = () => {
    setProviderToDelete(null);
  };

  const handleSync = async (provider: core.ProviderInfo) => {
    const instanceId = provider.instanceId || provider.id;
    try {
      await SyncProvider(instanceId);
    } catch (err) {
      console.error("Failed to sync provider:", err);
    }
  };

  const configuredIds = useMemo(() => new Set(configuredProviders.map((p) => p.id)), [configuredProviders]);

  // Color variations for multiple instances of the same provider
  const COLOR_VARIATIONS = [
    { filter: "hue-rotate(0deg)" },
    { filter: "hue-rotate(60deg)" },
    { filter: "hue-rotate(120deg)" },
    { filter: "hue-rotate(180deg)" },
    { filter: "hue-rotate(240deg)" },
    { filter: "hue-rotate(300deg)" },
  ];

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

  // Keep all provider branding in one component so official colors stay
  // consistent everywhere in the application.
  const getProviderIcon = (provider: core.ProviderInfo) => {
    const colorVariation = getColorVariation(provider);
    const iconContent = (
      <ProtocolIcon protocol={provider.id} className="h-5 w-5" size={20} />
    );

    if (colorVariation) {
      return <div style={colorVariation}>{iconContent}</div>;
    }
    return iconContent;
  };

  const providerToDeleteName = providerToDelete
    ? configuredProviders.find(p => (p.instanceId || p.id) === providerToDelete)?.instanceName ||
      configuredProviders.find(p => (p.instanceId || p.id) === providerToDelete)?.name ||
      providerToDelete
    : "";

  return (
    <>
      <AlertDialog open={providerToDelete !== null} onOpenChange={(open) => !open && handleRemoveCancel()}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("providers_modal_delete_title")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("providers_modal_delete_description", { name: providerToDeleteName })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={handleRemoveCancel}>{t("providers_modal_cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleRemoveConfirm}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {t("providers_modal_delete_button")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <div className="flex min-h-0 flex-1 flex-col">
        {view === "list" && (
          <>
            <div className="mb-4 flex-shrink-0 space-y-1">
              <h2 className="text-lg font-semibold">{t("providers_modal_title")}</h2>
              <p className="text-sm text-muted-foreground">
                {t("providers_modal_description")}
              </p>
            </div>
            <div className="space-y-6 overflow-y-auto pr-2 flex-1 min-h-0">

            {error && <p className="text-sm text-destructive">{error}</p>}

            <section className="space-y-3">
              <div>
                <h3 className="text-sm font-semibold text-muted-foreground">{t("providers_modal_configured_title")}</h3>
              </div>
              {configuredProviders.length === 0 && !loading ? (
                <p className="text-sm text-muted-foreground">{t("providers_modal_no_configured")}</p>
              ) : (
                <div className="space-y-3">
                  {configuredProviders.map((provider) => (
                    <Card key={provider.instanceId || provider.id} className={provider.syncError ? "border-orange-500/60" : ""}>
                      <CardHeader className="flex flex-row items-center justify-between space-y-0">
                        <div className="flex items-center gap-3">
                          {getProviderIcon(provider)}
                          <div>
                            <CardTitle>
                              {provider.instanceName || provider.name}
                              {provider.instanceName && provider.instanceName !== provider.name && (
                                <span className="text-sm font-normal text-muted-foreground ml-2">
                                  ({provider.name})
                                </span>
                              )}
                            </CardTitle>
                            <CardDescription>{provider.description}</CardDescription>
                          </div>
                        </div>
                        {provider.isActive && !provider.syncError && (
                          <span className="text-xs font-medium text-green-600">{t("providers_modal_active")}</span>
                        )}
                        {provider.syncError && (
                          <span className="text-xs font-medium text-orange-500 flex items-center gap-1">
                            <AlertTriangle className="h-3.5 w-3.5" />
                            {t("providers_modal_sync_error_label")}
                          </span>
                        )}
                      </CardHeader>
                      {provider.syncError && (
                        <div className="providers-modal__error-banner mx-6 mb-3 flex items-start gap-2 rounded-md bg-orange-500/10 border border-orange-500/30 px-3 py-2 text-sm text-orange-700 dark:text-orange-400">
                          <AlertTriangle className="h-4 w-4 shrink-0 mt-0.5" />
                          <span>{provider.syncError}</span>
                        </div>
                      )}
                      <CardContent className="flex gap-2 flex-wrap">
                        {provider.syncError && (
                          <Button
                            variant="default"
                            className="providers-modal__reauth-button flex items-center gap-2 bg-orange-500 hover:bg-orange-600 text-white"
                            onClick={() => handleReauth(provider)}
                          >
                            <RefreshCw className="h-4 w-4" />
                            {t("providers_modal_reauth")}
                          </Button>
                        )}
                        <Button variant="outline" className="flex items-center gap-2" onClick={() => handleEdit(provider)}>
                          <Settings className="h-4 w-4" />
                          {t("providers_modal_edit")}
                        </Button>
                        <Button
                          variant="outline"
                          className="flex items-center gap-2"
                          onClick={() => handleSync(provider)}
                          disabled={syncingInstances.has(provider.instanceId || provider.id)}
                        >
                          <RefreshCw className={`h-4 w-4 ${syncingInstances.has(provider.instanceId || provider.id) ? "animate-spin" : ""}`} />
                          {syncingInstances.has(provider.instanceId || provider.id) ? t("providers_modal_syncing") : t("providers_modal_sync")}
                        </Button>
                        <Button
                          variant="ghost"
                          className="text-destructive flex items-center gap-2"
                          onClick={() => handleRemoveClick(provider)}
                          disabled={isRemoving === (provider.instanceId || provider.id)}
                        >
                          <Trash2 className="h-4 w-4" />
                          {isRemoving === (provider.instanceId || provider.id) ? t("providers_modal_removing") : t("providers_modal_remove")}
                        </Button>
                      </CardContent>
                    </Card>
                  ))}
                </div>
              )}
            </section>

            <section className="space-y-3">
              <div>
                <h3 className="text-sm font-semibold text-muted-foreground">{t("providers_modal_available_title")}</h3>
                <p className="text-xs text-muted-foreground">
                  {t("providers_modal_available_description")}
                </p>
              </div>
              <div className="grid gap-3 md:grid-cols-2">
                {availableProviders.map((provider) => (
                  <Card
                    key={provider.id}
                    className="cursor-pointer transition hover:border-primary"
                    onClick={() => handleAddNew(provider)}
                  >
                    <CardHeader>
                      <CardTitle className="flex items-center gap-3">
                        <ProtocolIcon protocol={provider.id} className="h-5 w-5" size={20} />
                        <span className="flex-1">{provider.name}</span>
                        {configuredIds.has(provider.id) && (
                          <span className="text-xs text-muted-foreground">{t("providers_modal_configured_badge")}</span>
                        )}
                      </CardTitle>
                      <CardDescription>{provider.description}</CardDescription>
                    </CardHeader>
                  </Card>
                ))}
              </div>
            </section>
            </div>
          </>
        )}

        {view === "config" && selectedProvider && (
          <div className="overflow-y-auto flex-1 min-h-0 pr-2">
            <ProviderConfigForm
              provider={selectedProvider}
              mode={configuredIds.has(selectedProvider.id) ? "edit" : "create"}
              initialValues={selectedProvider.config}
              autoConnect={isReauth}
              onBack={() => {
                console.log("ProvidersModal: onBack called, returning to list view");
                setIsReauth(false);
                setView("list");
                setSelectedProvider(null);
              }}
              onRefresh={async () => {
                await refreshProviders();
              }}
              onClose={() => {
                console.log("ProvidersModal: closing modal from ProviderConfigForm");
                onOpenChange(false);
                setIsReauth(false);
                setView("list");
                setSelectedProvider(null);
              }}
            />
          </div>
        )}
        {view === "config" && !selectedProvider && (
          <div className="p-4">
            <p className="text-muted-foreground">No provider selected</p>
            <Button onClick={() => {
              console.log("ProvidersModal: returning to list view");
              setView("list");
            }}>Back</Button>
          </div>
        )}
      </div>
    </>
  );
}
