import { useMemo, useState, type KeyboardEvent as ReactKeyboardEvent, type RefObject } from "react";
import { useTranslation } from "react-i18next";

import { useAppStore } from "@/lib/store";
import { RichTextComposer } from "./RichTextComposer";

interface MessageEditComposerProps {
  providerInstanceId?: string;
  value: string;
  onChange: (value: string) => void;
  onSave: () => void;
  onCancel: () => void;
  onBlur?: (relatedTarget: EventTarget | null) => void;
  onMarkdownKeyDown?: (event: ReactKeyboardEvent<HTMLTextAreaElement>) => void;
  textareaRef?: RefObject<HTMLTextAreaElement | null>;
}

export function MessageEditComposer({
  providerInstanceId,
  value,
  onChange,
  onSave,
  onCancel,
  onBlur,
  onMarkdownKeyDown,
  textareaRef,
}: MessageEditComposerProps) {
  const { t } = useTranslation();
  const capabilities = useAppStore((state) => state.capabilities);
  const [mode, setMode] = useState<"wysiwyg" | "markdown">("wysiwyg");
  const features = useMemo(() => new Set(
    (providerInstanceId ? capabilities[providerInstanceId]?.messageFormatting : "")
      ?.split(",").filter(Boolean) ?? []
  ), [capabilities, providerInstanceId]);

  const handleKeyDown = (event: KeyboardEvent) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      onSave();
    } else if (event.key === "Escape") {
      event.preventDefault();
      onCancel();
    }
  };

  return (
    <div className="flex flex-col gap-1">
      <button
        type="button"
        className="self-end rounded px-2 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
        onClick={() => setMode((current) => current === "wysiwyg" ? "markdown" : "wysiwyg")}
      >
        {mode === "wysiwyg" ? t("composer_mode_markdown") : t("composer_mode_wysiwyg")}
      </button>
      {mode === "wysiwyg" ? (
        <RichTextComposer
          value={value}
          onChange={onChange}
          onCursorPrefixChange={() => undefined}
          onKeyDown={handleKeyDown}
          onPaste={() => undefined}
          onMount={(element) => element?.focus()}
          onBlur={(event) => onBlur?.(event.relatedTarget)}
          placeholder={t("edit_message")}
          features={features}
        />
      ) : (
        <textarea
          ref={textareaRef}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          onKeyDown={(event) => {
            onMarkdownKeyDown?.(event);
            if (event.defaultPrevented) return;
            if (event.key === "Enter" && !event.shiftKey) {
              event.preventDefault();
              onSave();
            } else if (event.key === "Escape") {
              onCancel();
            }
          }}
          onBlur={(event) => onBlur?.(event.relatedTarget)}
          className="min-h-20 w-full resize-y rounded-md border border-input bg-background px-3 py-2 text-foreground whitespace-pre-wrap"
          rows={Math.min(8, Math.max(3, value.split("\n").length))}
          autoFocus
        />
      )}
    </div>
  );
}
