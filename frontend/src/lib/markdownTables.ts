import { outsideMarkdownCode } from "./markdownCode.ts";

// Legacy rich-text conversions omitted the blank line before pipe tables.
// CommonMark can then swallow a whole table as a list's lazy continuation.
// Only repair an explicit pipe header followed by a matching GFM delimiter;
// fenced/indented code and literal pipes remain untouched.
export function repairLegacyTableBoundaries(markdown: string): string {
  return outsideMarkdownCode(markdown, (prose) => {
    const lines = prose.split("\n");
    const cells = (line: string) => line.trim().replace(/^\|/, "").replace(/\|$/, "").split(/(?<!\\)\|/);
    for (let index = lines.length - 1; index >= 2; index -= 1) {
      const separator = lines[index].trim();
      const header = lines[index - 1].trim();
      if (!/^\|[ \t]*:?-+:?[ \t]*(?:\|[ \t]*:?-+:?[ \t]*)+\|?$/.test(separator) ||
          !header.startsWith("|") || !header.endsWith("|") ||
          cells(header).length !== cells(separator).length ||
          lines[index - 2].trim() === "") continue;
      // A standalone bold caption can itself have been swallowed by the
      // preceding list. Isolate that caption together with its table.
      if (index >= 3 && /^\*\*[^*\n]+\*\*$/.test(lines[index - 2].trim()) &&
          lines[index - 3].trim() !== "") {
        lines.splice(index - 2, 0, "");
        index += 1;
      }
      lines.splice(index - 1, 0, "");
    }
    return lines.join("\n");
  });
}
