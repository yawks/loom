import { Check, ChevronDown, ChevronUp, Copy } from "lucide-react";
import { memo, useCallback, useLayoutEffect, useRef, useState } from "react";

const MAX_HEIGHT_PX = 480;

interface CodeBlockProps {
  children: React.ReactNode;
}

export const CodeBlock = memo(function CodeBlock({ children }: CodeBlockProps) {
  const wrapperRef = useRef<HTMLDivElement>(null);
  const [isTall, setIsTall] = useState(false);
  const [isExpanded, setIsExpanded] = useState(false);
  const [copied, setCopied] = useState(false);

  useLayoutEffect(() => {
    if (wrapperRef.current) {
      setIsTall(wrapperRef.current.scrollHeight > MAX_HEIGHT_PX);
    }
  }, [children]);

  const handleCopy = useCallback(async () => {
    const text = wrapperRef.current?.querySelector("code")?.textContent ?? "";
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // ignore
    }
  }, []);

  return (
    <div className="my-2 rounded-md overflow-hidden max-w-full relative group">
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
        className="relative overflow-hidden"
        style={isTall && !isExpanded ? { maxHeight: `${MAX_HEIGHT_PX}px` } : undefined}
      >
        <pre className="m-0">{children}</pre>
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
