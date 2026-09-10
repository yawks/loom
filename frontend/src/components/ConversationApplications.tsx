import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { AppWindow, ExternalLink, ShieldAlert, X } from "lucide-react";
import { GetConversationApplications } from "../../wailsjs/go/main/App";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import type { models } from "../../wailsjs/go/models";

export function ConversationApplications({ conversationId }: { conversationId: string }) {
  const [selected, setSelected] = useState<models.ConversationApplication | null>(null);
  const [approvedOrigin, setApprovedOrigin] = useState<string | null>(null);
  const { data = [] } = useQuery({
    queryKey: ["conversation-applications", conversationId],
    queryFn: () => GetConversationApplications(conversationId),
    enabled: Boolean(conversationId),
    refetchInterval: 5000,
  });

  if (!data.length && !selected) return null;
  return <div className="border-t pt-4 space-y-2">
    <h4 className="text-sm font-semibold text-muted-foreground">Applications</h4>
    {data.map((application) => <button key={application.id} type="button" className="flex w-full items-center gap-2 rounded-md border p-2 text-left hover:bg-muted" onClick={() => { setSelected(application); setApprovedOrigin(null); }}>
      <AppWindow className="h-4 w-4" />
      <span className="truncate text-sm">{application.name}</span>
    </button>)}
    {selected && <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 p-4" role="dialog" aria-modal="true" aria-label={selected.name}>
      <div className="flex h-[85vh] w-[min(1100px,95vw)] flex-col overflow-hidden rounded-lg border bg-background shadow-2xl">
        <div className="flex items-center gap-2 border-b p-3">
          <AppWindow className="h-4 w-4" /><strong className="flex-1 truncate">{selected.name}</strong>
          <button type="button" aria-label="Ouvrir dans le navigateur" onClick={() => BrowserOpenURL(selected.launchUrl)}><ExternalLink className="h-4 w-4" /></button>
          <button type="button" aria-label="Fermer" onClick={() => setSelected(null)}><X className="h-4 w-4" /></button>
        </div>
        {approvedOrigin !== selected.origin ? <div className="m-auto max-w-md space-y-4 p-6 text-center">
          <ShieldAlert className="mx-auto h-10 w-10 text-amber-500" />
          <p className="text-sm">Cette application distante pourra exécuter des scripts et communiquer avec son origine.</p>
          <p className="break-all font-mono text-xs text-muted-foreground">{selected.origin}</p>
          <button type="button" className="rounded-md bg-primary px-4 py-2 text-sm text-primary-foreground" onClick={() => setApprovedOrigin(selected.origin)}>Autoriser et ouvrir</button>
        </div> : <iframe
          title={selected.name}
          src={selected.launchUrl}
          className="h-full w-full border-0 bg-background"
          sandbox="allow-scripts allow-forms allow-same-origin"
          referrerPolicy="no-referrer"
          allow="camera 'none'; microphone 'none'; geolocation 'none'; clipboard-read 'none'; clipboard-write 'none'"
        />}
      </div>
    </div>}
  </div>;
}
