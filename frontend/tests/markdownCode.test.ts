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

test("legacy escaped commands recover filenames and remote targets without link markup", () => {
  const fence = "\\`".repeat(3);
  const command = "./build.sh --all --upload almalinux@www2.vicinorum-priv.com:./dist";
  const linked = "./[build.sh](http://build.sh) --all --upload [almalinux@www2.vicinorum-priv.com](mailto:almalinux@www2.vicinorum-priv.com):./dist";
  const repaired = repairLegacyCodeFences(`${fence}  \n${linked}  \n${fence}`);
  assert.equal(repaired, `~~~\n${command}  \n~~~`);
  const html = render(repaired);
  assert.ok(html.includes(command));
  assert.ok(!html.includes("<a "));
  assert.ok(!html.includes("[build.sh]"));
  assert.equal(repairLegacyCodeFences(repaired), repaired);
  for (const text of [linked, `\`${linked}\``, `~~~\n${linked}\n~~~`]) {
    assert.equal(repairLegacyCodeFences(text), text);
  }
  const example = "[documentation](https://example.com)";
  assert.equal(repairLegacyCodeFences(`${fence}\n${example}\n${fence}`), `~~~\n${example}\n~~~`);
});
