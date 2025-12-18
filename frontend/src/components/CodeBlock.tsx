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
    
    return { codeString, isActuallyBlock };
  }, [children, inline]);

  const { codeString, isActuallyBlock } = codeAnalysis;

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
    <div ref={containerRef} className="my-2 rounded-md overflow-x-auto max-w-full">
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
        {children}
      </SyntaxHighlighter>
    </div>
  );
});

