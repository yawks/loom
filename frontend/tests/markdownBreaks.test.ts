import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkBreaks from "remark-breaks";
import { rehypeCanonicalBreaks, remarkCanonicalBlankLines } from "../src/lib/markdownBreaks.ts";

test("message rendering retains each blank line without changing code or lists", () => {
  for (const count of [1, 2, 3, 5]) {
    const html = renderToStaticMarkup(createElement(ReactMarkdown, {
      remarkPlugins: [remarkGfm, remarkBreaks, remarkCanonicalBlankLines],
      children: `one${"\n".repeat(count)}two`,
    }));
    assert.equal((html.match(/<br\/>/g) ?? []).length, count === 1 ? 1 : count - 1);
  }
  const html = renderToStaticMarkup(createElement(ReactMarkdown, {
    remarkPlugins: [remarkGfm, remarkBreaks, remarkCanonicalBlankLines],
    children: "```\none\n\n\ntwo\n```\n\n- first\n- second",
  }));
  assert.ok(html.includes("one\n\n\ntwo"));
  assert.ok(!html.includes("<br/>"));
  assert.equal((html.match(/<li>/g) ?? []).length, 2);
});

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
