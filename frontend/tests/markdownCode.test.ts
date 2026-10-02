import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ReactMarkdown from "react-markdown";
import { outsideMarkdownCode, repairLegacyCodeFences } from "../src/lib/markdownCode.ts";
import { transformUrls } from "../src/lib/utils.ts";
import { emojiShortcodePattern } from "../src/lib/emojiShortcodes.ts";

const render = (children: string) => renderToStaticMarkup(createElement(ReactMarkdown, { children }));

test("indented prompt and inline HTML samples survive prose normalization", () => {
  const prompt = 'Introduction\n\n            ### FORMAT\n\n            {"summary": "<h3>titre</h3><ul><li>élément</li></ul>"}\n\n            Utilise `<p>`, `<strong>` et :calendar:.';
  let prose = "";
  assert.equal(outsideMarkdownCode(prompt, (value) => { prose += value; return transformUrls(value); }), prompt);
  assert.ok(!prose.includes('<li>'));
  assert.match(render(prompt), /<pre><code> {8}### FORMAT/);
  const inline = 'Utilise `<p>` et `<ul><li>`.';
  assert.equal(outsideMarkdownCode(inline, transformUrls), inline);
  assert.match(render(inline), /<code>&lt;p&gt;<\/code>/);
});

test("code literals, URLs, emoji shortcodes and whitespace stay untouched", () => {
  for (const code of ['```html\n<div> :calendar: <https://example.com> </div>\n```', '~~~text\n:calendar: <u>literal</u>\n~~~', '    <p>:calendar:</p>', '`<p>:calendar:</p>`']) {
    const text = `:calendar:\n\n${code}`;
    const result = outsideMarkdownCode(text, (prose) => transformUrls(prose).replace(emojiShortcodePattern(), 'EMOJI'));
    assert.equal(result, `EMOJI\n\n${code}`);
  }
});

test("legacy escaped outer fences recover one block with nested fences preserved", () => {
  const escapedFence = "\\`".repeat(3);
  const text = `${escapedFence}  \nfunction detectMob() {\n  const nodes = \\[1\\];\n\n\`\`\`\n    dydu.ui.toggle(1);\n\`\`\`\n}\n${escapedFence}`;
  const repaired = repairLegacyCodeFences(text);
  assert.equal(repaired, '~~~\nfunction detectMob() {\n  const nodes = [1];\n\n```\n    dydu.ui.toggle(1);\n```\n}\n~~~');
  const html = render(repaired);
  assert.equal((html.match(/<pre>/g) ?? []).length, 1);
  assert.ok(html.includes('const nodes = [1];'));
  assert.ok(html.includes('dydu.ui.toggle(1);'));
  assert.equal(repairLegacyCodeFences(repaired), repaired);
  assert.equal(repairLegacyCodeFences(`Exemple ${escapedFence}json`), `Exemple ${escapedFence}json`);
  const literalExample = `~~~~\n${escapedFence}\ncode\n${escapedFence}\n~~~~`;
  assert.equal(repairLegacyCodeFences(literalExample), literalExample);
});
