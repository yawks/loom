import { strict as assert } from "node:assert";
import { test } from "node:test";
import { MarkdownManager } from "@tiptap/markdown";
import StarterKit from "@tiptap/starter-kit";
import { composerInputMarkdown, composerMarkdown } from "../src/lib/composerMarkdown.ts";

const manager = new MarkdownManager({ extensions: [StarterKit] });
const serialize = manager.serialize.bind(manager);

test("loading a draft preserves canonical blank lines and fenced code", () => {
  for (const count of [2, 3, 4, 5]) {
    const markdown = `one${"\n".repeat(count)}two`;
    assert.equal(composerMarkdown(manager.parse(composerInputMarkdown(markdown)), serialize), markdown);
  }
  const code = "```\na\n\nb\n```";
  assert.equal(composerInputMarkdown(code), code);
});

test("composer preserves paragraph and empty-line counts", () => {
  for (const count of [0, 1, 2, 4]) {
    const paragraph = (text: string) => ({ type: "paragraph", content: text ? [{ type: "text", text }] : [] });
    assert.equal(composerMarkdown({ type: "doc", content: [paragraph("one"),
      ...Array.from({ length: count }, () => paragraph("")), paragraph("two")] }, serialize),
    `one${"\n".repeat(count + 1)}two`);
  }
});

test("composer keeps inline formatting, hard breaks and code", () => {
  const doc = { type: "doc", content: [
    { type: "paragraph", content: [{ type: "text", text: "hello", marks: [{ type: "italic" }] },
      { type: "hardBreak" }, { type: "text", text: "-> & <" }] },
    { type: "codeBlock", content: [{ type: "text", text: "a\n\nb &gt;" }] },
  ] };
  const output = composerMarkdown(doc, serialize);
  assert.ok(output.includes("*hello*"));
  assert.ok(output.includes("-&gt; &amp; &lt;"));
  assert.ok(output.includes("a\n\nb &gt;"));
});
