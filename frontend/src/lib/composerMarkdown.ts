import type { JSONContent } from "@tiptap/core";

// Tiptap reads pairs of newlines as paragraph separators; adapt canonical
// blank lines on entry as well, without altering fenced code.
export function composerInputMarkdown(markdown: string): string {
  return markdown.replace(/^(`{3,}|~{3,})[^\n]*\n[\s\S]*?^\1[ \t]*(?=\n|$)|\n{2,}/gm,
    (match, fence: string | undefined) => fence ? match : "\n".repeat(match.length * 2));
}

// A composer paragraph is one visual line. Markdown's default two-newline
// block separator otherwise doubles empty lines when messages are sent.
export function composerMarkdown(doc: JSONContent, serialize: (node: JSONContent) => string): string {
  return (doc.content ?? []).map((node) => serialize({ type: "doc", content: [node] })).join("\n");
}
