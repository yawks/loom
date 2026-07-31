import { useEffect, useMemo, useState } from "react";
import { AlertCircle, Check, LoaderCircle, Search, X } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ScrollArea } from "@/components/ui/scroll-area";
import { cn } from "@/lib/utils";
import { useAppStore } from "@/lib/store";
import { getContactStatusEmoji } from "@/lib/statusEmoji";
import { Emoji } from "./Emoji";
import { ProtocolIcon } from "./ProtocolIcon";
import { GetAllLastMessageTimestamps, GetCapabilities, GetConfiguredProviders, GetProviderContacts, OpenConversation, SearchProviderContacts } from "../../wailsjs/go/main/App";
import type { core, models } from "../../wailsjs/go/models";

interface NewConversationModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

type ExtendedCapabilities = core.Capabilities & {
  supportsContactDirectory: boolean;
  supportsDirectConversation: boolean;
  supportsGroupConversation: boolean;
  supportsGroupTitle: boolean;
  requiresGroupTitle: boolean;
  groupConversationTypes: string;
};

const contactKey = (contact: models.MetaContact) => contact.linkedAccounts[0]?.userId ?? String(contact.id);

function ContactAvatar({ contact, checked = false, compact = false }: { contact: models.MetaContact; checked?: boolean; compact?: boolean }) {
  const account = contact.linkedAccounts[0];
  const status = account?.status && account.status !== "offline" ? account.status : "";
  const emoji = getContactStatusEmoji(contact);
  return (
    <div className="relative shrink-0">
      <Avatar className={compact ? "h-6 w-6" : undefined}>
        <AvatarImage src={contact.avatarUrl} />
        <AvatarFallback className={compact ? "text-[10px]" : undefined}>{contact.displayName.slice(0, 2).toUpperCase()}</AvatarFallback>
      </Avatar>
      {emoji && <span className="absolute -top-1 -left-1 bg-background rounded-full border p-0.5"><Emoji emoji={emoji.emoji} providerInstanceId={emoji.providerInstanceId} size={compact ? 9 : 12} /></span>}
      {status && <span title={status} className={cn("absolute -bottom-0.5 -right-0.5 rounded-full border-2 border-background",
        compact ? "h-2.5 w-2.5" : "h-3.5 w-3.5",
        status === "online" ? "bg-green-500" :
          status === "away" ? "bg-yellow-500" :
            status === "busy" || status === "meeting" || status === "dnd" ? "bg-red-500" :
              status === "holiday" ? "bg-purple-500" : "bg-slate-400")} />}
      {checked && <span className="absolute -top-1 -right-1 bg-primary text-primary-foreground rounded-full p-0.5"><Check className="h-3 w-3" /></span>}
    </div>
  );
}

