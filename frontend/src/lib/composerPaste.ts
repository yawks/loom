// Adapt clipboard HTML to the editor's provider-neutral capabilities before
// ProseMirror discards unsupported nodes. Paragraph gaps belong in the document
// so they survive serialization and sending, not just in the editor's CSS.
export function normalizeComposerPasteHTML(html: string, features: ReadonlySet<string>): string {
  const doc = new DOMParser().parseFromString(html, "text/html");
  doc.querySelectorAll("script,style,meta,link").forEach((element) => element.remove());
  doc.querySelectorAll("h1,h2,h3,h4,h5,h6").forEach((heading) => {
    const paragraph = doc.createElement("p");
    const content = features.has("bold") ? doc.createElement("strong") : paragraph;
    content.append(...Array.from(heading.childNodes));
    if (content !== paragraph) paragraph.append(content);
    heading.replaceWith(paragraph);
  });
  doc.querySelectorAll("hr").forEach((rule) => {
    const paragraph = doc.createElement("p");
    paragraph.textContent = "—";
    rule.replaceWith(paragraph);
  });
  if (!features.has("table")) {
    doc.querySelectorAll("table").forEach((table) => {
      const rows = Array.from(table.rows).map((row) =>
        Array.from(row.cells).map((cell) => cell.textContent?.trim() ?? "").join("\t")).join("\n");
      const block = doc.createElement(features.has("code_block") ? "pre" : "p");
      block.textContent = rows;
      table.replaceWith(block);
    });
  }
  // Clipboard fragments commonly wrap their top-level blocks in divs.
  for (const wrapper of Array.from(doc.body.querySelectorAll("div")).reverse()) {
    if (wrapper.querySelector(":scope > p,:scope > ul,:scope > ol,:scope > table,:scope > pre")) {
      wrapper.replaceWith(...Array.from(wrapper.childNodes));
    }
  }
  const blocks = Array.from(doc.body.children);
  const isBlock = (node: Element) => /^(P|UL|OL|TABLE|PRE|BLOCKQUOTE)$/.test(node.tagName);
  const isEmpty = (node: Element) => node.tagName === "P" && !node.textContent?.trim() && !node.querySelector("img");
  for (let index = 1; index < blocks.length; index++) {
    const previous = blocks[index - 1];
    const current = blocks[index];
    if (isBlock(previous) && isBlock(current) && !isEmpty(previous) && !isEmpty(current)) {
      current.before(doc.createElement("p"));
    }
  }
  return doc.body.innerHTML;
}
