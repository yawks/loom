import { Children, cloneElement, isValidElement, type ReactElement, type ReactNode } from "react";

export function nodeText(node: ReactNode): string {
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(nodeText).join("");
  if (isValidElement<{ children?: ReactNode }>(node)) return nodeText(node.props.children);
  return "";
}

function appendSplitNode(lines: ReactNode[][], node: ReactNode): void {
  if (typeof node === "string" || typeof node === "number") {
    String(node).split("\n").forEach((part, index) => {
      if (index > 0) lines.push([]);
      if (part) lines[lines.length - 1].push(part);
    });
    return;
  }
  if (Array.isArray(node)) {
    node.forEach((child) => appendSplitNode(lines, child));
    return;
  }
  if (!isValidElement<{ children?: ReactNode }>(node)) return;

  const childLines: ReactNode[][] = [[]];
  Children.forEach(node.props.children, (child) => appendSplitNode(childLines, child));
  childLines.forEach((line, index) => {
    if (index > 0) lines.push([]);
    lines[lines.length - 1].push(cloneElement(node as ReactElement<{ children?: ReactNode }>, { key: index }, line));
  });
}

export function codeBlockLines(children: ReactNode): ReactNode[][] {
  const lines: ReactNode[][] = [[]];
  Children.forEach(children, (child) => appendSplitNode(lines, child));
  // Markdown adds one terminal newline to block code, not another numbered
  // line. Remove exactly that line, preserving intentional blank lines.
  if (lines.length > 1 && nodeText(lines[lines.length - 1]) === "") lines.pop();
  return lines;
}
