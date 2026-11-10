import type { CSSProperties } from "react";

export type CardNode = Record<string, unknown>;
export type StructuredCardAttachment = { type?: string; cardJson?: string; isSupplementary?: boolean };

export function isStructuredAdaptiveCardAttachment(attachment: StructuredCardAttachment): boolean {
  return attachment.type === "adaptive_card" && Boolean(attachment.cardJson);
}

function structuredAttachments(attachments?: string): StructuredCardAttachment[] {
  try {
    const parsed: unknown = JSON.parse(attachments || "[]");
    return Array.isArray(parsed) ? parsed.filter((value): value is StructuredCardAttachment =>
      value !== null && typeof value === "object" && isStructuredAdaptiveCardAttachment(value)) : [];
  } catch {
    return [];
  }
}

export function hasStructuredAdaptiveCard(attachments?: string): boolean {
  return structuredAttachments(attachments).length > 0;
}

export function shouldHideAdaptiveCardBody(attachments?: string): boolean {
  // Legacy structured cards omitted this field and stored their searchable
  // fallback in Body. Supplementary cards explicitly preserve independent text.
  return structuredAttachments(attachments).some((attachment) => !attachment.isSupplementary);
}

function pixelSize(value: unknown): number | undefined {
  if (typeof value !== "string" || !/^\d+(?:\.\d+)?px$/i.test(value)) return undefined;
  return Math.min(720, Math.max(1, parseFloat(value)));
}

export function adaptiveColumnStyle(column: CardNode): CSSProperties {
  const width = pixelSize(column.width);
  const flex = width !== undefined ? `0 0 ${width}px`
    : column.width === "auto" ? "0 0 auto"
    : typeof column.width === "number" && Number.isFinite(column.width) && column.width > 0
      ? `${Math.min(column.width, 100)} 1 0px` : "1 1 0px";
  return {
    flex, minWidth: 0,
    ...(width !== undefined ? { width } : {}),
    ...(typeof column.verticalContentAlignment === "string" && column.verticalContentAlignment.toLowerCase() === "center"
      ? { display: "flex", flexDirection: "column", justifyContent: "center" } : {}),
  };
}

export function adaptiveImageStyle(image: CardNode): CSSProperties {
  const width = pixelSize(image.width);
  const height = pixelSize(image.height);
  return { ...(width !== undefined ? { width } : {}), ...(height !== undefined ? { height } : {}), maxWidth: "100%" };
}

export function adaptiveSpacingClass(element: CardNode): string {
  switch (String(element.spacing ?? "").toLowerCase()) {
    case "none": return "mt-0";
    case "small": return "mt-1";
    case "medium": return "mt-3";
    case "large": return "mt-4";
    default: return "mt-2";
  }
}

export function safeAdaptiveActionURL(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  try {
    const url = new URL(value);
    return url.protocol === "https:" || url.protocol === "http:" ? url.href : undefined;
  } catch {
    return undefined;
  }
}
