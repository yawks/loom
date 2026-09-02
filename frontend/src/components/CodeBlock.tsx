import { Check, ChevronDown, ChevronUp, Copy } from "lucide-react";
import { Children, cloneElement, isValidElement, memo, type ReactElement, type ReactNode, useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import { cn } from "@/lib/utils";

const MAX_HEIGHT_PX = 480;

interface CodeBlockProps {
  children: React.ReactNode;
}

function nodeText(node: ReactNode): string {
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(nodeText).join("");
  if (isValidElement<{ children?: ReactNode }>(node)) return nodeText(node.props.children);
  return "";
}

type CodeElement = ReactElement<{ className?: string; children?: ReactNode }>;

function findCodeElement(node: ReactNode): CodeElement | undefined {
  for (const child of Children.toArray(node)) {
    if (!isValidElement<{ className?: string; children?: ReactNode }>(child)) continue;
    const className = child.props.className ?? "";
    if (child.type === "code" || /(?:^|\s)hljs(?:\s|$)/.test(className)) return child;
    const nested = findCodeElement(child.props.children);
    if (nested) return nested;
  }
  return undefined;
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

export const CodeBlock = memo(function CodeBlock({ children }: CodeBlockProps) {
  const wrapperRef = useRef<HTMLDivElement>(null);
  const [isTall, setIsTall] = useState(false);
  const [isExpanded, setIsExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const codeElement = findCodeElement(children);
  const codeText = useMemo(() => nodeText(codeElement?.props.children ?? children).replace(/\n$/, ""), [children, codeElement]);
  const codeLines = useMemo(() => {
    const lines: ReactNode[][] = [[]];
    Children.forEach(codeElement?.props.children ?? children, (child) => appendSplitNode(lines, child));
    return lines;
  }, [children, codeElement]);

  useLayoutEffect(() => {
    if (wrapperRef.current) {
      setIsTall(wrapperRef.current.scrollHeight > MAX_HEIGHT_PX);
    }
  }, [children]);

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(codeText);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // ignore
    }
  }, [codeText]);

  return (
    <div className="my-2 rounded-md overflow-hidden max-w-full min-w-0 relative group">
      <button
        onClick={handleCopy}
        className="absolute top-2 right-2 z-10 p-1.5 rounded-md transition-colors opacity-0 group-hover:opacity-100 bg-gray-100/90 hover:bg-gray-200/90 text-gray-700 hover:text-gray-900 dark:bg-gray-800/80 dark:hover:bg-gray-700/80 dark:text-gray-300 dark:hover:text-white"
        title={copied ? "Copié !" : "Copier le code"}
        aria-label={copied ? "Copié !" : "Copier le code"}
      >
        {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
      </button>

      <div
        ref={wrapperRef}
        className="relative max-w-full min-w-0 overflow-hidden"
        style={isTall && !isExpanded ? { maxHeight: `${MAX_HEIGHT_PX}px` } : undefined}
      >
        <pre className="m-0 max-w-full whitespace-normal py-2">
          <code className={cn(codeElement?.props.className, "!overflow-visible !p-0 leading-5")}>
            {codeLines.map((line, index) => (
              <span className="my-0 grid min-h-0 grid-cols-[3rem_minmax(0,1fr)] items-start py-0 leading-5" key={index}>
                <span
                  aria-hidden="true"
                  className="select-none border-r border-current/15 pr-3 text-right text-current/40"
                >
                  {index + 1}
                </span>
                <span className="my-0 min-w-0 whitespace-pre-wrap break-words py-0 pl-3 leading-5">{line.length > 0 ? line : " "}</span>
              </span>
            ))}
          </code>
        </pre>
        {isTall && !isExpanded && (
          <div className="absolute bottom-0 left-0 right-0 h-16 bg-gradient-to-t from-background to-transparent pointer-events-none" />
        )}
      </div>

      {isTall && (
        <div className="flex items-center justify-center mt-2">
          <button
            onClick={() => setIsExpanded((v) => !v)}
            className="flex items-center gap-1.5 px-3 py-1.5 text-xs text-muted-foreground hover:text-foreground bg-muted/50 hover:bg-muted border border-border rounded-md transition-colors"
          >
            {isExpanded ? (
              <><ChevronUp className="h-3.5 w-3.5" /><span>Réduire</span></>
            ) : (
              <><ChevronDown className="h-3.5 w-3.5" /><span>Afficher la suite</span></>
            )}
          </button>
        </div>
      )}
    </div>
  );
});
