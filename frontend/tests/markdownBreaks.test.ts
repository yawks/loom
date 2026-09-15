import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { rehypeCanonicalBreaks } from "../src/lib/markdownBreaks.ts";

test("canonical cell breaks preserve table structure without enabling arbitrary HTML", () => {
  const html = renderToStaticMarkup(createElement(ReactMarkdown, {
    remarkPlugins: [remarkGfm],
    rehypePlugins: [rehypeCanonicalBreaks],
    children: "| Priority<br>Approval | Owner |\n| --- | --- |\n| First<br/>Second | A |\n\n<script>alert(1)</script>",
  }));
  assert.match(html, /<th>Priority<br\/>Approval<\/th>/);
  assert.match(html, /<td>First<br\/>Second<\/td>/);
  assert.equal((html.match(/<th>/g) ?? []).length, 2);
  assert.equal((html.match(/<td>/g) ?? []).length, 2);
  assert.ok(!html.includes("<script>"));
});
