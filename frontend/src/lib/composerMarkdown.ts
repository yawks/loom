import type { JSONContent } from "@tiptap/core";

// Tiptap reads pairs of newlines as paragraph separators; adapt canonical
// blank lines on entry as well, without altering fenced code.
export function composerInputMarkdown(markdown: string): string {
  return markdown.replace(/^(`{3,}|~{3,})[^\n]*\n[\s\S]*?^\1[ \t]*(?=\n|$)|\n{2,}/gm,
    (match, fence: string | undefined, offset: number) => {
      if (fence) return match;
      const before = markdown.slice(0, offset);
      const after = markdown.slice(offset + match.length);
      const beforeTable = /^\|[^\n]*\|\n\|[ \t:|-]+\|[ \t]*(?:\n|$)/.test(after);
      const afterTable = /(?:^|\n)\|[^\n]*\|[ \t]*$/.test(before) && /(?:^|\n)\|[ \t:|-]+\|[ \t]*\n/.test(before);
      // Table boundaries need one structural blank line, not an editable
      // empty paragraph. Only additional blank lines become empty paragraphs.
      return "\n".repeat((match.length - (beforeTable || afterTable ? 1 : 0)) * 2);
    });
}

// A composer paragraph is one visual line. Markdown's default two-newline
// block separator otherwise doubles empty lines when messages are sent.
export function composerMarkdown(doc: JSONContent, serialize: (node: JSONContent) => string): string {
  const nodes = doc.content ?? [];
  return nodes.map((node, index) => {
    const boundary = index === 0 ? "" : node.type === "table" || nodes[index - 1].type === "table" ? "\n\n" : "\n";
    const serialized = serialize({ type: "doc", content: [node] });
    return boundary + (node.type === "table" ? serialized.trim() : serialized);
  }).join("");
}
