import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { emojiShortcodePattern } from "../src/lib/emojiShortcodes.ts";

test("a colon before a URL does not consume the scheme as an emoji", () => {
  for (const prefix of ["voir là :", "**voir** :", ""]) {
    const url = "https://www.service-public.gouv.fr/particuliers/vosdroits/R53491";
    const input = prefix + url;
    assert.deepEqual([...input.matchAll(emojiShortcodePattern())], []);
    const processed = input.replace(emojiShortcodePattern(), "EMOJI");
    assert.equal(processed, input);
    const html = renderToStaticMarkup(createElement(ReactMarkdown, {
      remarkPlugins: [remarkGfm], children: processed,
    }));
    assert.ok(html.includes(`<a href="${url}">${url}</a>`));
  }
  assert.deepEqual([..."lien :http://example.com".matchAll(emojiShortcodePattern())], []);
});

test("real shortcodes still match and times remain intact", () => {
  const input = "À 12:42:05 :smile: puis :custom_emoji: et :https://example.com";
  assert.deepEqual([...input.matchAll(emojiShortcodePattern())].map((match) => match[1]),
    ["smile", "custom_emoji"]);
});
