import { useEffect, useMemo, useState } from "react";
import { GetProviderAttachmentData } from "../../wailsjs/go/main/App";
import { MessageText } from "./MessageText";
import type { EventCardData } from "../lib/structuredEventCard";

function badgeHue(value: string): number { let hash = 0; for (const char of value.trim().toLowerCase()) hash = (Math.imul(hash, 31) + (char.codePointAt(0) ?? 0)) >>> 0; return hash % 360; }

export function StructuredEventCard({ cardJson, providerInstanceId }: { cardJson: string; providerInstanceId?: string }) {
  const card = useMemo<EventCardData | null>(() => { try { const parsed = JSON.parse(cardJson) as EventCardData; return parsed.title ? parsed : null; } catch { return null; } }, [cardJson]);
  const [imageData, setImageData] = useState<string | null>(null);
  const [imageFailed, setImageFailed] = useState(false);
  useEffect(() => {
    if (!card?.imageUrl || !providerInstanceId) return;
    let active = true;
    GetProviderAttachmentData(providerInstanceId, card.imageUrl).then((data) => { if (active) setImageData(data); }).catch(() => { if (active) setImageFailed(true); });
    return () => { active = false; };
  }, [card?.imageUrl, providerInstanceId]);
  if (!card) return null;
  const descriptionLines = (card.description ?? "").split("\n");
  if (descriptionLines[0]?.replace(/^#+\s*/, "").trim() === card.title.trim()) descriptionLines.shift();
  const description = descriptionLines.join("\n").trim();
  const hue = badgeHue(card.badge ?? "event");
  return <article className="w-full max-w-[640px] rounded-xl border border-border/70 bg-background/80 p-3 shadow-sm sm:p-4">
    <div className="grid grid-cols-[40px_minmax(0,1fr)] items-start gap-2.5 sm:grid-cols-[56px_minmax(0,1fr)] sm:gap-3.5">
      <div className="grid h-10 w-10 place-items-center overflow-hidden rounded-xl bg-muted text-lg font-bold text-muted-foreground sm:h-14 sm:w-14 sm:text-xl">
        {imageData && !imageFailed ? <img src={imageData} alt="" className="h-full w-full object-contain" onError={() => setImageFailed(true)} /> : card.title.slice(0, 1).toUpperCase()}
      </div>
      <div className="min-w-0">
        <div className="flex items-start justify-between gap-2.5">
          <h3 className="min-w-0 break-words text-base font-bold leading-snug">{card.title}</h3>
          {card.badge && <span className="max-w-[45%] shrink-0 break-words rounded-full border px-2 py-0.5 text-center text-[11px] font-semibold" style={{ color: `hsl(${hue} 65% 42%)`, borderColor: `hsl(${hue} 45% 55% / .45)`, background: `hsl(${hue} 65% 50% / .14)` }}>{card.badge}</span>}
        </div>
        {description && <div className="mt-1.5 text-sm leading-relaxed text-muted-foreground"><MessageText text={description} providerInstanceId={providerInstanceId} /></div>}
      </div>
    </div>
  </article>;
}
