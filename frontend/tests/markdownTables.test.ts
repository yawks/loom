import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkBreaks from "remark-breaks";
import { repairLegacyTableBoundaries } from "../src/lib/markdownTables.ts";
import { remarkCanonicalBlankLines } from "../src/lib/markdownBreaks.ts";

test("reporting table following a list and bold caption renders as a table", () => {
  const table = "| Indicateur | S45-46 | vs S43-44 |\n| --- | --- | --- |\n" +
    "| 🔴 Critique | 1 compte · 1,7 k€ MRR | = |\n" +
    "| 🟠 Warning | 11 comptes · 108,9 k€ MRR | 13 · 93 k€ |\n" +
    "| Sorties d'alerte | 3 (Génération, Butagaz, INPI) | 2 |\n" +
    "| Mises en production | 1 Zaion · 1 Dydu | 2 · 1 |\n" +
    "| Ventes additionnelles | 23,4 k€ (Urssaf) | 8,2 k€ |\n" +
    "| Churn | 0 | 1 · 1,33 k€ |";
  const markdown = "**En bref**\n- Trois sorties d'alerte.\n- Dydu : +23,4 k€.\n**Chiffres clés**\n" +
    table + "\n\n**🙋 CODIR** : aucune nouvelle décision.";
  const repaired = repairLegacyTableBoundaries(markdown);
  assert.ok(repaired.includes("**Chiffres clés**\n\n| Indicateur"));
  const html = renderToStaticMarkup(createElement(ReactMarkdown, {
    remarkPlugins: [remarkGfm, remarkBreaks, remarkCanonicalBlankLines], children: repaired,
  }));
  assert.equal((html.match(/<table>/g) ?? []).length, 1);
  assert.equal((html.match(/<td>/g) ?? []).length, 18);
  assert.ok(html.includes("</ul>"));
  assert.ok(html.includes("<p><strong>Chiffres clés</strong></p>"));
  assert.ok(html.includes("<p><strong>🙋 CODIR</strong>"));
  assert.equal(repairLegacyTableBoundaries(repaired), repaired);
});

test("literal code and non-table pipes are not rewritten", () => {
  const table = "| A | B |\n| --- | --- |\n| x | y |";
  for (const text of [
    "prose\n| A | B |\n| not | a separator |",
    "prose\n| A |\n| --- | --- |",
    "```markdown\nprose\n" + table + "\n```",
    "~~~\nprose\n" + table + "\n~~~",
    "    prose\n" + table.split("\n").map((line) => "    " + line).join("\n"),
    "prose\n\n" + table,
  ]) assert.equal(repairLegacyTableBoundaries(text), text);
});
