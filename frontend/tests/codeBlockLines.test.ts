import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createElement } from "react";
import { codeBlockLines, nodeText } from "../src/lib/codeBlockLines.ts";

test("Markdown terminal newline does not add an empty numbered line", () => {
  const command = "./build.sh --all --upload almalinux@www2.vicinorum-priv.com:./dist";
  for (const text of [command, command + "\n"]) {
    assert.deepEqual(codeBlockLines(text).map(nodeText), [command]);
  }
  assert.deepEqual(codeBlockLines("one\n\ntwo\n\n").map(nodeText), ["one", "", "two", ""]);
  assert.deepEqual(codeBlockLines("").map(nodeText), [""]);
});

test("highlighted spans retain syntax and intentional empty lines", () => {
  const children = [
    createElement("span", { className: "hljs-title", key: "title" }, "one\n"),
    createElement("span", { className: "hljs-string", key: "string" }, "\ntwo\n"),
  ];
  const lines = codeBlockLines(children);
  assert.deepEqual(lines.map(nodeText), ["one", "", "two"]);
  assert.equal((lines[0][0] as ReturnType<typeof createElement>).props.className, "hljs-title");
});
