import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { GetConfiguredProviders, GetNotificationSettings, SaveNotificationSettings } from "../../wailsjs/go/main/App";
import type { core, models } from "../../wailsjs/go/models";
import { RequestNotificationAuthorization } from "../../wailsjs/runtime/runtime";
import { cn } from "@/lib/utils";
import { ProtocolIcon } from "@/components/ProtocolIcon";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { AtSign, Bell, BellRing, ChevronDown, MessageCircle, Users } from "lucide-react";
import type { ReactNode } from "react";

type NotificationRule = Omit<models.NotificationSettings, "convertValues">;

const defaultRule = (providerInstanceId = ""): NotificationRule => ({
  id: 0, providerInstanceId, useGlobal: providerInstanceId !== "", enabled: false,
  showConversationName: true, showMessageDetail: true,
  conversationScope: "all", trigger: "every_message",
  createdAt: {} as NotificationRule["createdAt"], updatedAt: {} as NotificationRule["updatedAt"],
});

function Toggle({ checked, disabled, onChange, label }: { checked: boolean; disabled?: boolean; onChange: (value: boolean) => void; label: string }) {
  return <label className={cn("flex items-center justify-between gap-4 py-2 text-sm", disabled && "opacity-45")}>
    <span>{label}</span>
    <input type="checkbox" className="h-4 w-4 accent-primary" checked={checked} disabled={disabled} onChange={(event) => onChange(event.target.checked)} />
  </label>;
}

function NotificationPreview({ highlight }: { highlight: "title" | "message" }) {
  return <div aria-hidden="true" className="w-36 rounded-lg border bg-background p-2 shadow-sm">
    <div className="flex gap-2">
      <div className="mt-0.5 h-5 w-5 rounded-md bg-primary/15 p-0.5"><Bell className="h-4 w-4 text-primary" /></div>
      <div className="min-w-0 flex-1 space-y-1.5">
        <div className={cn("h-2 rounded-full", highlight === "title" ? "bg-primary" : "bg-muted-foreground/25")} />
        <div className={cn("h-1.5 rounded-full", highlight === "message" ? "bg-primary" : "bg-muted-foreground/20")} />
        <div className={cn("h-1.5 w-2/3 rounded-full", highlight === "message" ? "bg-primary/60" : "bg-muted-foreground/15")} />
      </div>
    </div>
  </div>;
}

function RuleToggleRow({ children, preview, disabled }: { children: ReactNode; preview?: "title" | "message"; disabled?: boolean }) {
  return <div className="border-b py-2 last:border-b-0">
    {children}
    {preview && <div className={cn("pb-1", disabled && "opacity-45")}><NotificationPreview highlight={preview} /></div>}
  </div>;
}

type Choice = { value: string; label: string; icon: typeof Bell };
function ChoiceDropdown({ value, choices, disabled, onChange }: { value: string; choices: Choice[]; disabled?: boolean; onChange: (value: string) => void }) {
  const [open, setOpen] = useState(false);
  const selected = choices.find((choice) => choice.value === value) || choices[0];
  const SelectedIcon = selected.icon;
  return <Popover open={open} onOpenChange={setOpen}>
    <PopoverTrigger asChild><Button type="button" variant="outline" disabled={disabled} className="w-full justify-between">
      <span className="flex items-center gap-2"><SelectedIcon className="h-4 w-4 text-muted-foreground" />{selected.label}</span><ChevronDown className="h-4 w-4 opacity-50" />
    </Button></PopoverTrigger>
    <PopoverContent align="start" className="w-[var(--radix-popover-trigger-width)] p-1">
      {choices.map((choice) => { const Icon = choice.icon; return <Button key={choice.value} type="button" variant={choice.value === value ? "secondary" : "ghost"} className="w-full justify-start gap-2" onClick={() => { onChange(choice.value); setOpen(false); }}>
        <Icon className="h-4 w-4 text-muted-foreground" />{choice.label}{choice.value === value && <span className="ml-auto">✓</span>}
      </Button>; })}
    </PopoverContent>
  </Popover>;
}

