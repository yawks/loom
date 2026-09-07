import type { CSSProperties } from "react";
import type { core } from "../../wailsjs/go/models";

const INSTANCE_COLOR_FILTERS = [
  "hue-rotate(0deg)",
  "hue-rotate(60deg)",
  "hue-rotate(120deg)",
  "hue-rotate(180deg)",
  "hue-rotate(240deg)",
  "hue-rotate(300deg)",
];

const instanceKey = (provider: core.ProviderInfo) => provider.instanceId || provider.id;

function compareInstanceIds(left: string, right: string): number {
  const leftMatch = left.match(/^(.*)-(\d+)$/);
  const rightMatch = right.match(/^(.*)-(\d+)$/);
  if (leftMatch && rightMatch && leftMatch[1] === rightMatch[1]) {
    const numericDifference = Number(leftMatch[2]) - Number(rightMatch[2]);
    if (numericDifference !== 0) return numericDifference;
  }
  return left.localeCompare(right);
}

export function getProviderInstanceColorStyle(
  provider: core.ProviderInfo,
  providers: core.ProviderInfo[],
): CSSProperties | undefined {
  const instanceIds = providers
    .filter((candidate) => candidate.id === provider.id)
    .map(instanceKey)
    .sort(compareInstanceIds);
  if (instanceIds.length <= 1) return undefined;

  const position = instanceIds.indexOf(instanceKey(provider));
  if (position <= 0) return undefined;
  return { filter: INSTANCE_COLOR_FILTERS[position % INSTANCE_COLOR_FILTERS.length] };
}
