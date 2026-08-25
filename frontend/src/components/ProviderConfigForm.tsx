import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import {
  AutoLoginGoogleChatGaia,
  AutoLoginSlack,
  AutoLoginTeams,
  AutoPairGoogleMessages,
  CompleteGoogleMessagesLogin,
  ConnectProvider,
  CreateProvider,
  CreateProviderWithOptions,
  GetGoogleChatWebCookies,
  GetProviderQRCode,
  ResetProviderAuthentication,
  SyncProvider,
} from "../../wailsjs/go/main/App";
import { Suspense, lazy, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ToastContainer, useToast } from "@/components/ui/toast";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { RefreshCw } from "lucide-react";
import type { core } from "../../wailsjs/go/models";
import { useTranslation } from "react-i18next";

const QRCodeCanvas = lazy(() =>
  import("qrcode.react").then((module) => ({ default: module.QRCodeCanvas }))
);

type ProviderFieldSchema = {
  type?: string;
  title?: string;
  description?: string;
  default?: string;
  placeholder?: string;
};

interface ProviderConfigFormProps {
  provider: core.ProviderInfo;
  mode: "create" | "edit";
  initialValues?: Record<string, any>;
  autoConnect?: boolean; // When true and provider uses QR auth, trigger connect immediately on mount
  onBack: () => void;
  onRefresh: () => Promise<void> | void;
  onClose?: () => void; // Callback to close the modal
}

