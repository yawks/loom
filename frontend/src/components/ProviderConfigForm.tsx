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

  const handleChange = (key: string, value: string) => {
    setValues((prev) => ({ ...prev, [key]: value }));
  };

  const handleSave = useCallback(async () => {
    setIsSaving(true);
    setSaveMessage(null);
    try {
      await CreateProvider(provider.id, values);
      await onRefresh();
      setSaveMessage("Configuration saved successfully.");
    } catch (error) {
      console.error("Failed to save provider config:", error);
      setSaveMessage("Unable to save the configuration. Please try again.");
    } finally {
      setIsSaving(false);
    }
  }, [provider.id, values, onRefresh]);

  const fetchQRCode = useCallback(async () => {
    try {
      const code = await GetProviderQRCode(provider.id);
      setQrCode(code ?? "");
      setPollError(null);
    } catch (error) {
      console.error("Failed to fetch QR code:", error);
      setPollError("Unable to fetch the QR code right now.");
    }
  }, [provider.id]);

  const handleConnect = useCallback(async () => {
    setConnectState("connecting");
    setPollError(null);
    try {
      await CreateProvider(provider.id, values);
      await onRefresh();
      await ConnectProvider(provider.id);
      setConnectState("connected");
      setIsPollingQR(true);
      await fetchQRCode();
    } catch (error) {
      console.error("Failed to connect provider:", error);
      setConnectState("idle");
      setPollError("Unable to connect. Ensure the provider is configured properly.");
    }
  }, [provider.id, values, onRefresh, fetchQRCode]);

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
          ← Back
        </Button>
        <h2 className="text-xl font-semibold">{provider.name}</h2>
        <p className="text-sm text-muted-foreground">{provider.description}</p>
      </div>

      {hasFields ? (
        <Card>
          <CardHeader>
            <CardTitle>
              {mode === "edit" ? "Edit configuration" : "Configure provider"}
            </CardTitle>
            <CardDescription>
              Provide the required information to initialize this provider.
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
              {isSaving ? "Saving..." : "Save"}
            </Button>
          </CardFooter>
        </Card>
      ) : (
        <Card>
          <CardHeader>
            <CardTitle>No configuration required</CardTitle>
            <CardDescription>This provider does not require additional parameters.</CardDescription>
          </CardHeader>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Connection</CardTitle>
          <CardDescription>
            Connect to start synchronization and display the QR code if required.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <Button onClick={handleConnect} disabled={connectState === "connecting"}>
            {connectState === "connecting" ? "Connecting..." : "Connect"}
          </Button>

          {pollError && <p className="text-sm text-destructive">{pollError}</p>}

          {qrCode ? (
            <div className="flex flex-col items-center gap-2">
              <div className="bg-white p-4 rounded-lg">
                <Suspense fallback={<p className="text-sm text-muted-foreground">Loading QR code…</p>}>
                  <QRCodeCanvas value={qrCode} size={256} level="M" />
                </Suspense>
              </div>
              <p className="text-sm text-muted-foreground text-center max-w-md">
                Scan this QR code with the {provider.name} app on your phone to authorize the connection.
                <br />
                <span className="text-xs text-yellow-600 dark:text-yellow-500">
                  ⚠️ The QR code expires in ~30 seconds. Scan quickly!
                </span>
              </p>
            </div>
          ) : (
            connectState === "connected" && (
              <p className="text-sm text-muted-foreground">
                Waiting for a QR code. Ensure the provider is ready to pair.
              </p>
            )
          )}
        </CardContent>
      </Card>
    </div>
  );
}

