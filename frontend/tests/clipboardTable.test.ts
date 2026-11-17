import { strict as assert } from "node:assert";
import { test } from "node:test";
import { MarkdownManager } from "@tiptap/markdown";
import StarterKit from "@tiptap/starter-kit";
import { TableKit } from "@tiptap/extension-table";
import { clipboardTableEditorInput, clipboardTablesToMarkdown } from "../src/lib/clipboardTable.ts";
import { composerInputMarkdown, composerMarkdown } from "../src/lib/composerMarkdown.ts";

const manager = new MarkdownManager({ extensions: [StarterKit, TableKit] });
const serialize = manager.serialize.bind(manager);

test("pasted reporting table survives editor serialization and draft reload", () => {
  const pasted = "📊 Reporting Ops\n\nChiffres clés\n\n" +
    "Indicateur\tS45-46\tvs S43-44\n" +
    "🔴 Critique\t1 compte · 1,7 k€ MRR\t=\n" +
    "🟠 Warning\t11 comptes · 108,9 k€ MRR\t13 · 93 k€\n" +
    "Sorties d'alerte\t3 (Génération, Butagaz, INPI)\t2\n" +
    "Mises en production\t1 Zaion (Génération) · 1 Dydu (Avancial)\t2 · 1\n" +
    "Ventes additionnelles\t23,4 k€ (Urssaf)\t8,2 k€\n" +
    "Churn\t0\t1 · 1,33 k€\n\n🙋 CODIR : aucune nouvelle décision.";
  const markdown = clipboardTablesToMarkdown(pasted);
  const doc = manager.parse(clipboardTableEditorInput(pasted)!);
  const tables = doc.content?.filter((node) => node.type === "table") ?? [];
  assert.equal(tables.length, 1);
  assert.equal(tables[0].content?.length, 7);
  assert.ok(tables[0].content?.every((row) => row.content?.length === 3));
  const stored = composerMarkdown(doc, serialize);
  assert.match(stored, /\n\n\|/);
  assert.match(stored, /\|\n\n/);
  const reloaded = manager.parse(composerInputMarkdown(stored));
  assert.deepEqual(reloaded, doc);
  assert.ok(stored.includes("1 compte · 1,7 k€ MRR"));
  assert.ok(stored.includes("🙋 CODIR"));
  assert.ok(stored.startsWith("📊 Reporting Ops\n\nChiffres clés\n\n"));
  assert.deepEqual(stored.match(/\n{2,}/g), markdown.match(/\n{2,}/g));
});

test("paste retains blank-line runs in prose and on both sides of a table", () => {
  for (const count of [2, 3, 4, 5]) {
    const gap = "\n".repeat(count);
    const pasted = `Reporting${gap}En bref${gap}Chiffres clés${gap}A\tB\none\ttwo${gap}CODIR${gap}Actions`;
    const input = clipboardTableEditorInput(pasted);
    assert.notEqual(input, null);
    const doc = manager.parse(input!);
    const serialized = composerMarkdown(doc, serialize);
    assert.deepEqual(serialized.match(/\n{2,}/g), clipboardTablesToMarkdown(pasted).match(/\n{2,}/g));
    assert.ok(serialized.includes(`Reporting${gap}En bref`));
    assert.ok(serialized.includes(`Chiffres clés${gap}|`));
    assert.ok(serialized.includes(`|${gap}CODIR${gap}Actions`));
    for (let reload = 0; reload < 3; reload++) {
      assert.equal(composerMarkdown(manager.parse(composerInputMarkdown(serialized)), serialize), serialized);
    }
  }
});

test("tab indentation, code, irregular rows and ordinary prose are preserved", () => {
  for (const text of [
    "ordinary prose", "one\ttwo", "\tindented\n\tcode",
    "a\tb\nc\td\te", "```\na\tb\nc\td\n```", "~~~\na\tb\nc\td\n~~~",
  ]) assert.equal(clipboardTablesToMarkdown(text), text);
  assert.match(clipboardTablesToMarkdown("A\tB\nx | y\t<script>"), /x \\\| y/);
});
