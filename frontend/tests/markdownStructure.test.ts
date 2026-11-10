import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkBreaks from "remark-breaks";
import { remarkCanonicalBlankLines } from "../src/lib/markdownBreaks.ts";

test("numbered headings and analysis paragraphs stay outside formatted tables", () => {
  const markdown = "Analyse\n- Trois ruptures.\n\n" + [1, 2, 3].map((index) =>
    `---\n\n### ${index}. Analyse\n` +
    "|  | Référence | Aujourd'hui |\n| --- | --- | --- |\n" +
    "| Volume | 1 752 | **3 213** |\n| Taux | 92 % | **80 %** |\n\n" +
    "**Lecture**\n- **Le volume augmente.**\n"
  ).join("\n");
  const html = renderToStaticMarkup(createElement(ReactMarkdown, {
    remarkPlugins: [remarkGfm, remarkBreaks, remarkCanonicalBlankLines], children: markdown,
  }));
  assert.equal((html.match(/<table>/g) ?? []).length, 3);
  assert.equal((html.match(/<h3>/g) ?? []).length, 3);
  assert.equal((html.match(/<hr\/>/g) ?? []).length, 3);
  assert.equal((html.match(/<tbody><tr>/g) ?? []).length, 3);
  assert.equal((html.match(/<td>/g) ?? []).length, 18);
  assert.equal((html.match(/<p><strong>Lecture<\/strong><\/p>/g) ?? []).length, 3);
  assert.ok(html.includes("<td><strong>3 213</strong></td>"));
  assert.ok(html.includes("<h3>2. Analyse</h3>"));
  assert.ok(!html.includes("<ol>"));
});
