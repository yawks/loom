import { unified } from "unified";
import remarkParse from "remark-parse";
import type { RootContent } from "mdast";

const parser = unified().use(remarkParse);

function codeRanges(text: string): Array<[number, number]> {
  const ranges: Array<[number, number]> = [];
  const visit = (node: RootContent) => {
    if (node.type === "code" || node.type === "inlineCode") {
      const start = node.position?.start.offset;
      const end = node.position?.end.offset;
      if (start !== undefined && end !== undefined) ranges.push([start, end]);
    } else if ("children" in node) {
      node.children.forEach(visit);
    }
  };
  parser.parse(text).children.forEach(visit);
  return ranges;
}

/** Transform prose while leaving fenced, indented and inline code untouched. */
export function outsideMarkdownCode(text: string, transform: (text: string) => string): string {
  let offset = 0;
  let result = "";
  for (const [start, end] of codeRanges(text)) {
    result += transform(text.slice(offset, start)) + text.slice(start, end);
    offset = end;
  }
  return result + transform(text.slice(offset));
}

// Legacy cached Markdown sometimes escaped an entire pasted code block.
// Only paired, standalone escaped fences qualify; inline examples stay literal.
export function repairLegacyCodeFences(text: string): string {
  const ranges = codeRanges(text);
  return text.replace(
    /(^|\n)[ \t]{0,3}\\`\\`\\`([\w+-]*)[ \t]*\r?\n([\s\S]*?)\r?\n[ \t]{0,3}\\`\\`\\`[ \t]*(?=\r?\n|$)/g,
    (match, prefix, language, code: string, offset: number) => {
      const start = offset + prefix.length;
      if (ranges.some(([from, to]) => start >= from && start < to)) return match;
      const literal = code.replace(/\\([!"#$%&'()*+,\-./:;<=>?@[\]\\^_`{|}~])/g, "$1");
      const fence = "~".repeat(Math.max(3, ...Array.from(literal.matchAll(/~+/g), (match) => match[0].length + 1)));
      return `${prefix}${fence}${language}\n${literal}\n${fence}`;
    },
  );
}
