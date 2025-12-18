import { Check, ChevronDown, ChevronUp, Copy } from "lucide-react";
import { memo, useEffect, useMemo, useRef, useState } from "react";
import {
  oneDark,
  oneLight,
} from "react-syntax-highlighter/dist/esm/styles/prism";

import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";

interface CodeBlockProps {
  children: string;
  className?: string;
  inline?: boolean;
  isFromMe?: boolean;
}

/**
 * Component to render code blocks with syntax highlighting.
 * Automatically detects the language from the className (e.g., "language-javascript").
 * Falls back to auto-detection if no language is specified.
 */
export const CodeBlock = memo(function CodeBlock({
  children,
  className,
  inline = false,
  isFromMe = false,
}: CodeBlockProps) {
  const [isDark, setIsDark] = useState(() => {
    // Initialize with current theme on mount
    return document.documentElement.classList.contains("dark");
  });
  
  // Track if code block is visible for lazy rendering
  const [isVisible, setIsVisible] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  
  // Track if code is expanded (for long code blocks)
  const [isExpanded, setIsExpanded] = useState(false);
  
  // Track if copy was successful
  const [copied, setCopied] = useState(false);

  // Detect dark mode - only update when actually changing
  useEffect(() => {
    const checkDarkMode = () => {
      const isDarkMode = document.documentElement.classList.contains("dark");
      setIsDark((prev) => {
        // Only update if actually different to avoid unnecessary re-renders
        if (prev !== isDarkMode) {
          return isDarkMode;
        }
        return prev;
      });
    };

    // Listen for theme changes
    const observer = new MutationObserver(checkDarkMode);
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });

    return () => observer.disconnect();
  }, []);
  
  // Lazy render code blocks only when visible (IntersectionObserver)
  useEffect(() => {
    if (!containerRef.current) return;
    
    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            setIsVisible(true);
            // Once visible, stop observing (we don't need to hide it again)
            observer.disconnect();
          }
        });
      },
      {
        // Start loading when code block is 200px away from viewport
        rootMargin: "200px",
        threshold: 0.01,
      }
    );

    observer.observe(containerRef.current);

    return () => observer.disconnect();
  }, []);

  // Extract language from className (e.g., "language-javascript" -> "javascript")
  const match = /language-(\w+)/.exec(className || "");
  let language = match ? match[1] : "";

  // Memoize code analysis to avoid recalculating on every render
  const codeAnalysis = useMemo(() => {
    const codeString = String(children);
    const hasMultipleLines = codeString.includes("\n");
    const isLongCode = codeString.length > 50;
    const isActuallyBlock = !inline || hasMultipleLines || isLongCode;
    
    // Count lines in the code
    const lineCount = codeString.split("\n").length;
    const exceedsMaxLines = lineCount > 20;
    
    return { codeString, isActuallyBlock, lineCount, exceedsMaxLines };
  }, [children, inline]);

  const { codeString, isActuallyBlock, lineCount, exceedsMaxLines } = codeAnalysis;
  
  // Handle copy to clipboard
  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(codeString);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (err) {
      console.error("Failed to copy code:", err);
    }
  };
  
  // Get visible code (first 20 lines if collapsed)
  const visibleCode = useMemo(() => {
    if (!exceedsMaxLines || isExpanded) {
      return codeString;
    }
    const lines = codeString.split("\n");
    return lines.slice(0, 20).join("\n");
  }, [codeString, exceedsMaxLines, isExpanded]);

  // Memoize language detection to avoid recalculating
  const detectedLanguage = useMemo(() => {
    if (language && language !== "**" && language !== "*") {
      return language;
    }

    const code = codeString;
    const codeLower = code.toLowerCase();
    
    // JavaScript detection - check for various patterns
    const isJavaScript =
      codeLower.includes("function") ||
      codeLower.includes("const ") ||
      codeLower.includes("let ") ||
      codeLower.includes("var ") ||
      codeLower.includes("=>") ||
      code.includes("/**") || // JSDoc comment
      code.includes("//") || // Single line comment
      codeLower.includes("return ") ||
      codeLower.includes("if (") ||
      codeLower.includes("for (");
    
    if (isJavaScript) {
      return "javascript";
    } else if (codeLower.includes("def ") || codeLower.includes("import ")) {
      return "python";
    } else if (
      codeLower.includes("package ") ||
      codeLower.includes("func ") ||
      codeLower.includes(":=")
    ) {
      return "go";
    } else if (codeLower.includes("public class") || codeLower.includes("private ")) {
      return "java";
    } else if (
      codeLower.includes("<?php") ||
      codeLower.includes("<?=") ||
      codeLower.includes("<?")
    ) {
      return "php";
    } else {
      return "text";
    }
  }, [language, codeString]);

  // Inline code (single backtick) - but only if truly inline
  if (!isActuallyBlock) {
    return (
      <code
        className={
          isFromMe
            ? "bg-white/20 px-1 py-0.5 rounded text-sm font-mono"
            : "bg-muted px-1 py-0.5 rounded text-sm font-mono"
        }
      >
        {children}
      </code>
    );
  }

  // Select theme based on dark mode
  const theme = isDark ? oneDark : oneLight;

  // Code block (triple backticks)
  // Show placeholder until visible for lazy loading
  if (!isVisible) {
    return (
      <div
        ref={containerRef}
        className="my-2 rounded-md overflow-x-auto max-w-full"
      >
        <pre
          className={
            isDark
              ? "bg-gray-900 text-gray-100 p-4 rounded-md text-sm font-mono"
              : "bg-gray-100 text-gray-900 p-4 rounded-md text-sm font-mono"
          }
        >
          <code>{children}</code>
        </pre>
      </div>
    );
  }
  
  return (
    <div ref={containerRef} className="my-2 rounded-md overflow-hidden max-w-full relative group">
      {/* Clipboard button */}
      <button
        onClick={handleCopy}
        className={`absolute top-2 right-2 z-10 p-1.5 rounded-md transition-colors opacity-0 group-hover:opacity-100 ${
          isDark
            ? "bg-gray-800/80 hover:bg-gray-700/80 text-gray-300 hover:text-white"
            : "bg-gray-100/90 hover:bg-gray-200/90 text-gray-700 hover:text-gray-900"
        }`}
        title={copied ? "Copié !" : "Copier le code"}
        aria-label={copied ? "Copié !" : "Copier le code"}
      >
        {copied ? (
          <Check className="h-4 w-4" />
        ) : (
          <Copy className="h-4 w-4" />
        )}
      </button>
      
      <div 
        className={exceedsMaxLines && !isExpanded ? "relative overflow-hidden" : "relative"}
        style={exceedsMaxLines && !isExpanded ? { maxHeight: "480px" } : {}}
      >
        <SyntaxHighlighter
          language={detectedLanguage}
          style={theme}
          customStyle={{
            margin: 0,
            borderRadius: "0.375rem",
            fontSize: "0.875rem",
            whiteSpace: "pre",
            wordBreak: "normal",
            overflowWrap: "normal",
          }}
          showLineNumbers={false} // Disabled for performance
          wrapLines={false}
          wrapLongLines={false}
          PreTag="div"
        >
          {visibleCode}
        </SyntaxHighlighter>
        
        {/* Gradient overlay when collapsed */}
        {exceedsMaxLines && !isExpanded && (
          <div className="absolute bottom-0 left-0 right-0 h-16 bg-gradient-to-t from-background to-transparent pointer-events-none" />
        )}
      </div>
      
      {/* Expand/Collapse button for long code */}
      {exceedsMaxLines && (
        <div className="flex items-center justify-center mt-2">
          <button
            onClick={() => setIsExpanded(!isExpanded)}
            className="flex items-center gap-1.5 px-3 py-1.5 text-xs text-muted-foreground hover:text-foreground bg-muted/50 hover:bg-muted border border-border rounded-md transition-colors"
            aria-label={isExpanded ? "Réduire" : "Afficher plus"}
          >
            {isExpanded ? (
              <>
                <ChevronUp className="h-3.5 w-3.5" />
                <span>Réduire</span>
              </>
            ) : (
              <>
                <ChevronDown className="h-3.5 w-3.5" />
                <span>Afficher les {lineCount - 20} lignes restantes</span>
              </>
            )}
          </button>
        </div>
      )}
    </div>
  );
});