export function ProviderConfigForm({
  provider,
  mode,
  initialValues,
  autoConnect = false,
  onBack,
  onRefresh,
  onClose,
}: ProviderConfigFormProps) {
  const { t } = useTranslation();
  const schema = useMemo(() => {
    const raw = provider.configSchema ?? {};
    if (typeof raw !== "object" || raw === null) {
      return {};
    }
    const props = (raw as { properties?: Record<string, ProviderFieldSchema> }).properties;
    return props ?? {};
  }, [provider.configSchema]);

  const [values, setValues] = useState<Record<string, string>>(() => {
    const defaults: Record<string, string> = {};
    for (const [key, field] of Object.entries(schema)) {
      const initial =
        (initialValues && typeof initialValues[key] === "string"
          ? (initialValues[key] as string)
          : undefined) ??
        field.default ??
        "";
      defaults[key] = initial;
    }
    return defaults;
  });

  const [instanceName, setInstanceName] = useState<string>(() => {
    // Use instanceName from provider if available, otherwise default to empty
    return provider.instanceName || "";
  });

  const [currentInstanceID, setCurrentInstanceID] = useState<string>(() => {
    // Use instanceId from provider if available, otherwise empty string
    // Empty string will cause backend to generate a new instanceID with proper format (e.g., "slack-1")
    return provider.instanceId || "";
  });

  const [isSaving, setIsSaving] = useState(false);
	const [isRefreshing, setIsRefreshing] = useState(false);
  const { toasts, showToast, closeToast } = useToast();
  const [connectState, setConnectState] = useState<"idle" | "connecting" | "connected">("idle");
  const [qrCode, setQrCode] = useState("");
  const [pairingEmoji, setPairingEmoji] = useState("");
  const [isPollingQR, setIsPollingQR] = useState(false);
  const [pollError, setPollError] = useState<string | null>(null);
  const [webCookies, setWebCookies] = useState<Record<string, string> | null>(null);
  const autoConnectFiredRef = useRef(false);

  useEffect(() => {
    if (provider.id === "googlechat" && currentInstanceID) {
      GetGoogleChatWebCookies(currentInstanceID)
        .then((cookies) => {
          if (cookies && Object.keys(cookies).length > 0) {
            setWebCookies(cookies);
          }
        })
        .catch(() => {});
    }
  }, [provider.id, currentInstanceID]);

  useEffect(() => {
    setValues((prev) => {
      const next: Record<string, string> = { ...prev };
      for (const field of Object.keys(schema)) {
        if (!(field in next)) {
          next[field] = "";
        }
      }
      return next;
    });
  }, [schema]);

  // Update currentInstanceID when provider changes (e.g., after refresh)
  useEffect(() => {
    if (provider.instanceId && provider.instanceId !== currentInstanceID) {
      console.log(`ProviderConfigForm: Updating currentInstanceID from ${currentInstanceID} to ${provider.instanceId}`);
      setCurrentInstanceID(provider.instanceId);
    }
    // Also update if currentInstanceID is set but provider.instanceId is not (after save)
    if (currentInstanceID && !provider.instanceId && currentInstanceID.includes('-')) {
      console.log(`ProviderConfigForm: Keeping currentInstanceID ${currentInstanceID} (provider.instanceId not yet updated)`);
    }
  }, [provider.instanceId, currentInstanceID]);

  const handleChange = (key: string, value: string) => {
    setValues((prev) => ({ ...prev, [key]: value }));
  };

  // Providers with a dedicated connection flow in this form (they handle connect themselves).
  const hasOwnConnectFlow = provider.id === "whatsapp" || provider.id === "slack" || provider.id === "googlemessages" || provider.id === "teams";
  const usesQRCodeAuth = provider.id === "whatsapp";

  const handleSave = useCallback(async () => {
    setIsSaving(true);
    try {
      const filteredValues: Record<string, string> = {};
      for (const [key, value] of Object.entries(values)) {
        if (value && value.trim() !== "") {
          filteredValues[key] = value;
        }
      }
      const existingInstanceID = provider.instanceId || "";
      const instanceID = await CreateProviderWithOptions(provider.id, filteredValues, instanceName, existingInstanceID, true);
      setCurrentInstanceID(instanceID);

      if (hasOwnConnectFlow) {
        await onRefresh();
        showToast(t("configuration_saved"), "success");
      } else {
        await ConnectProvider(instanceID);
        await onRefresh();
        if (onClose) onClose();
        SyncProvider(instanceID).catch((err) => console.error("Failed to sync provider:", err));
      }
    } catch (error) {
      console.error("Failed to save provider config:", error);
      showToast(t("configuration_save_error"), "error");
    } finally {
      setIsSaving(false);
    }
  }, [provider.id, provider.instanceId, currentInstanceID, values, instanceName, hasOwnConnectFlow, onRefresh, onClose, t, showToast]);

  const fetchQRCode = useCallback(async () => {
	  // Google Messages uses account-cookie pairing and emoji confirmation, not QR.
	  // Keep this guard even though only WhatsApp renders the QR card: reauth can
	  // otherwise invoke this callback before the form has re-rendered.
	  if (!usesQRCodeAuth) {
		return;
	  }
    try {
      // Use the current instanceID (either from provider or from creation)
      // Prefer provider.instanceId if available, otherwise use currentInstanceID
      const instanceID = provider.instanceId || currentInstanceID;
      console.log(`ProviderConfigForm.fetchQRCode: Fetching QR code for instanceID: ${instanceID} (provider.instanceId: ${provider.instanceId}, currentInstanceID: ${currentInstanceID})`);

      // Don't try to fetch QR code if we don't have a valid instanceID
      // Valid instanceID should be in format "provider-number" (e.g., "whatsapp-1")
      if (!instanceID || !instanceID.includes('-')) {
        console.warn(`ProviderConfigForm.fetchQRCode: Skipping - Invalid instanceID ${instanceID}. Provider instanceId: ${provider.instanceId}`);
        return;
      }

      const code = await GetProviderQRCode(instanceID);
      console.log(`ProviderConfigForm.fetchQRCode: QR code received: ${code ? 'yes' : 'no'}`);
      setQrCode(code ?? "");
      setPollError(null);
    } catch (error) {
      console.error("ProviderConfigForm.fetchQRCode: Failed to fetch QR code:", error);
      setPollError(t("qr_code_fetch_error"));
    }
  }, [currentInstanceID, provider.instanceId, t, usesQRCodeAuth]);

  const handleConnect = useCallback(async () => {
	  // This function owns the QR-only flow. Never let a reauthentication action
	  // route Google Messages through it, even if a stale modal callback fires.
	  if (!usesQRCodeAuth) {
		setIsPollingQR(false);
		return;
	  }
    setConnectState("connecting");
    setPollError(null);
    try {
      console.log(`ProviderConfigForm.handleConnect: Creating provider with id=${provider.id}, instanceName=${instanceName}`);
      // In edit mode, use existing instanceID if available
      const existingInstanceID = mode === "edit" && provider.instanceId ? provider.instanceId : "";
      // A revocation performed on the phone does not clear whatsmeow's local
      // Store.ID. Explicit re-authentication must clear it first, otherwise
      // ConnectProvider sees an apparently authenticated client and emits no QR.
      if (existingInstanceID) {
        await ResetProviderAuthentication(existingInstanceID);
      }
      const instanceID = await CreateProvider(provider.id, values, instanceName, existingInstanceID);
      console.log(`ProviderConfigForm.handleConnect: Created provider, instanceID=${instanceID}`);

      // Update currentInstanceID BEFORE onRefresh to ensure it's available for any callbacks
      setCurrentInstanceID(instanceID);
      console.log(`ProviderConfigForm.handleConnect: Updated currentInstanceID to ${instanceID}`);

      // Use a small delay to ensure state is updated before onRefresh triggers re-render
      await new Promise(resolve => setTimeout(resolve, 0));

      // Start pairing before refreshing parent state. Refreshing selectedProvider
      // can re-render this form and interrupt the in-flight reauthentication
      // callback before ConnectProvider has created the QR channel.
      await ConnectProvider(instanceID);
      console.log(`ProviderConfigForm.handleConnect: Connected provider ${instanceID}`);

      setConnectState("connected");
      setIsPollingQR(true);

      await onRefresh();
      console.log(`ProviderConfigForm.handleConnect: Refreshed providers list`);

      // Fetch QR code directly with the new instanceID (don't use fetchQRCode from closure)
      try {
        console.log(`ProviderConfigForm.handleConnect: Fetching QR code for instanceID: ${instanceID}`);
        const code = await GetProviderQRCode(instanceID);
        console.log(`ProviderConfigForm.handleConnect: QR code received: ${code ? 'yes' : 'no'}`);
        setQrCode(code ?? "");
        setPollError(null);
      } catch (error) {
        console.error("ProviderConfigForm.handleConnect: Failed to fetch QR code:", error);
        setPollError(t("qr_code_fetch_error"));
      }
    } catch (error) {
      console.error("Failed to connect provider:", error);
      setConnectState("idle");
      setPollError(t("provider_connect_error"));
    }
  }, [provider.id, provider.instanceId, values, instanceName, mode, onRefresh, t, usesQRCodeAuth]);

  useEffect(() => {
	if (!usesQRCodeAuth) {
		setIsPollingQR(false);
		setQrCode("");
	}
  }, [usesQRCodeAuth]);

  useEffect(() => {
    if (!isPollingQR) {
      return;
    }
    const interval = window.setInterval(() => {
      fetchQRCode();
    }, 3000);
    return () => window.clearInterval(interval);
  }, [isPollingQR, fetchQRCode]);

  // Auto-trigger the connect flow when opened via "Re-authenticate" for QR-based providers.
  // Only fires once on mount (guarded by ref) to avoid re-triggering on re-renders.
  useEffect(() => {
    if (!autoConnect || !usesQRCodeAuth || autoConnectFiredRef.current || Object.keys(schema).length > 0) return;
    autoConnectFiredRef.current = true;
    handleConnect();
  }, [autoConnect, handleConnect, schema, usesQRCodeAuth]);

  const hasFields = Object.keys(schema).length > 0;

  const handleRefresh = useCallback(async () => {
    const instanceID = provider.instanceId || currentInstanceID;
    if (!instanceID) return;

    setIsRefreshing(true);
    try {
      await SyncProvider(instanceID);
      showToast(t("providers_modal_sync"), "success");
    } catch (error) {
      console.error("Failed to refresh provider:", error);
      showToast(t("providers_modal_sync_error_label"), "error");
    } finally {
      setIsRefreshing(false);
    }
  }, [currentInstanceID, provider.instanceId, showToast, t]);

  return (
    <div className="space-y-4">
      <div>
        <Button variant="ghost" onClick={onBack} className="mb-2 px-0 text-muted-foreground">
          ← {t("back")}
        </Button>
        <div className="flex items-start justify-between gap-3">
          <div>
            <h2 className="text-xl font-semibold">{provider.name}</h2>
            <p className="text-sm text-muted-foreground">{provider.description}</p>
          </div>
          {mode === "edit" && (provider.instanceId || currentInstanceID) && (
            <Button
              variant="outline"
              size="sm"
              className="shrink-0 gap-2"
              onClick={handleRefresh}
              disabled={isRefreshing}
            >
              <RefreshCw className={`h-4 w-4 ${isRefreshing ? "animate-spin" : ""}`} />
              {isRefreshing ? t("providers_modal_syncing") : t("providers_modal_sync")}
            </Button>
          )}
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t("instance_name")}</CardTitle>
          <CardDescription>
            {t("instance_name_description")}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Input
            value={instanceName}
            onChange={(event) => setInstanceName(event.target.value)}
            placeholder={t("instance_name_placeholder", { providerName: provider.name })}
          />
        </CardContent>
      </Card>

      {hasFields && (
        <Card>
          <CardHeader>
            <CardTitle>
              {mode === "edit" ? t("edit_configuration") : t("configure_provider")}
            </CardTitle>
            <CardDescription>
              {t("provider_config_description")}
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {Object.entries(schema).filter(([key]) => {
              // Slack fields handled by the dedicated Slack card below
              if (provider.id === "slack" && ["token", "d_cookie", "workspace_url"].includes(key)) return false;
              return true;
            }).map(([key, field]) => (
              <div key={key} className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">
                  {field.title ?? key}
                </label>
                <Input
                  value={values[key] ?? ""}
                  onChange={(event) => handleChange(key, event.target.value)}
                  placeholder={field.placeholder ?? field.description ?? ""}
                />
                {field.description && (
                  <p className="text-xs text-muted-foreground">{field.description}</p>
                )}
              </div>
            ))}
          </CardContent>
          <CardFooter className="flex gap-2 justify-end">
            <Button onClick={handleSave} disabled={isSaving}>
              {isSaving ? t("saving") : t("save")}
            </Button>
          </CardFooter>
        </Card>
      )}

      {provider.id === "googlechat" && (
        <Card className="mt-4">
          <CardHeader>
            <CardTitle>Messages différés (Optionnel)</CardTitle>
            <CardDescription>
              Google Chat ne propose pas de programmation d'envois via son API officielle REST. Pour autoriser les envois différés côté serveur, connectez votre session Web Google via Chrome (Gaia).
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <p className="text-xs text-muted-foreground">
              Les identifiants OAuth2 Client ID / Secret ci-dessus restent requis pour l'accès aux messages et à l'historique standard.
            </p>

            {webCookies && Object.keys(webCookies).length > 0 && (
              <div className="rounded-md border bg-muted/40 p-3 space-y-2">
                <div className="flex items-center gap-2 text-xs font-semibold text-green-600 dark:text-green-400">
                  <span>✓</span> Session Web active ({Object.keys(webCookies).length} cookies capturés)
                </div>
                <div className="space-y-1 border-t pt-2 text-xs font-mono">
                  {Object.entries(webCookies).map(([name, val]) => (
                    <div key={name} className="flex justify-between items-center text-muted-foreground">
                      <span className="font-semibold text-foreground">{name}:</span>
                      <span className="truncate max-w-[200px]" title={val}>
                        {val.length > 12 ? `${val.slice(0, 6)}...${val.slice(-6)}` : val}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            <Button
              variant="outline"
              className="w-full"
              disabled={isSaving}
              onClick={async () => {
                setIsSaving(true);
                try {
                  let instanceID = provider.instanceId || currentInstanceID;
                  if (!instanceID) {
                    const filteredValues: Record<string, string> = {};
                    for (const [key, value] of Object.entries(values)) {
                      if (value && value.trim() !== "") {
                        filteredValues[key] = value;
                      }
                    }
                    instanceID = await CreateProviderWithOptions(provider.id, filteredValues, instanceName, "", true);
                    setCurrentInstanceID(instanceID);
                  }
                  const extracted = await AutoLoginGoogleChatGaia(instanceID);
                  if (extracted) {
                    setWebCookies(extracted);
                  }
                  await onRefresh();
                  showToast("Session Web Google Chat connectée (Messages différés activés)", "success");
                } catch (error) {
                  console.error("Failed to log into Google Chat Gaia:", error);
                  showToast(String(error) || "Impossible de se connecter à la session Web Google Chat", "error");
                } finally {
                  setIsSaving(false);
                }
              }}
            >
              {isSaving ? "Connexion Web en cours…" : webCookies ? "Re-connecter la session Web Google" : "Se connecter avec Google (Activer les messages différés)"}
            </Button>
          </CardContent>
        </Card>
      )}

      {provider.id === "whatsapp" && (
      <Card>
        <CardHeader>
          <CardTitle>{t("connection")}</CardTitle>
          <CardDescription>
            {t("connection_description")}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <Button onClick={handleConnect} disabled={connectState === "connecting" || (connectState === "connected" && !!qrCode)}>
            {connectState === "connecting" ? t("connecting") : connectState === "connected" ? (qrCode ? t("connected") : t("loading_qr_code")) : t("show_qr_code")}
          </Button>

          {pollError && <p className="text-sm text-destructive">{pollError}</p>}

          {(connectState === "connecting" || (connectState === "connected" && !qrCode)) && (
            <div className="flex flex-col items-center gap-2">
              <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary"></div>
              <p className="text-sm text-muted-foreground">{t("loading_qr_code")}</p>
            </div>
          )}

          {qrCode ? (
            <div className="flex flex-col items-center gap-2">
              <div className="bg-white p-4 rounded-lg">
                <Suspense fallback={<p className="text-sm text-muted-foreground">{t("loading_qr_code")}</p>}>
                  <QRCodeCanvas value={qrCode} size={256} level="M" />
                </Suspense>
              </div>
              <p className="text-sm text-muted-foreground text-center max-w-md">
                {t("qr_code_instructions", { providerName: provider.name })}
                <br />
                <span className="text-xs text-yellow-600 dark:text-yellow-500">
                  ⚠️ {t("qr_code_expires_warning")}
                </span>
              </p>
            </div>
          ) : (
            connectState === "connected" && !isPollingQR && (
              <p className="text-sm text-muted-foreground">
                {t("waiting_for_qr_code")}
              </p>
            )
          )}
        </CardContent>
      </Card>
      )}

      {provider.id === "googlemessages" && (
      <Card>
        <CardHeader>
          <CardTitle>Connexion Google</CardTitle>
          <CardDescription>
            Loom ouvre un navigateur Chrome, saisit vos identifiants et récupère automatiquement les cookies de session. Si la validation en deux étapes est activée, complétez-la dans la fenêtre Chrome qui apparaît.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {!pairingEmoji ? (
            <Button
              disabled={isSaving}
              onClick={async () => {
                setIsSaving(true);
                try {
                  let instanceID = provider.instanceId || currentInstanceID;
                  if (!instanceID) {
                    instanceID = await CreateProviderWithOptions(provider.id, {}, instanceName, "", true);
                    setCurrentInstanceID(instanceID);
                  }
                  const emoji = await AutoPairGoogleMessages(instanceID);
                  setPairingEmoji(emoji);
                } catch (error) {
                  console.error("Failed to start Google Messages pairing:", error);
                  showToast(String(error) || "Impossible de démarrer l’appairage Google Messages", "error");
                } finally {
                  setIsSaving(false);
                }
              }}
            >
              {isSaving ? "Connexion en cours…" : "Se connecter avec Google"}
            </Button>
          ) : (
            <div className="space-y-3">
              <p className="text-center text-5xl" aria-label="Pairing emoji">{pairingEmoji}</p>
              <div className="space-y-2 text-sm text-muted-foreground">
                <ol className="list-decimal space-y-1 pl-5">
                  <li>Déverrouillez le téléphone Android et ouvrez Google Messages.</li>
                  <li>Dans la demande d’appairage de compte Google, sélectionnez <strong className="text-foreground">{pairingEmoji}</strong>.</li>
                  <li>Revenez ici puis cliquez sur « J’ai confirmé l’emoji ».</li>
                </ol>
                <p>Si aucune demande n’apparaît, laissez Google Messages ouvert quelques secondes puis recommencez l’appairage.</p>
              </div>
              <Button
                disabled={isSaving}
                onClick={async () => {
                  setIsSaving(true);
                  try {
                    const instanceID = provider.instanceId || currentInstanceID;
                    await CompleteGoogleMessagesLogin(instanceID);
                    setPairingEmoji("");
                    await onRefresh();
                    SyncProvider(instanceID).catch((error) => console.error("Failed to sync Google Messages:", error));
                    if (onClose) onClose();
                  } catch (error) {
                    console.error("Failed to complete Google Messages pairing:", error);
                    showToast("Google Messages pairing was not completed", "error");
                  } finally {
                    setIsSaving(false);
                  }
                }}
              >J’ai confirmé l’emoji</Button>
            </div>
          )}
        </CardContent>
      </Card>
      )}

      {provider.id === "teams" && (
      <Card>
        <CardHeader>
          <CardTitle>Connexion Microsoft Teams</CardTitle>
          <CardDescription>
            Loom ouvre Chrome sur la page Microsoft officielle. Microsoft gère votre mot de passe, la MFA et les règles de sécurité de votre entreprise ; Loom ne lit jamais les cookies du navigateur.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Utilisez votre compte professionnel ou scolaire. Si votre organisation impose un appareil conforme, Microsoft peut refuser la connexion.
          </p>
          <Button
            className="w-full"
            disabled={isSaving}
            onClick={async () => {
              setIsSaving(true);
              try {
                let instanceID = provider.instanceId || currentInstanceID;
                if (!instanceID) {
                  const config = values.tenant?.trim() ? { tenant: values.tenant.trim() } : {};
                  instanceID = await CreateProviderWithOptions(provider.id, config, instanceName, "", true);
                  setCurrentInstanceID(instanceID);
                }
                await AutoLoginTeams(instanceID, values.tenant?.trim() || "");
                await onRefresh();
                SyncProvider(instanceID).catch((error) => console.error("Failed to sync Microsoft Teams:", error));
                showToast("Microsoft Teams connecté", "success");
                if (onClose) onClose();
              } catch (error) {
                console.error("Failed to log into Microsoft Teams:", error);
                showToast(String(error) || "Impossible de se connecter à Microsoft Teams", "error");
              } finally {
                setIsSaving(false);
              }
            }}
          >
            {isSaving ? "Connexion Microsoft en cours…" : "Se connecter avec Microsoft"}
          </Button>
        </CardContent>
      </Card>
      )}

      {provider.id === "slack" && (
        <Card>
          <CardHeader>
            <CardTitle>{t("slack_browser_login_title")}</CardTitle>
            <CardDescription>{t("slack_browser_login_description")}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <label className="text-sm font-medium">{t("slack_workspace_url_label")}</label>
              <Input
                placeholder={t("slack_workspace_url_placeholder")}
                value={values.workspace_url ?? ""}
                onChange={(e) => handleChange("workspace_url", e.target.value)}
                disabled={isSaving}
              />
              <p className="text-xs text-muted-foreground">{t("slack_workspace_url_hint")}</p>
            </div>
            <Button
              className="w-full"
              disabled={isSaving}
              onClick={async () => {
                setIsSaving(true);
                try {
                  let instanceID = provider.instanceId || currentInstanceID;
                  if (!instanceID) {
                    instanceID = await CreateProviderWithOptions(provider.id, {}, instanceName, "", true);
                    setCurrentInstanceID(instanceID);
                  }
                  await AutoLoginSlack(instanceID, values.workspace_url?.trim() ?? "");
                  await onRefresh();
                  SyncProvider(instanceID).catch((error) => console.error("Failed to sync Slack:", error));
                  showToast(t("slack_connected"), "success");
                  if (onClose) onClose();
                } catch (error) {
                  console.error("Failed to log into Slack:", error);
                  showToast(String(error) || t("slack_connect_error"), "error");
                } finally {
                  setIsSaving(false);
                }
              }}
            >
              {isSaving ? t("slack_browser_login_in_progress") : t("slack_browser_login_button")}
            </Button>

            <details className="group">
              <summary className="cursor-pointer select-none text-sm text-muted-foreground hover:text-foreground">
                {t("slack_advanced_title")}
              </summary>
              <div className="mt-3 space-y-4 border-t pt-3">
                <p className="text-xs text-muted-foreground" dangerouslySetInnerHTML={{ __html: t("slack_advanced_description") }} />

                <details className="group/tuto">
                  <summary className="cursor-pointer select-none text-xs font-medium text-muted-foreground hover:text-foreground">
                    {t("slack_xoxp_tutorial_title")}
                  </summary>
                  <ol className="mt-2 space-y-2 pl-4 text-xs text-muted-foreground list-decimal">
                    <li dangerouslySetInnerHTML={{ __html: t("slack_xoxp_tutorial_step1").replace("<a>", '<a href="https://api.slack.com/apps" target="_blank" class="underline text-foreground">').replace("</a>", "</a>") }} />
                    <li dangerouslySetInnerHTML={{ __html: t("slack_xoxp_tutorial_step2") }} />
                    <li dangerouslySetInnerHTML={{ __html: t("slack_xoxp_tutorial_step3") }} />
                    <li dangerouslySetInnerHTML={{ __html: t("slack_xoxp_tutorial_step4") }} />
                  </ol>
                </details>

                {["token", "d_cookie"].map((key) => {
                  const field = schema[key];
                  if (!field) return null;
                  // d_cookie is only needed for xoxc tokens
                  if (key === "d_cookie" && !values.token?.startsWith("xoxc-")) return null;
                  return (
                    <div key={key} className="space-y-1">
                      <label className="text-sm font-medium">{field.title ?? key}</label>
                      <Input
                        placeholder={field.description}
                        value={values[key] ?? ""}
                        onChange={(e) => handleChange(key, e.target.value)}
                        disabled={isSaving}
                      />
                      {key === "d_cookie" && (
                        <p className="text-xs text-muted-foreground" dangerouslySetInnerHTML={{ __html: t("slack_d_cookie_hint") }} />
                      )}
                    </div>
                  );
                })}
                <Button
                  variant="outline"
                  className="w-full"
                  disabled={isSaving}
                  onClick={async () => {
                    setIsSaving(true);
                    try {
                      const filteredValues: Record<string, string> = {};
                      for (const [key, value] of Object.entries(values)) {
                        if (value && value.trim() !== "" && key !== "workspace_url") {
                          filteredValues[key] = value;
                        }
                      }
                      const existingInstanceID = provider.instanceId || currentInstanceID || "";
                      const instanceID = await CreateProvider(provider.id, filteredValues, instanceName, existingInstanceID);
                      setCurrentInstanceID(instanceID);
                      await ConnectProvider(instanceID);
                      await onRefresh();
                      if (onClose) onClose();
                      SyncProvider(instanceID).catch((error) => console.error("Failed to sync provider:", error));
                    } catch (error) {
                      console.error("Failed to connect with token:", error);
                      showToast(t("configuration_save_error"), "error");
                      setIsSaving(false);
                    }
                  }}
                >
                  {isSaving ? t("connecting") : t("slack_advanced_connect_button")}
                </Button>
              </div>
            </details>
          </CardContent>
        </Card>
      )}
      <ToastContainer toasts={toasts} onClose={closeToast} />
    </div>
  );
}
