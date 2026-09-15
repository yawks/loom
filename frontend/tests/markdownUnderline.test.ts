import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { rehypeCanonicalUnderline } from "../src/lib/markdownUnderline.ts";

const render = (children: string) => renderToStaticMarkup(createElement(ReactMarkdown, {
  remarkPlugins: [remarkGfm], rehypePlugins: [rehypeCanonicalUnderline], children,
}));

test("bold and underline combine in either nesting order without visible delimiters", () => {
  const title = "1/ Présentation de la Roadmap projets Accefil pour la MNH";
  assert.equal(render(`**<u>${title}</u>**`), `<p><strong><u>${title}</u></strong></p>`);
  assert.equal(render(`<u>**${title}**</u>`), `<p><u><strong>${title}</strong></u></p>`);
  assert.equal(render(`**avant <u>${title}</u> après**`),
    `<p><strong>avant <u>${title}</u> après</strong></p>`);
});

test("underline preserves Markdown structure, spacing, links and nested formatting", () => {
  assert.equal(render("avant <u>**gras** et *italique* et ~~barré~~</u> après"),
    "<p>avant <u><strong>gras</strong> et <em>italique</em> et <del>barré</del></u> après</p>");
  assert.equal(render("3. **<u>[lien](https://example.com)</u>**"),
    '<ol start="3">\n<li><strong><u><a href="https://example.com">lien</a></u></strong></li>\n</ol>');
});

test("code and escaped delimiters stay literal, arbitrary HTML stays inert", () => {
  assert.equal(render("`**<u>code</u>**`"), "<p><code>**&lt;u&gt;code&lt;/u&gt;**</code></p>");
  assert.equal(render("\\*\\*<u>literal</u>\\*\\*"), "<p>**<u>literal</u>**</p>");
  assert.ok(!render('<u onclick="alert(1)">text</u>').includes('<u onclick='));
  assert.ok(!render('<script>alert(1)</script>').includes('<script>'));
});
