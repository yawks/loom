interface MarkdownNode {
  type: string;
  value?: string;
  tagName?: string;
  properties?: Record<string, unknown>;
  children?: MarkdownNode[];
}

const SAFE_RICH_COLOR = /^(?:#[0-9a-f]{3,8}|(?:rgb|rgba|hsl|hsla)\([0-9.,% ]+\)|[a-z]+)$/i;

function richTextStyle(attributes: Record<string, string>): Record<string, string> {
  const style: Record<string, string> = {};
  const color = attributes["color"]?.trim();
  const background = attributes["background"]?.trim();
  if (color && SAFE_RICH_COLOR.test(color)) style.color = color;
  if (background && SAFE_RICH_COLOR.test(background)) style.backgroundColor = background;

  const size = attributes["size"]?.trim().match(/^([0-9]+(?:\.[0-9]+)?)(px|pt|em|rem|%)$/i);
  if (size) {
    const value = Number(size[1]);
    const unit = size[2].toLowerCase();
    const limits: Record<string, [number, number]> = {
      px: [8, 48], pt: [6, 36], em: [0.5, 3], rem: [0.5, 3], "%": [50, 300],
    };
    const [minimum, maximum] = limits[unit];
    style.fontSize = `${Math.min(maximum, Math.max(minimum, value))}${unit}`;
  }
  if (attributes["underline"] === "true") style.textDecorationLine = "underline";
  return style;
}

// Interpret the canonical style extension inside the parsed Markdown tree.
// Keeping one document preserves paragraphs, lists and emphasis across styles.
// Only validated presentation attributes are exposed; arbitrary HTML stays inert.
export function rehypeCanonicalStyle() {
  return (tree: MarkdownNode) => {
    const visit = (node: MarkdownNode) => {
      if (!node.children) return;
      node.children.forEach(visit);
      const children: MarkdownNode[] = [];
      const stack: MarkdownNode[][] = [children];
      for (const child of node.children) {
        const opening = child.type === "raw" && /^<loom-style\b([^<>]*)>$/i.exec(child.value ?? "");
        if (opening) {
          const attributes: Record<string, string> = {};
          for (const match of opening[1].matchAll(/([a-z-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')/gi)) {
            attributes[match[1].toLowerCase()] = match[2] ?? match[3];
          }
          const style = richTextStyle(attributes);
          const css = Object.entries(style).map(([key, value]) =>
            `${key.replace(/[A-Z]/g, (letter) => `-${letter.toLowerCase()}`)}:${value}`
          ).join(";");
          const span: MarkdownNode = {
            type: "element", tagName: "span", properties: { style: css }, children: [],
          };
          stack[stack.length - 1].push(span);
          stack.push(span.children!);
        } else if (child.type === "raw" && /^<\/loom-style\s*>$/i.test(child.value ?? "") && stack.length > 1) {
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
