import type { Root, Blockquote } from "mdast";

// Markdown collapses consecutive blank lines. Message bodies retain them so
// display and editing share the same number of visual line breaks.
export function remarkCanonicalBlankLines() {
  return (tree: Root) => {
    const visit = (node: Root | Blockquote) => {
      node.children.forEach((child, index) => {
        const previous = node.children[index - 1];
        if (child.type === "paragraph" && previous?.type === "paragraph") {
          const gap = (child.position?.start.line ?? 0) - (previous.position?.end.line ?? 0);
          for (let line = 1; line < gap; line += 1) {
            child.children.unshift({ type: "break" });
          }
        }
        if (child.type === "blockquote") visit(child);
      });
    };
    visit(tree);
  };
}

interface MarkdownNode {
  type: string;
  value?: string;
  tagName?: string;
  properties?: Record<string, unknown>;
  children?: MarkdownNode[];
}

// Canonical Markdown uses <br> for breaks inside table cells. Accept only
// this inert tag, keeping all other raw HTML subject to the renderer's defaults.
export function rehypeCanonicalBreaks() {
  return (tree: MarkdownNode) => {
    const visit = (node: MarkdownNode) => {
      if (node.type === "raw" && /^<br\s*\/?\s*>$/i.test(node.value ?? "")) {
        node.type = "element";
        node.tagName = "br";
        node.properties = {};
        node.children = [];
        delete node.value;
      }
      node.children?.forEach(visit);
    };
    visit(tree);
  };
}
