import { useCallback, useEffect, useMemo, useState } from "react";
import { Settings, Trash2, RefreshCw, Wine, MessageCircle } from "lucide-react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog";
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
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { ProviderConfigForm } from "@/components/ProviderConfigForm";
import type { core } from "../../wailsjs/go/models";
import {
  GetAvailableProviders,
  GetConfiguredProviders,
  RemoveProvider,
  SyncProvider,
} from "../../wailsjs/go/main/App";
import { EventsOn } from "../../wailsjs/runtime/runtime";

interface ProvidersModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

type ViewState = "list" | "config";

interface SyncStatusPayload {
  status: "fetching_contacts" | "fetching_history" | "completed" | "error" | null;
  message: string;
}

export function ProvidersModal({ open, onOpenChange }: ProvidersModalProps) {
  const [view, setView] = useState<ViewState>("list");
  const [availableProviders, setAvailableProviders] = useState<core.ProviderInfo[]>([]);
  const [configuredProviders, setConfiguredProviders] = useState<core.ProviderInfo[]>([]);
  const [selectedProvider, setSelectedProvider] = useState<core.ProviderInfo | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [isRemoving, setIsRemoving] = useState<string | null>(null);
  const [isSyncing, setIsSyncing] = useState<string | null>(null);
  const [providerToDelete, setProviderToDelete] = useState<string | null>(null);

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
    } catch (err) {
      console.error("Failed to load providers:", err);
      setError("Failed to load providers. Please try again.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (open) {
      refreshProviders();
      setView("list");
      setSelectedProvider(null);
    }
  }, [open, refreshProviders]);

  useEffect(() => {
    if (!open || view !== "config" || !selectedProvider) {
      return;
    }

    const unsubscribe = EventsOn("sync-status", (payload: string) => {
      try {
        const status: SyncStatusPayload = JSON.parse(payload);
        if (status.status === "completed" && selectedProvider.id === "whatsapp") {
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
    setSelectedProvider(provider);
    setView("config");
  };

  const handleRemoveClick = (providerID: string) => {
    setProviderToDelete(providerID);
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
      setError("Failed to remove provider. Please try again.");
    } finally {
      setIsRemoving(null);
    }
  };

  const handleRemoveCancel = () => {
    setProviderToDelete(null);
  };

  const configuredIds = useMemo(() => new Set(configuredProviders.map((p) => p.id)), [configuredProviders]);

  // Get provider icon component
  const getProviderIcon = (providerId: string) => {
    switch (providerId) {
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
  };

  const providerToDeleteName = providerToDelete 
    ? configuredProviders.find(p => p.id === providerToDelete)?.name || providerToDelete
    : "";

  return (
    <>
      <AlertDialog open={providerToDelete !== null} onOpenChange={(open) => !open && handleRemoveCancel()}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete Provider</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to delete the provider "{providerToDeleteName}"? This will permanently delete all associated conversations and messages. This action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={handleRemoveCancel}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleRemoveConfirm}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        {view === "list" && (
          <div className="space-y-6 max-h-[80vh] overflow-y-auto pr-2">
            <DialogHeader>
              <DialogTitle>Providers</DialogTitle>
              <DialogDescription>
                Manage configured providers and add new ones to connect your platforms.
              </DialogDescription>
            </DialogHeader>

            {error && <p className="text-sm text-destructive">{error}</p>}

            <section className="space-y-3">
              <div>
                <h3 className="text-sm font-semibold text-muted-foreground">Configured Providers</h3>
              </div>
              {configuredProviders.length === 0 && !loading ? (
                <p className="text-sm text-muted-foreground">No providers configured yet.</p>
              ) : (
                <div className="space-y-3">
                  {configuredProviders.map((provider) => (
                    <Card key={provider.id}>
                      <CardHeader className="flex flex-row items-center justify-between space-y-0">
                        <div className="flex items-center gap-3">
                          {getProviderIcon(provider.id)}
                          <div>
                            <CardTitle>{provider.name}</CardTitle>
                            <CardDescription>{provider.description}</CardDescription>
                          </div>
                        </div>
                        {provider.isActive && (
                          <span className="text-xs font-medium text-green-600">Active</span>
                        )}
                      </CardHeader>
                      <CardContent className="flex gap-2">
                        <Button variant="outline" className="flex items-center gap-2" onClick={() => handleEdit(provider)}>
                          <Settings className="h-4 w-4" />
                          Edit
                        </Button>
                        <Button
                          variant="outline"
                          className="flex items-center gap-2"
                          onClick={async () => {
                            setIsSyncing(provider.id);
                            try {
                              await SyncProvider(provider.id);
                              await refreshProviders();
                            } catch (err) {
                              console.error("Failed to sync provider:", err);
                              setError("Failed to sync provider. Please try again.");
                            } finally {
                              setIsSyncing(null);
                            }
                          }}
                          disabled={isSyncing === provider.id}
                        >
                          <RefreshCw className={`h-4 w-4 ${isSyncing === provider.id ? "animate-spin" : ""}`} />
                          {isSyncing === provider.id ? "Syncing..." : "Sync"}
                        </Button>
                        <Button
                          variant="ghost"
                          className="text-destructive flex items-center gap-2"
                          onClick={() => handleRemoveClick(provider.id)}
                          disabled={isRemoving === provider.id}
                        >
                          <Trash2 className="h-4 w-4" />
                          {isRemoving === provider.id ? "Removing..." : "Remove"}
                        </Button>
                      </CardContent>
                    </Card>
                  ))}
                </div>
              )}
            </section>

            <section className="space-y-3">
              <div>
                <h3 className="text-sm font-semibold text-muted-foreground">Available Providers</h3>
                <p className="text-xs text-muted-foreground">
                  Click on a provider to configure it.
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
                        {getProviderIcon(provider.id)}
                        <span className="flex-1">{provider.name}</span>
                        {configuredIds.has(provider.id) && (
                          <span className="text-xs text-muted-foreground">Configured</span>
                        )}
                      </CardTitle>
                      <CardDescription>{provider.description}</CardDescription>
                    </CardHeader>
                  </Card>
                ))}
              </div>
            </section>
          </div>
        )}

        {view === "config" && selectedProvider && (
          <ProviderConfigForm
            provider={selectedProvider}
            mode={configuredIds.has(selectedProvider.id) ? "edit" : "create"}
            initialValues={selectedProvider.config}
            onBack={() => {
              setView("list");
              setSelectedProvider(null);
            }}
            onRefresh={async () => {
              await refreshProviders();
            }}
          />
        )}
      </DialogContent>
    </Dialog>
    </>
  );
}

