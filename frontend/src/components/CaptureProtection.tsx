import { createContext, useContext, useLayoutEffect, useState } from "react";
import type { ReactNode } from "react";
import { createPortal } from "react-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Shield, ShieldCheck } from "lucide-react";
import { useTranslation } from "react-i18next";
import { GetCaptureProtectionSettings, SetConversationCaptureProtection, SetWindowCaptureProtection } from "../../wailsjs/go/main/App";
import { useAppStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { createCaptureProtectionQueue } from "@/lib/captureProtectionQueue";

const settingsKey = ["captureProtectionSettings"];
const applyProtection = createCaptureProtectionQueue(SetWindowCaptureProtection, () =>
  new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())))
);

interface ProtectionContext {
  supported: boolean;
  limited: boolean;
  conversationIds: string[];
  save: (conversationId: string, enabled: boolean) => Promise<void>;
}
const Context = createContext<ProtectionContext | null>(null);

export function CaptureProtectionProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const settings = useQuery({ queryKey: settingsKey, queryFn: GetCaptureProtectionSettings, staleTime: Infinity, retry: false });
  const selectedContact = useAppStore((state) => state.selectedContact);
  const selectedProfile = useAppStore((state) => state.selectedContactProfile);
  const protectedIds = settings.data?.conversationIds ?? [];
  // Protect all linked conversations of the selected contact: details and
  // conversation search can show more than the currently selected account.
  const selectedIds = selectedContact?.linkedAccounts.map((account) =>
    account.conversationId || (account.providerInstanceId && account.userId
      ? `${account.providerInstanceId}::${account.userId}` : account.userId)
  ) ?? [];
  if (selectedProfile) selectedIds.push(selectedProfile.conversationId);
  const enabled = selectedIds.some((id) => protectedIds.includes(id));
  const [ready, setReady] = useState(false);
  const [nativeError, setNativeError] = useState(false);
  const [attempt, setAttempt] = useState(0);

  useLayoutEffect(() => {
    let cancelled = false;
    setReady(false);
    setNativeError(false);
    if (settings.isSuccess) {
      // Layout effects hide the whole document (including portal dialogs)
      // before painting a newly selected confidential conversation.
      applyProtection(enabled).then(() => {
        if (!cancelled) setReady(true);
      }).catch((error: unknown) => {
        console.error("Failed to apply window capture protection:", error);
        if (!cancelled) setNativeError(true);
      });
    }
    return () => { cancelled = true; };
  }, [enabled, settings.isSuccess, attempt]);

  const save = async (conversationId: string, value: boolean) => {
    await SetConversationCaptureProtection(conversationId, value);
    queryClient.setQueryData<Awaited<ReturnType<typeof GetCaptureProtectionSettings>>>(settingsKey, (previous) => {
      if (!previous) return previous;
      const ids = previous.conversationIds.filter((id) => id !== conversationId);
      if (value) ids.push(conversationId);
      return { ...previous, conversationIds: ids };
    });
  };
  const blocked = !settings.isSuccess || !ready;
  const failed = settings.isError || nativeError;

  return (
    <Context.Provider value={{ supported: settings.data?.supported ?? false, limited: settings.data?.limited ?? false, conversationIds: protectedIds, save }}>
      <div inert={blocked} className="h-full">{children}</div>
      {blocked && createPortal(
        <div data-capture-protection-overlay className="pointer-events-auto fixed inset-0 z-[99999] flex flex-col items-center justify-center gap-4 bg-background p-8 text-center text-foreground">
          <style>{`body > :not([data-capture-protection-overlay]), body > :not([data-capture-protection-overlay]) * { visibility: hidden !important; }`}</style>
          <Shield className="h-8 w-8" />
          <p role={failed ? "alert" : "status"}>{t(failed ? "capture_protection_error" : "capture_protection_pending")}</p>
          {failed && <Button onClick={() => { if (settings.isError) void settings.refetch(); else setAttempt((value) => value + 1); }}>{t("capture_protection_retry")}</Button>}
          {nativeError && enabled && <Button variant="outline" onClick={() => {
            useAppStore.getState().setSelectedContact(null);
            useAppStore.getState().setSelectedContactProfile(null);
            useAppStore.getState().setShowThreads(false);
            useAppStore.getState().setSelectedAvatarUrl(null);
          }}>{t("capture_protection_leave")}</Button>}
        </div>, document.body
      )}
    </Context.Provider>
  );
}

export function CaptureProtectionControl({ conversationId }: { conversationId: string }) {
  const protection = useContext(Context);
  const { t } = useTranslation();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState(false);
  const enabled = protection?.conversationIds.includes(conversationId) ?? false;
  if (!protection || (!protection.supported && !enabled) || !conversationId) return null;

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant={enabled ? "secondary" : "ghost"} size="icon" title={t(enabled ? "capture_protection_on" : "capture_protection_title")} aria-label={t(enabled ? "capture_protection_on" : "capture_protection_title")}>
          {enabled ? <ShieldCheck className="h-4 w-4" /> : <Shield className="h-4 w-4" />}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 space-y-3">
        <p className="font-medium">{t("capture_protection_title")}</p>
        <p className="text-sm text-muted-foreground">{t("capture_protection_description")}</p>
        {protection.limited && <p className="text-sm text-amber-700 dark:text-amber-400">{t("capture_protection_limited")}</p>}
        <p className="text-xs text-muted-foreground">{t("capture_protection_scope")}</p>
        {error && <p role="alert" className="text-sm text-destructive">{t("capture_protection_save_error")}</p>}
        <Button className="w-full" disabled={saving} onClick={async () => {
          setSaving(true);
          setError(false);
          try { await protection.save(conversationId, !enabled); }
          catch { setError(true); }
          finally { setSaving(false); }
        }}>{t(enabled ? "capture_protection_disable" : "capture_protection_enable")}</Button>
      </PopoverContent>
    </Popover>
  );
}
