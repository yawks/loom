import { Suspense, lazy, useCallback, useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from "@/components/ui/card";
import type { core } from "../../wailsjs/go/models";
import {
  ConnectProvider,
  CreateProvider,
  GetProviderQRCode,
} from "../../wailsjs/go/main/App";
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
  onBack: () => void;
  onRefresh: () => Promise<void> | void;
}

export function ProviderConfigForm({
  provider,
  mode,
  initialValues,
  onBack,
  onRefresh,
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
    // Use instanceId from provider if available, otherwise use provider.id as fallback
    return provider.instanceId || provider.id;
  });

  const [isSaving, setIsSaving] = useState(false);
  const [saveMessage, setSaveMessage] = useState<string | null>(null);
  const [connectState, setConnectState] = useState<"idle" | "connecting" | "connected">("idle");
  const [qrCode, setQrCode] = useState("");
  const [isPollingQR, setIsPollingQR] = useState(false);
  const [pollError, setPollError] = useState<string | null>(null);

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
  }, [provider.instanceId, currentInstanceID]);

  const handleChange = (key: string, value: string) => {
    setValues((prev) => ({ ...prev, [key]: value }));
  };

  const handleSave = useCallback(async () => {
    setIsSaving(true);
    setSaveMessage(null);
    try {
      // In edit mode, use existing instanceID if available
      const existingInstanceID = mode === "edit" && provider.instanceId ? provider.instanceId : "";
      const instanceID = await CreateProvider(provider.id, values, instanceName, existingInstanceID);
      setCurrentInstanceID(instanceID); // Store the instanceID for QR code fetching
      await onRefresh();
      setSaveMessage(t("configuration_saved"));
    } catch (error) {
      console.error("Failed to save provider config:", error);
      setSaveMessage(t("configuration_save_error"));
    } finally {
      setIsSaving(false);
    }
  }, [provider.id, provider.instanceId, values, instanceName, mode, onRefresh, t]);

  const fetchQRCode = useCallback(async () => {
    try {
      // Use the current instanceID (either from provider or from creation)
      // Prefer provider.instanceId if available, otherwise use currentInstanceID
      const instanceID = provider.instanceId || currentInstanceID;
      console.log(`ProviderConfigForm.fetchQRCode: Fetching QR code for instanceID: ${instanceID} (provider.instanceId: ${provider.instanceId}, currentInstanceID: ${currentInstanceID})`);
      
      // Don't try to fetch QR code if we don't have a valid instanceID
      if (!instanceID || instanceID === provider.id) {
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
  }, [currentInstanceID, provider.id, provider.instanceId, t]);

  const handleConnect = useCallback(async () => {
    setConnectState("connecting");
    setPollError(null);
    try {
      console.log(`ProviderConfigForm.handleConnect: Creating provider with id=${provider.id}, instanceName=${instanceName}`);
      // In edit mode, use existing instanceID if available
      const existingInstanceID = mode === "edit" && provider.instanceId ? provider.instanceId : "";
      const instanceID = await CreateProvider(provider.id, values, instanceName, existingInstanceID);
      console.log(`ProviderConfigForm.handleConnect: Created provider, instanceID=${instanceID}`);
      
      // Update currentInstanceID BEFORE onRefresh to ensure it's available for any callbacks
      setCurrentInstanceID(instanceID);
      console.log(`ProviderConfigForm.handleConnect: Updated currentInstanceID to ${instanceID}`);
      
      // Use a small delay to ensure state is updated before onRefresh triggers re-render
      await new Promise(resolve => setTimeout(resolve, 0));
      
      await onRefresh();
      console.log(`ProviderConfigForm.handleConnect: Refreshed providers list`);
      
      await ConnectProvider(instanceID);
      console.log(`ProviderConfigForm.handleConnect: Connected provider ${instanceID}`);
      
      setConnectState("connected");
      setIsPollingQR(true);
      
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
  }, [provider.id, provider.instanceId, values, instanceName, mode, onRefresh, t]);

  useEffect(() => {
    if (!isPollingQR) {
      return;
    }
    const interval = window.setInterval(() => {
      fetchQRCode();
    }, 3000);
    return () => window.clearInterval(interval);
  }, [isPollingQR, fetchQRCode]);

  const hasFields = Object.keys(schema).length > 0;

  return (
    <div className="space-y-4">
      <div>
        <Button variant="ghost" onClick={onBack} className="mb-2 px-0 text-muted-foreground">
          ← {t("back")}
        </Button>
        <h2 className="text-xl font-semibold">{provider.name}</h2>
        <p className="text-sm text-muted-foreground">{provider.description}</p>
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
            {Object.entries(schema).map(([key, field]) => (
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
            {saveMessage && (
              <p className="text-sm text-muted-foreground">{saveMessage}</p>
            )}
          </CardContent>
          <CardFooter className="flex gap-2 justify-end">
            <Button onClick={handleSave} disabled={isSaving}>
              {isSaving ? t("saving") : t("save")}
            </Button>
          </CardFooter>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle>{t("connection")}</CardTitle>
          <CardDescription>
            {t("connection_description")}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <Button onClick={handleConnect} disabled={connectState === "connecting" || connectState === "connected"}>
            {connectState === "connecting" ? t("connecting") : connectState === "connected" ? t("show_qr_code") : t("show_qr_code")}
          </Button>

          {pollError && <p className="text-sm text-destructive">{pollError}</p>}

          {connectState === "connecting" && !qrCode && (
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
    </div>
  );
}

