import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Building2, Loader2, Mail, MapPin, Phone, UserRound } from "lucide-react";

import { GetDocumentPreview } from "../../wailsjs/go/main/App";

type Preview = {
  kind: "eml" | "vcard";
  name: string;
  subject: string;
  from: string;
  to: string;
  date: string;
  body: string;
  organization: string;
  title: string;
  phones: string[];
  emails: string[];
  addresses: string[];
};

export default function TextDocumentPreview({ url, kind, providerInstanceId }: { url: string; kind: Preview["kind"]; providerInstanceId?: string }) {
  const { t } = useTranslation();
  const [preview, setPreview] = useState<Preview>();
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let active = true;
    void GetDocumentPreview(providerInstanceId || "", url, kind)
      .then((value) => { if (active) setPreview(value as Preview); })
      .catch(() => { if (active) setFailed(true); });
    return () => { active = false; };
  }, [url, kind, providerInstanceId]);

  if (failed) return <div className="flex flex-1 items-center justify-center text-muted-foreground" role="alert">{t("office_preview_error")}</div>;
  if (!preview) return <Loader2 className="m-auto animate-spin" />;

  if (preview.kind === "eml") return (
    <article className="min-h-0 flex-1 overflow-auto rounded-xl border bg-background">
      <header className="space-y-2 border-b bg-muted/30 p-5">
        <h2 className="text-xl font-semibold">{preview.subject || t("email_no_subject")}</h2>
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
          <dt className="text-muted-foreground">{t("email_from")}</dt><dd className="break-all">{preview.from}</dd>
          <dt className="text-muted-foreground">{t("email_to")}</dt><dd className="break-all">{preview.to}</dd>
          {preview.date && <><dt className="text-muted-foreground">{t("email_date")}</dt><dd>{preview.date}</dd></>}
        </dl>
      </header>
      <div className="whitespace-pre-wrap break-words p-5 text-sm leading-6">{preview.body}</div>
    </article>
  );

  const initials = preview.name.split(/\s+/).slice(0, 2).map((part) => part[0]).join("").toUpperCase();
  return (
    <article className="m-auto w-full max-w-lg overflow-hidden rounded-2xl border bg-background shadow-sm">
      <header className="flex items-center gap-4 border-b bg-muted/30 p-6">
        <div className="flex h-16 w-16 shrink-0 items-center justify-center rounded-full bg-emerald-500/15 text-xl font-semibold text-emerald-600">{initials || <UserRound />}</div>
        <div className="min-w-0"><h2 className="truncate text-xl font-semibold">{preview.name || t("contact")}</h2><p className="text-sm text-muted-foreground">{preview.title}{preview.title && preview.organization ? " · " : ""}{preview.organization}</p></div>
      </header>
      <div className="space-y-4 p-6">
        {preview.phones?.map((value) => <a key={value} className="flex items-center gap-3 hover:underline" href={`tel:${value}`}><Phone className="h-4 w-4 text-muted-foreground" />{value}</a>)}
        {preview.emails?.map((value) => <a key={value} className="flex items-center gap-3 break-all hover:underline" href={`mailto:${value}`}><Mail className="h-4 w-4 shrink-0 text-muted-foreground" />{value}</a>)}
        {preview.addresses?.map((value) => <div key={value} className="flex items-start gap-3"><MapPin className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />{value}</div>)}
        {preview.organization && !preview.title && <div className="flex items-center gap-3"><Building2 className="h-4 w-4 text-muted-foreground" />{preview.organization}</div>}
      </div>
    </article>
  );
}
