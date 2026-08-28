import { ExternalLink } from "lucide-react";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { cn } from "@/lib/utils";
import { htmlFragmentToText } from "@/lib/messageUtils";
import { MessageText } from "./MessageText";

type CardNode = Record<string, unknown>;

type StructuredCardAttachment = { type?: string; cardJson?: string };

export function isStructuredAdaptiveCardAttachment(attachment: StructuredCardAttachment): boolean {
  return attachment.type === "adaptive_card" && Boolean(attachment.cardJson);
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function nodes(value: unknown): CardNode[] {
  return Array.isArray(value) ? value.filter((item): item is CardNode => Boolean(item) && typeof item === "object") : [];
}

function normalizeAdaptiveMarkdown(value: unknown): string {
  let result = htmlFragmentToText(text(value))
    .replace(/\\([@()[\]*_])/g, "$1")
    // Workflows sometimes nests a Markdown link inside another link target.
    .replace(/\[([^\]\n]+)\]\(\[([^\]\n]+)\]\((https?:\/\/[^)\s]+)\)\)/g, "[$1]($3)");

  if (/\{\*?\{(?:DATE|TIME)\(/i.test(result)) {
    result = result.replaceAll("*", "");
  }
  const formatDate = (raw: string, includeDate: boolean) => {
    const date = new Date(raw.trim());
    if (Number.isNaN(date.getTime())) return raw;
    return includeDate
      ? new Intl.DateTimeFormat(undefined, { weekday: "short", day: "2-digit", month: "short", year: "numeric" }).format(date)
      : new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" }).format(date);
  };
  result = result
    .replace(/\{\{DATE\(([^,]+),\s*SHORT\)\}\}/gi, (_, raw: string) => formatDate(raw, true))
    .replace(/\{\{TIME\(([^)]+)\)\}\}/gi, (_, raw: string) => formatDate(raw, false));
  return result;
}

function CardElement({ element }: { element: CardNode }) {
  const type = text(element.type).toLowerCase();

  if (type === "textblock") {
    const size = text(element.size).toLowerCase();
    const weight = text(element.weight).toLowerCase();
    const color = text(element.color).toLowerCase();
    return (
      <div className={cn(
        "whitespace-pre-wrap leading-snug",
        size === "large" || size === "extralarge" ? "text-lg" : size === "medium" ? "text-base" : "text-sm",
        weight === "bolder" || weight === "bold" ? "font-semibold" : "font-normal",
        color === "accent" && "text-indigo-600 dark:text-indigo-400",
		Boolean(element.separator) && "border-t border-border pt-3 mt-3",
        element.spacing === "none" ? "mt-0" : "mt-2",
		)}><MessageText text={normalizeAdaptiveMarkdown(element.text)} /></div>
    );
  }

  if (type === "factset") {
    return (
      <dl className="mt-3 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-7 gap-y-2.5 text-sm">
        {nodes(element.facts).map((fact, index) => (
          <div className="contents" key={`${text(fact.title)}-${index}`}>
			<dt className="font-semibold text-foreground"><MessageText text={normalizeAdaptiveMarkdown(fact.title).replace(/:\s*$/, "")} /></dt>
			<dd className="min-w-0 whitespace-pre-wrap text-foreground/90"><MessageText text={normalizeAdaptiveMarkdown(fact.value)} /></dd>
          </div>
        ))}
      </dl>
    );
  }

  if (type === "action.openurl") {
    const url = text(element.url);
    return (
      <button type="button" onClick={() => url && BrowserOpenURL(url)} className="inline-flex items-center gap-2 rounded-md border border-border bg-background px-3 py-2 text-sm font-semibold text-foreground shadow-sm transition-colors hover:bg-muted">
        <ExternalLink className="h-4 w-4" />{text(element.title)}
      </button>
    );
  }

  if (type === "image") {
    const url = text(element.url);
    return url ? <img src={url} alt={text(element.altText)} className="mt-2 max-h-48 max-w-full rounded object-contain" /> : null;
  }

  const children = [element.items, element.columns, element.body].flatMap(nodes);
  const actions = nodes(element.actions);
  if (type === "columnset") {
    return <div className="mt-2 flex flex-wrap gap-4">{children.map((child, index) => <div className="min-w-0 flex-1" key={index}><CardElement element={child} /></div>)}</div>;
  }
  if (type === "actionset") {
    return <div className="mt-3 flex flex-wrap gap-2">{actions.map((action, index) => <CardElement element={action} key={index} />)}</div>;
  }
  return (
    <div className={cn(type === "container" && "mt-3 rounded-lg border border-border bg-muted/20 p-4")}>
      {children.map((child, index) => <CardElement element={child} key={index} />)}
      {actions.length > 0 && <div className="mt-3 flex flex-wrap gap-2">{actions.map((action, index) => <CardElement element={action} key={index} />)}</div>}
    </div>
  );
}

export function StructuredAdaptiveCard({ cardJson }: { cardJson: string }) {
  let card: CardNode;
  try {
    card = JSON.parse(cardJson) as CardNode;
  } catch {
    return null;
  }
  return (
	<div className="mt-2 w-[720px] max-w-[min(82vw,calc(100vw-6rem))] rounded-xl border border-border bg-background p-4 text-foreground shadow-sm">
      {nodes(card.body).map((element, index) => <CardElement element={element} key={index} />)}
      {nodes(card.actions).length > 0 && <div className="mt-4 flex flex-wrap gap-2 border-t border-border pt-3">{nodes(card.actions).map((action, index) => <CardElement element={action} key={index} />)}</div>}
    </div>
  );
}

export function hasStructuredAdaptiveCard(attachments?: string): boolean {
  if (!attachments?.trim()) return false;
  try {
    const parsed = JSON.parse(attachments) as Array<{ type?: string; cardJson?: string }>;
    return parsed.some(isStructuredAdaptiveCardAttachment);
  } catch {
    return false;
  }
}
