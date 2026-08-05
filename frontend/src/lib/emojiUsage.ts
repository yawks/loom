const USAGE_STORAGE_KEY = "loom_emoji_usage_v1";
const PICKER_SUGGESTED_KEY = "epr_suggested";

const STANDARD_LIMIT = 20;
const CUSTOM_LIMIT = 10;

export interface EmojiUsage {
  id: string;
  original: string;
  count: number;
  lastUsedAt: string;
}

interface StoredEmojiUsage {
  standard: EmojiUsage[];
  customByProvider: Record<string, EmojiUsage[]>;
}

const emptyUsage = (): StoredEmojiUsage => ({ standard: [], customByProvider: {} });

const canUseStorage = () => typeof window !== "undefined" && Boolean(window.localStorage);

function isUsage(value: unknown): value is EmojiUsage {
  if (!value || typeof value !== "object") return false;
  const item = value as Partial<EmojiUsage>;
  return typeof item.id === "string" && typeof item.original === "string" &&
    typeof item.count === "number" && Number.isFinite(item.count) &&
    typeof item.lastUsedAt === "string";
}

function readUsage(): StoredEmojiUsage {
  if (!canUseStorage()) return emptyUsage();
  try {
    const raw = window.localStorage.getItem(USAGE_STORAGE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<StoredEmojiUsage>;
      const customByProvider = Object.fromEntries(
        Object.entries(parsed.customByProvider ?? {}).map(([provider, usages]) => [
          provider,
          Array.isArray(usages) ? usages.filter(isUsage) : [],
        ])
      );
      return {
        standard: Array.isArray(parsed.standard) ? parsed.standard.filter(isUsage) : [],
        customByProvider,
      };
    }

    // Preserve the history collected by emoji-picker-react before Loom owned it.
    const legacy = JSON.parse(window.localStorage.getItem(PICKER_SUGGESTED_KEY) ?? "[]") as Array<{
      unified?: string;
      original?: string;
      count?: number;
    }>;
    const now = Date.now();
    return {
      standard: legacy.slice(0, STANDARD_LIMIT).flatMap((item, index) =>
        item.unified && item.original
          ? [{
              id: item.unified,
              original: item.original,
              count: Math.max(1, Number(item.count) || 1),
              lastUsedAt: new Date(now - index).toISOString(),
            }]
          : []
      ),
      customByProvider: {},
    };
  } catch {
    return emptyUsage();
  }
}

function writeUsage(usage: StoredEmojiUsage): void {
  if (!canUseStorage()) return;
  try {
    window.localStorage.setItem(USAGE_STORAGE_KEY, JSON.stringify(usage));
  } catch {
    // Emoji history is a convenience; a full or disabled localStorage is harmless.
  }
}

const byFrequencyThenRecency = (a: EmojiUsage, b: EmojiUsage) =>
  b.count - a.count || Date.parse(b.lastUsedAt) - Date.parse(a.lastUsedAt);

function recordInList(list: EmojiUsage[], id: string, original: string, limit: number): EmojiUsage[] {
  const now = new Date().toISOString();
  const existing = list.find((item) => item.id === id);
  const updated = existing
    ? list.map((item) => item.id === id
        ? { ...item, original, count: item.count + 1, lastUsedAt: now }
        : item)
    : [...list, { id, original, count: 1, lastUsedAt: now }];

  // Capacity is an LRU concern; presentation order is a frequency concern.
  return updated
    .sort((a, b) => Date.parse(b.lastUsedAt) - Date.parse(a.lastUsedAt))
    .slice(0, limit);
}

function syncPickerSuggestions(standard: EmojiUsage[]): void {
  if (!canUseStorage()) return;
  try {
    window.localStorage.setItem(PICKER_SUGGESTED_KEY, JSON.stringify(
      [...standard].sort(byFrequencyThenRecency).map((item) => ({
        unified: item.id,
        original: item.original,
        count: item.count,
      }))
    ));
  } catch {
    // Keep Loom's history even if the third-party picker cache cannot be updated.
  }
}

export function prepareEmojiSuggestions(): void {
  const usage = readUsage();
  writeUsage(usage);
  syncPickerSuggestions(usage.standard);
}

export function recordStandardEmojiUsage(unified: string, original = unified): void {
  const usage = readUsage();
  usage.standard = recordInList(usage.standard, unified, original, STANDARD_LIMIT);
  writeUsage(usage);
  syncPickerSuggestions(usage.standard);
}

export function recordCustomEmojiUsage(providerInstanceId: string, name: string): void {
  if (!providerInstanceId) return;
  const usage = readUsage();
  usage.customByProvider[providerInstanceId] = recordInList(
    usage.customByProvider[providerInstanceId] ?? [],
    name,
    name,
    CUSTOM_LIMIT
  );
  writeUsage(usage);
}

export function orderCustomEmojis<T extends { id: string }>(providerInstanceId: string, emojis: T[]): T[] {
  const ranked = [...(readUsage().customByProvider[providerInstanceId] ?? [])].sort(byFrequencyThenRecency);
  const rank = new Map(ranked.map((item, index) => [item.id, index]));
  return emojis
    .map((emoji, index) => ({ emoji, index, rank: rank.get(emoji.id) }))
    .sort((a, b) => {
      if (a.rank !== undefined && b.rank !== undefined) return a.rank - b.rank;
      if (a.rank !== undefined) return -1;
      if (b.rank !== undefined) return 1;
      return a.index - b.index;
    })
    .map(({ emoji }) => emoji);
}
