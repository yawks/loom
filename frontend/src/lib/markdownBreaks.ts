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
