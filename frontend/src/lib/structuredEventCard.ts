export interface EventCardData { title: string; badge?: string; description?: string; imageUrl?: string }
export function isStructuredEventCardAttachment(value: { type?: string; cardJson?: string }): boolean { return value.type === "event-card" && Boolean(value.cardJson); }