export function NewConversationModal({ open, onOpenChange }: NewConversationModalProps) {
  const { t } = useTranslation();
  const setSelectedContact = useAppStore((state) => state.setSelectedContact);
  const [providers, setProviders] = useState<core.ProviderInfo[]>([]);
  const [providerId, setProviderId] = useState("");
  const [capabilities, setCapabilities] = useState<ExtendedCapabilities | null>(null);
  const [contacts, setContacts] = useState<models.MetaContact[]>([]);
  const [initialContacts, setInitialContacts] = useState<models.MetaContact[]>([]);
  const [selected, setSelected] = useState<models.MetaContact[]>([]);
  const [matches, setMatches] = useState<models.MetaContact[]>([]);
  const [search, setSearch] = useState("");
  const [resolvedSearch, setResolvedSearch] = useState("");
  const [lastMessageTimestamps, setLastMessageTimestamps] = useState<Record<string, number>>({});
  const [title, setTitle] = useState("");
  const [conversationType, setConversationType] = useState("");
  const [loadingProviderContacts, setLoadingProviderContacts] = useState(false);
  const [loadingSearchContacts, setLoadingSearchContacts] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!open) return;
    setSelected([]);
    setMatches([]);
    setSearch("");
    setResolvedSearch("");
    setTitle("");
    setError("");
    setResolvedSearch("");
    setProviderId("");
    setContacts([]);
    setInitialContacts([]);
    setCapabilities(null);
    void GetAllLastMessageTimestamps().then(setLastMessageTimestamps).catch(() => setLastMessageTimestamps({}));
    void GetConfiguredProviders().then(async (items) => {
      const supported = (await Promise.all(items.map(async (item) => ({
        item,
        caps: await GetCapabilities(item.instanceId).catch(() => null),
      })))).filter(({ caps }) => (caps as ExtendedCapabilities | null)?.supportsContactDirectory).map(({ item }) => item);
      setProviders(supported);
    });
  }, [open]);

  useEffect(() => {
    if (!open || !providerId) return;
    let cancelled = false;
    setLoadingProviderContacts(true);
    setCapabilities(null);
    setContacts([]);
    setInitialContacts([]);
    setSelected([]);
    setMatches([]);
    setError("");
    Promise.all([GetCapabilities(providerId), GetProviderContacts(providerId)])
      .then(([caps, providerContacts]) => {
        if (cancelled) return;
        const extended = caps as ExtendedCapabilities;
        setCapabilities(extended);
        setContacts(providerContacts);
        setInitialContacts(providerContacts);
        setConversationType(extended.groupConversationTypes?.split(",")[0] || "group");
      })
      .catch((reason) => { if (!cancelled) setError(String(reason)); })
      .finally(() => { if (!cancelled) setLoadingProviderContacts(false); });
    return () => { cancelled = true; };
  }, [open, providerId]);

  useEffect(() => {
    if (!open || !providerId) return;
    if (search.trim().length < 2) {
      setContacts(initialContacts);
      setResolvedSearch("");
      setLoadingSearchContacts(false);
      return;
    }
    let cancelled = false;
    const query = search.trim();
    setLoadingSearchContacts(true);
    const timeout = window.setTimeout(() => {
      void SearchProviderContacts(providerId, query)
        .then((results) => { if (!cancelled) { setContacts(results); setResolvedSearch(query); } })
        .catch((reason) => { if (!cancelled) { setError(String(reason)); setResolvedSearch(query); } })
        .finally(() => { if (!cancelled) setLoadingSearchContacts(false); });
    }, 300);
    return () => { cancelled = true; window.clearTimeout(timeout); };
  }, [open, providerId, search, initialContacts, capabilities]);

  const filtered = useMemo(() => {
    const query = search.trim().toLocaleLowerCase();
    const matching = query.length >= 2
      ? [...contacts]
      : contacts.filter((contact) => !query || contact.displayName.toLocaleLowerCase().includes(query));
    return matching.sort((a, b) => {
      const aTime = lastMessageTimestamps[a.linkedAccounts[0]?.conversationId ?? ""] ?? 0;
      const bTime = lastMessageTimestamps[b.linkedAccounts[0]?.conversationId ?? ""] ?? 0;
      return bTime - aTime || a.displayName.localeCompare(b.displayName);
    });
  }, [contacts, search, lastMessageTimestamps]);

  const searchPending = search.trim().length >= 2 && resolvedSearch !== search.trim();
  const showContactsLoader = loadingProviderContacts || loadingSearchContacts || searchPending;

  const toggle = (contact: models.MetaContact) => {
    const key = contactKey(contact);
    setSelected((current) => current.some((item) => contactKey(item) === key)
      ? current.filter((item) => contactKey(item) !== key)
      : [...current, contact]);
  };

  const openContact = (contact: models.MetaContact) => {
    setSelectedContact(contact);
    onOpenChange(false);
  };

  const submit = async () => {
    if (!providerId || selected.length === 0) return;
    setIsSubmitting(true);
    setError("");
    try {
      const resolution = await OpenConversation({
        providerInstanceId: providerId,
        participantIds: selected.map((contact) => contact.linkedAccounts[0].userId),
        conversationType,
        title: title.trim(),
      });
      if (resolution.matches.length === 1) return openContact(resolution.matches[0]);
      if (resolution.matches.length > 1) {
        setMatches(resolution.matches);
        return;
      }
      if (resolution.created) openContact(resolution.created);
    } catch (reason) {
      const rawError = String(reason);
      setError(rawError.includes("name_taken")
        ? t("conversation_errors.name_taken")
        : rawError.includes("invalid_name_specials")
          ? t("conversation_errors.invalid_name")
          : rawError.includes("UNIQUE constraint failed")
            ? t("conversation_errors.persistence_conflict")
            : rawError.replace(/^Error:\s*/, ""));
    } finally {
      setIsSubmitting(false);
    }
  };

  const isGroup = selected.length > 1;
  const types = capabilities?.groupConversationTypes?.split(",").filter(Boolean) ?? [];
  const titleRequired = !!(isGroup && capabilities?.requiresGroupTitle && conversationType !== "group_message");
  const canSubmit = selected.length > 0 && (!isGroup || capabilities?.supportsGroupConversation) && (!titleRequired || !!title.trim());

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[560px] h-[82vh] flex flex-col p-0 gap-0">
        <DialogHeader className="p-6 pb-3"><DialogTitle>{t("new_conversation")}</DialogTitle></DialogHeader>
        <div className="flex-1 flex flex-col overflow-hidden px-6 gap-4">
          <div>
            <Label className="mb-2 block">{t("choose_provider")}</Label>
            <div className="flex flex-wrap gap-2">
              {providers.map((provider) => (
                <Button key={provider.instanceId} type="button" variant={providerId === provider.instanceId ? "default" : "outline"}
                  className="gap-2" onClick={() => { setSearch(""); setProviderId(provider.instanceId); }}>
                  <ProtocolIcon protocol={provider.id} className="h-4 w-4" />
                  {provider.instanceName || provider.name}
                </Button>
              ))}
              {providers.length === 0 && <p className="text-sm text-muted-foreground">{t("no_provider_contact_directory")}</p>}
            </div>
          </div>

          {providerId && (matches.length > 1 ? (
            <div className="flex-1 overflow-hidden flex flex-col gap-3">
              <p className="text-sm text-muted-foreground">{t("choose_existing_conversation")}</p>
              <ScrollArea className="flex-1 border rounded-md">
                <div className="p-2 space-y-1">
                  {matches.map((contact) => (
                    <button key={contactKey(contact)} type="button" onClick={() => openContact(contact)}
                      className="w-full flex items-center gap-3 p-3 rounded-lg text-left hover:bg-muted">
                      <ContactAvatar contact={contact} />
                      <div className="min-w-0"><div className="font-medium truncate">{contact.displayName}</div>
                        <div className="text-xs text-muted-foreground">{contact.linkedAccounts[0]?.protocol}</div></div>
                    </button>
                  ))}
                </div>
              </ScrollArea>
            </div>
          ) : (
            <>
              {selected.length > 0 && (
                <div className="rounded-md border bg-muted/20 p-2 flex flex-wrap gap-2">
                  {selected.map((contact) => <Button key={contactKey(contact)} type="button" variant="secondary" size="sm" className="h-auto min-h-8 gap-2 py-1 pl-1.5 pr-2" onClick={() => toggle(contact)}>
                    <ContactAvatar contact={contact} compact />
                    {contact.displayName}<X className="h-3 w-3" />
                  </Button>)}
                </div>
              )}

              {isGroup && types.length > 1 && (
                <div><Label className="mb-2 block">{t("conversation_type")}</Label>
                  <div className="flex flex-wrap gap-2">{types.map((type) => <Button key={type} type="button" size="sm"
                    variant={conversationType === type ? "default" : "outline"} onClick={() => setConversationType(type)}>{t(`conversation_types.${type}`)}</Button>)}</div>
                </div>
              )}
              {isGroup && capabilities?.supportsGroupTitle && conversationType !== "group_message" && (
                <div><Label htmlFor="group-title" className="mb-2 block">{t("conversation_title")}</Label>
                  <Input id="group-title" value={title} onChange={(event) => setTitle(event.target.value)} placeholder={t("optional_group_title")} /></div>
              )}

              <div className="relative"><Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
                <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t("search_contacts")} className="pl-9" /></div>
              <ScrollArea className="flex-1 border rounded-md">
                <div className="p-2 space-y-1">
                  {showContactsLoader && <div className="flex items-center justify-center gap-2 py-10 text-sm text-muted-foreground"><LoaderCircle className="h-5 w-5 animate-spin" />{t("loading_contacts")}</div>}
                  {!showContactsLoader && filtered.length === 0 && <div className="text-center py-8 text-muted-foreground">{t("no_contacts_found")}</div>}
                  {!showContactsLoader && filtered.map((contact) => {
                    const account = contact.linkedAccounts[0];
                    const checked = selected.some((item) => contactKey(item) === contactKey(contact));
                    const selectionDisabled = !checked && selected.length > 0 && !capabilities?.supportsGroupConversation;
                    return <button key={contactKey(contact)} type="button" disabled={selectionDisabled} onClick={() => toggle(contact)}
                      className={cn("w-full flex items-center gap-3 p-2 rounded-lg text-left", checked ? "bg-accent" : "hover:bg-muted", selectionDisabled && "opacity-40 cursor-not-allowed")}>
                      <ContactAvatar contact={contact} checked={checked} />
                      <div className="min-w-0"><div className="font-medium truncate">{contact.displayName}</div><div className="text-xs text-muted-foreground truncate">{account?.username || account?.userId}</div></div>
                    </button>;
                  })}
                </div>
              </ScrollArea>
            </>
          ))}
          {error && <div role="alert" className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
            <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
            <span>{error}</span>
          </div>}
        </div>
        <DialogFooter className="p-6 pt-4 border-t mt-4">
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("cancel")}</Button>
          {matches.length === 0 && <Button onClick={submit} disabled={!canSubmit || isSubmitting}>{isSubmitting ? t("creating") : t("continue")}</Button>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
