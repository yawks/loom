import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkBreaks from "remark-breaks";
import { rehypeCanonicalStyle } from "../src/lib/markdownStyle.ts";
import { rehypeCanonicalUnderline } from "../src/lib/markdownUnderline.ts";

const render = (children: string) => renderToStaticMarkup(createElement(ReactMarkdown, {
  remarkPlugins: [remarkGfm, remarkBreaks],
  rehypePlugins: [rehypeCanonicalUnderline, rehypeCanonicalStyle], children,
}));

test("highlighted messages retain paragraphs and line breaks around styled fragments", () => {
  const html = render('Introduction\n\n \n\n**Point important**\n\nAvant <loom-style background="#E5F18F">annotation</loom-style> après.\n\n<loom-style background="#E5F18F">Note\nSuite</loom-style>\n\nConclusion');
  assert.equal((html.match(/<p>/g) ?? []).length, 5);
  assert.match(html, /<p><strong>Point important<\/strong><\/p>/);
  assert.match(html, /Avant <span style="background-color:#E5F18F">annotation<\/span> après\.<\/p>/);
  assert.match(html, /Note<br\/>\nSuite/);
  assert.ok(html.endsWith('<p>Conclusion</p>'));
});

test("styles preserve list numbering, nested formatting and boundary spaces", () => {
  assert.equal(render('3. avant <loom-style color="red">**gras** et <u>souligné</u></loom-style> après'),
    '<ol start="3">\n<li>avant <span style="color:red"><strong>gras</strong> et <u>souligné</u></span> après</li>\n</ol>');
  assert.equal(render('**avant <loom-style color="red">rouge</loom-style> après**'),
    '<p><strong>avant <span style="color:red">rouge</span> après</strong></p>');
});

test("styles remain inert and code remains literal", () => {
  const html = render('<loom-style color="url(evil)" background="red" size="999px" onclick="evil()">text</loom-style>');
  assert.equal(html, '<p><span style="background-color:red;font-size:48px">text</span></p>');
  assert.ok(!render('<script>alert(1)</script>').includes('<script>'));
  assert.equal(render('`<loom-style color="red">literal</loom-style>`'),
    '<p><code>&lt;loom-style color=&quot;red&quot;&gt;literal&lt;/loom-style&gt;</code></p>');
});
