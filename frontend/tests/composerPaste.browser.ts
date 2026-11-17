import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { Markdown } from "@tiptap/markdown";
import { normalizeComposerPasteHTML } from "../src/lib/composerPaste";
import { composerInputMarkdown, composerMarkdown } from "../src/lib/composerMarkdown";

// Run in a real browser (native DOMParser and ProseMirror clipboard parser),
// rather than mocking the very HTML-paste path covered by this regression.
export function checkRichProposalPaste() {
  const features = new Set(["bold", "bulleted_list", "numbered_list", "code_block"]);
  const fixture = "<h1>Proposition d’accompagnement : catalogue UGAP</h1>" +
    "<p>Nous proposons deux phases :</p><ul>" +
    "<li><p><strong>Phase 1, preuve de concept</strong> : faisabilité.</p></li>" +
    "<li><p><strong>Phase 2, industrialisation</strong> : intégration.</p></li></ul>" +
    "<hr><h2>Phase 1 : preuve de concept</h2><p>Validation de bout en bout.</p>" +
    "<h3>Périmètre</h3><ul><li><p>Conception du prompt :</p><ul>" +
    "<li><p>Recherche de produits</p></li><li><p>Comparaison des fiches</p></li></ul></li>" +
    "<li><p>Tests techniques</p></li></ul>" +
    "<h3>Estimation</h3><table><tr><th>Poste</th><th>Charge</th><th>Montant</th></tr>" +
    "<tr><td>Expertise technique</td><td>10 JH</td><td>10 000 € HT</td></tr></table>";
  const host = document.createElement("div");
  document.body.append(host);
  const editor = new Editor({
    element: host,
    extensions: [StarterKit.configure({ heading: false, horizontalRule: false }), Markdown],
    editorProps: { transformPastedHTML: (html) => normalizeComposerPasteHTML(html, features) },
  });
  try {
    const clipboardData = new DataTransfer();
    clipboardData.setData("text/html", fixture);
    clipboardData.setData("text/plain", "Proposition aplatie sans mise en forme");
    editor.view.dom.dispatchEvent(new ClipboardEvent("paste", { clipboardData, bubbles: true, cancelable: true }));
    const doc = editor.getJSON();
    const markdown = composerMarkdown(doc, (value) => editor.markdown!.serialize(value));
    const check = (valid: boolean, message: string) => { if (!valid) throw new Error(message + "\n" + markdown); };
    check(markdown.startsWith("**Proposition"), "heading must become bold");
    check(markdown.includes("\n\nNous proposons"), "paragraph gap lost");
    check(markdown.includes("- **Phase 1"), "bullet/inline emphasis lost");
    check(markdown.includes("  - Recherche de produits"), "nested list lost");
    check(markdown.includes("10 000 € HT"), "unsupported table content lost");
    check(editor.view.dom.querySelectorAll("ul").length === 3, "HTML lists were flattened");
    check(editor.view.dom.querySelectorAll("pre").length === 1, "table fallback was not preformatted");
    const reloaded = editor.markdown!.parse(composerInputMarkdown(markdown));
    check(composerMarkdown(reloaded, (value) => editor.markdown!.serialize(value)).trimEnd() === markdown.trimEnd(),
      "draft reload changed spacing/lists");
    const normalized = normalizeComposerPasteHTML("<p>One</p><p></p><p>Two</p>", features);
    check((normalized.match(/<p><\/p>/g) ?? []).length === 1, "explicit blank paragraph doubled");
    return { markdown, lists: 3, tableFallbacks: 1, blankParagraphs: doc.content?.filter((node) => node.type === "paragraph" && !node.content?.length).length };
  } finally {
    editor.destroy();
    host.remove();
  }
}
