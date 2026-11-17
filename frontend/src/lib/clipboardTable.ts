import { outsideMarkdownCode } from "./markdownCode.ts";
import { composerInputMarkdown } from "./composerMarkdown.ts";

// The editor needs the same blank-line adaptation on paste as when loading a
// draft. Parsing the converted Markdown directly collapses empty paragraphs.
export function clipboardTableEditorInput(text: string): string | null {
  const markdown = clipboardTablesToMarkdown(text);
  return markdown === text ? null : composerInputMarkdown(markdown);
}

// Plain-text copies of tables (spreadsheets, documents, web pages) use tabs.
// Convert only rectangular, multi-row blocks; ordinary prose and indentation
// must not be interpreted as a table.
export function clipboardTablesToMarkdown(text: string): string {
  return outsideMarkdownCode(text, (prose) => {
    const lines = prose.replace(/\r\n/g, "\n").split("\n");
    const result: string[] = [];
    const cell = (value: string) => value.trim().replace(/\\/g, "\\\\")
      .replace(/([|*_`[\]<>])/g, "\\$1");
    for (let index = 0; index < lines.length;) {
      const row = lines[index].split("\t");
      if (row.length < 2 || !row[0].trim()) {
        result.push(lines[index++]);
        continue;
      }
      let end = index + 1;
      while (end < lines.length && lines[end].includes("\t") && lines[end].split("\t").length === row.length) end++;
      if (end - index < 2) {
        result.push(lines[index++]);
        continue;
      }
      if (result.length > 0 && result[result.length - 1].trim()) result.push("");
      result.push("| " + row.map(cell).join(" | ") + " |");
      result.push("| " + row.map(() => "---").join(" | ") + " |");
      for (let item = index + 1; item < end; item++) {
        result.push("| " + lines[item].split("\t").map(cell).join(" | ") + " |");
      }
      if (end < lines.length && lines[end].trim()) result.push("");
      index = end;
    }
    return result.join("\n");
  });
}