function RuleEditor({ rule, global, globalRule, onChange }: { rule: NotificationRule; global: boolean; globalRule?: NotificationRule; onChange: (rule: NotificationRule) => void }) {
  const { t } = useTranslation();
  const update = (patch: Partial<NotificationRule>) => onChange({ ...rule, ...patch });
  const inherited = !global && rule.useGlobal;
  const disabled = inherited || !rule.enabled;
  const setUseGlobal = (useGlobal: boolean) => {
    if (!useGlobal && rule.useGlobal && globalRule) {
      onChange({
        ...rule,
        useGlobal: false,
        enabled: globalRule.enabled,
        showConversationName: globalRule.showConversationName,
        showMessageDetail: globalRule.showMessageDetail,
        conversationScope: globalRule.conversationScope,
        trigger: globalRule.trigger,
      });
      return;
    }
    update({ useGlobal });
  };
  return <div className="space-y-1">
    {!global && <Toggle checked={rule.useGlobal} onChange={setUseGlobal} label={t("notifications_use_global")} />}
    {!inherited && <div>
      <div className="rounded-lg border px-3">
        <RuleToggleRow><Toggle checked={rule.enabled} disabled={inherited} onChange={(enabled) => update({ enabled })} label={t("notifications_enabled")} /></RuleToggleRow>
        <RuleToggleRow preview="title" disabled={disabled}><Toggle checked={rule.showConversationName} disabled={disabled} onChange={(showConversationName) => update({ showConversationName, ...(!showConversationName ? { showMessageDetail: false } : {}) })} label={t("notifications_show_conversation")} /></RuleToggleRow>
        <RuleToggleRow preview="message" disabled={disabled || !rule.showConversationName}><Toggle checked={rule.showMessageDetail} disabled={disabled || !rule.showConversationName} onChange={(showMessageDetail) => update({ showMessageDetail })} label={t("notifications_show_detail")} /></RuleToggleRow>
      </div>
      <label className={cn("block space-y-1 py-2 text-sm", disabled && "opacity-45")}>
        <span>{t("notifications_conversations")}</span>
        <ChoiceDropdown disabled={disabled} value={rule.conversationScope} onChange={(conversationScope) => update({ conversationScope })} choices={[
          { value: "dm", label: t("notifications_dm_only"), icon: MessageCircle }, { value: "all", label: t("notifications_all_conversations"), icon: Users },
        ]} />
      </label>
      <label className={cn("block space-y-1 py-2 text-sm", disabled && "opacity-45")}>
        <span>{t("notifications_trigger")}</span>
        <ChoiceDropdown disabled={disabled} value={rule.trigger} onChange={(trigger) => update({ trigger })} choices={[
          { value: "every_message", label: t("notifications_every_message"), icon: BellRing }, { value: "attention", label: t("notifications_attention_only"), icon: AtSign },
        ]} />
      </label>
    </div>}
  </div>;
}

export function NotificationSettings() {
  const { t } = useTranslation();
  const [providers, setProviders] = useState<core.ProviderInfo[]>([]);
  const [rules, setRules] = useState<Record<string, NotificationRule>>({});
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    try {
      const accounts = await GetConfiguredProviders();
      const entries = await Promise.all(["", ...accounts.map((p) => p.instanceId)].map(async (id) => [id, await GetNotificationSettings(id)] as const));
      setProviders(accounts); setRules(Object.fromEntries(entries)); setError("");
    } catch { setError(t("notifications_load_error")); }
  }, [t]);
  useEffect(() => { void load(); }, [load]);

  const save = async (id: string, rule: NotificationRule) => {
    setRules((current) => ({ ...current, [id]: rule }));
    try {
      const saved = await SaveNotificationSettings(rule as models.NotificationSettings);
      setRules((current) => ({ ...current, [id]: saved })); setError("");
      if (saved.enabled && !saved.useGlobal) void RequestNotificationAuthorization().catch(() => undefined);
    } catch { setError(t("notifications_save_error")); }
  };
  return <div className="flex-1 space-y-6 overflow-y-auto scroll-area p-6">
    <div><h3 className="font-semibold">{t("notifications_global")}</h3><p className="text-xs text-muted-foreground">{t("notifications_global_help")}</p>
      <RuleEditor global rule={rules[""] || defaultRule()} onChange={(rule) => void save("", rule)} /></div>
    {providers.map((provider) => <div className="border-t pt-5" key={provider.instanceId}>
      <h3 className="flex items-center gap-2 font-semibold"><ProtocolIcon protocol={provider.id} size={20} />{provider.instanceName || provider.name}</h3>
      <RuleEditor global={false} globalRule={rules[""] || defaultRule()} rule={rules[provider.instanceId] || defaultRule(provider.instanceId)} onChange={(rule) => void save(provider.instanceId, rule)} />
    </div>)}
    {error && <p className="text-sm text-destructive">{error}</p>}
  </div>;
}
