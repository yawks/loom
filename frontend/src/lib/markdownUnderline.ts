interface MarkdownNode {
  type: string;
  value?: string;
  tagName?: string;
  properties?: Record<string, unknown>;
  children?: MarkdownNode[];
}

// Interpret only the canonical underline extension, after Markdown has parsed
// emphasis across its boundaries (for example **<u>bold underline</u>**).
// Arbitrary HTML and attributes remain subject to the renderer's defaults.
export function rehypeCanonicalUnderline() {
  return (tree: MarkdownNode) => {
    const visit = (node: MarkdownNode) => {
      if (!node.children) return;
      node.children.forEach(visit);
      const children: MarkdownNode[] = [];
      const stack: MarkdownNode[][] = [children];
      for (const child of node.children) {
        if (child.type === "raw" && /^<u>$/i.test(child.value ?? "")) {
          const underline: MarkdownNode = {
            type: "element", tagName: "u", properties: {}, children: [],
          };
          stack[stack.length - 1].push(underline);
          stack.push(underline.children!);
        } else if (child.type === "raw" && /^<\/u>$/i.test(child.value ?? "") && stack.length > 1) {
          stack.pop();
        } else {
          stack[stack.length - 1].push(child);
        }
      }
      node.children = children;
    };
    visit(tree);
  };
}
