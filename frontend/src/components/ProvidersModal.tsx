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
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  GetAvailableProviders,
  GetConfiguredProviders,
  RemoveProvider,
} from "../../wailsjs/go/main/App";
import { MessageCircle, Settings, Trash2, Wine } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { ProviderConfigForm } from "@/components/ProviderConfigForm";
import type { core } from "../../wailsjs/go/models";
import { useTranslation } from "react-i18next";

interface ProvidersModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

type ViewState = "list" | "config";

interface SyncStatusPayload {
  status: "fetching_contacts" | "fetching_history" | "fetching_avatars" | "completed" | "error" | null;
  message: string;
}

export function ProvidersModal({ open, onOpenChange }: ProvidersModalProps) {
  const { t } = useTranslation();
  const [view, setView] = useState<ViewState>("list");
  const [availableProviders, setAvailableProviders] = useState<core.ProviderInfo[]>([]);
  const [configuredProviders, setConfiguredProviders] = useState<core.ProviderInfo[]>([]);
  const [selectedProvider, setSelectedProvider] = useState<core.ProviderInfo | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [isRemoving, setIsRemoving] = useState<string | null>(null);
  const [providerToDelete, setProviderToDelete] = useState<string | null>(null);
  const prevOpenRef = useRef(false);

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

  const handleEdit = (provider: core.ProviderInfo) => {
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
      // Refresh providers list
      await refreshProviders();
      // The contacts will be refreshed automatically via the contacts-refresh event
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

  // Get provider icon component
  const getProviderIcon = (provider: core.ProviderInfo) => {
    const colorVariation = getColorVariation(provider);
    const iconContent = (() => {
      switch (provider.id) {
        case "mock":
          return <Wine className="h-5 w-5" />;
        case "whatsapp":
          return (
            <svg className="h-5 w-5" viewBox="0 0 24 24" fill="currentColor">
              <path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413Z"/>
            </svg>
          );
        default:
          return <MessageCircle className="h-5 w-5" />;
      }
    })();

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

      <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl max-h-[90vh] flex flex-col">
        {view === "list" && (
          <>
            <DialogHeader className="flex-shrink-0">
              <DialogTitle>{t("providers_modal_title")}</DialogTitle>
              <DialogDescription>
                {t("providers_modal_description")}
              </DialogDescription>
            </DialogHeader>
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
                    <Card key={provider.instanceId || provider.id}>
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
                        {provider.isActive && (
                          <span className="text-xs font-medium text-green-600">{t("providers_modal_active")}</span>
                        )}
                      </CardHeader>
                      <CardContent className="flex gap-2">
                        <Button variant="outline" className="flex items-center gap-2" onClick={() => handleEdit(provider)}>
                          <Settings className="h-4 w-4" />
                          {t("providers_modal_edit")}
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
                        {(() => {
                          switch (provider.id) {
                            case "mock":
                              return <Wine className="h-5 w-5" />;
                            case "whatsapp":
                              return (
                                <svg className="h-5 w-5" viewBox="0 0 24 24" fill="currentColor">
                                  <path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413Z"/>
                                </svg>
                              );
                            default:
                              return <MessageCircle className="h-5 w-5" />;
                          }
                        })()}
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
              onBack={() => {
                console.log("ProvidersModal: onBack called, returning to list view");
                setView("list");
                setSelectedProvider(null);
              }}
              onRefresh={async () => {
                await refreshProviders();
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
      </DialogContent>
    </Dialog>
    </>
  );
}

