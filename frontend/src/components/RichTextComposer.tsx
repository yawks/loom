import { EditorContent, useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { Markdown } from "@tiptap/markdown";
import Link from "@tiptap/extension-link";
import Underline from "@tiptap/extension-underline";
import { BackgroundColor, Color, FontSize, TextStyle } from "@tiptap/extension-text-style";
import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";

const SAFE_COLOR = /^(?:#[0-9a-f]{3,8}|(?:rgb|rgba|hsl|hsla)\([0-9.,% ]+\)|[a-z]+)$/i;
const SAFE_SIZE = /^[0-9]+(?:\.[0-9]+)?(?:px|pt|em|rem|%)$/i;

const canonicalToEditorMarkdown = (markdown: string): string => markdown
  .replace(/<loom-style\b([^<>]*)>/gi, (_tag, rawAttributes: string) => {
    const styles: string[] = [];
    for (const match of rawAttributes.matchAll(/(color|background|size)\s*=\s*(?:"([^"]*)"|'([^']*)')/gi)) {
      const name = match[1].toLowerCase();
      const value = (match[2] ?? match[3] ?? "").trim();
      if ((name === "color" || name === "background") && SAFE_COLOR.test(value)) {
        styles.push(`${name === "background" ? "background-color" : "color"}:${value}`);
      } else if (name === "size" && SAFE_SIZE.test(value)) {
        styles.push(`font-size:${value}`);
      }
    }
    return styles.length > 0 ? `<span style="${styles.join(";")}">` : "";
  })
  .replace(/<\/loom-style\s*>/gi, "</span>");

const CanonicalUnderline = Underline.extend({
  renderMarkdown(node, helpers) {
    return `<u>${helpers.renderChildren(node)}</u>`;
  },
});

const CanonicalTextStyle = TextStyle.extend({
  renderMarkdown(node, helpers) {
    const attributes: string[] = [];
    const color = node.attrs?.color;
    const background = node.attrs?.backgroundColor;
    const size = node.attrs?.fontSize;
    if (typeof color === "string" && SAFE_COLOR.test(color)) attributes.push(`color="${color}"`);
    if (typeof background === "string" && SAFE_COLOR.test(background)) attributes.push(`background="${background}"`);
    if (typeof size === "string" && SAFE_SIZE.test(size)) attributes.push(`size="${size}"`);
    const content = helpers.renderChildren(node);
    return attributes.length > 0 ? `<loom-style ${attributes.join(" ")}>${content}</loom-style>` : content;
  },
});

export interface RichTextComposerHandle {
  focus: () => void;
  insertText: (text: string) => void;
  hasSelection: () => boolean;
  isAtStart: () => boolean;
  isAtEnd: () => boolean;
  toggleBold: () => void;
  toggleItalic: () => void;
  toggleUnderline: () => void;
  toggleStrike: () => void;
  toggleCode: () => void;
  toggleCodeBlock: () => void;
  toggleBulletList: () => void;
  toggleOrderedList: () => void;
  setLink: (href: string) => void;
  setColor: (color: string) => void;
  setBackgroundColor: (color: string) => void;
  setFontSize: (size: string) => void;
}

interface RichTextComposerProps {
  value: string;
  disabled?: boolean;
  placeholder: string;
  onChange: (markdown: string) => void;
  onCursorPrefixChange: (prefix: string) => void;
  onKeyDown: (event: KeyboardEvent) => void;
  onPaste: (event: React.ClipboardEvent<HTMLElement>) => void;
  onHeightChange?: () => void;
  onMount?: (element: HTMLElement | null) => void;
  features: ReadonlySet<string>;
}

export const RichTextComposer = forwardRef<RichTextComposerHandle, RichTextComposerProps>(function RichTextComposer({
  value,
  disabled,
  placeholder,
  onChange,
  onCursorPrefixChange,
  onKeyDown,
  onPaste,
  onHeightChange,
  onMount,
  features,
}, ref) {
  const emittedValue = useRef(value);
  const callbacks = useRef({ onChange, onCursorPrefixChange, onHeightChange });
  const onKeyDownRef = useRef(onKeyDown);
  useEffect(() => {
    callbacks.current = { onChange, onCursorPrefixChange, onHeightChange };
    onKeyDownRef.current = onKeyDown;
  }, [onChange, onCursorPrefixChange, onHeightChange, onKeyDown]);

  const reportCursor = (editor: NonNullable<ReturnType<typeof useEditor>>) => {
    const prefix = editor.state.doc.textBetween(0, editor.state.selection.from, "\n", "\n");
    callbacks.current.onCursorPrefixChange(prefix);
  };

  const editor = useEditor({
    immediatelyRender: false,
    extensions: [
      StarterKit.configure({
        heading: false,
        horizontalRule: false,
        blockquote: false,
        bold: features.has("bold") ? {} : false,
        italic: features.has("italic") ? {} : false,
        strike: features.has("strikethrough") ? {} : false,
        code: features.has("inline_code") ? {} : false,
        codeBlock: features.has("code_block") ? {} : false,
        bulletList: features.has("bulleted_list") ? {} : false,
        orderedList: features.has("numbered_list") ? {} : false,
        listItem: features.has("bulleted_list") || features.has("numbered_list") ? {} : false,
        link: false,
        underline: false,
      }),
      ...(features.has("underline") ? [CanonicalUnderline] : []),
      ...(features.has("link") ? [Link.configure({ openOnClick: false, autolink: true })] : []),
      ...(features.has("text_color") || features.has("background_color") || features.has("font_size")
        ? [CanonicalTextStyle] : []),
      ...(features.has("text_color") ? [Color] : []),
      ...(features.has("background_color") ? [BackgroundColor] : []),
      ...(features.has("font_size") ? [FontSize] : []),
      Markdown.configure({ markedOptions: { gfm: true, breaks: true } }),
    ],
    content: canonicalToEditorMarkdown(value),
    contentType: "markdown",
    editable: !disabled,
    editorProps: {
      attributes: {
        class: "rich-message-composer min-h-[38px] max-h-[198px] overflow-y-auto px-3 py-2 text-sm leading-5 outline-none",
        "aria-label": placeholder,
        "data-placeholder": placeholder,
      },
      handleKeyDown: (_view, event) => {
        onKeyDownRef.current(event);
        return event.defaultPrevented;
      },
    },
    onUpdate: ({ editor: currentEditor }) => {
      const markdown = currentEditor.getMarkdown();
      emittedValue.current = markdown;
      callbacks.current.onChange(markdown);
      reportCursor(currentEditor);
      requestAnimationFrame(() => callbacks.current.onHeightChange?.());
    },
    onSelectionUpdate: ({ editor: currentEditor }) => reportCursor(currentEditor),
  });

  useEffect(() => {
    if (!editor || value === emittedValue.current) return;
    emittedValue.current = value;
    editor.commands.setContent(canonicalToEditorMarkdown(value), { contentType: "markdown", emitUpdate: false });
  }, [editor, value]);

  useEffect(() => {
    editor?.setEditable(!disabled);
  }, [disabled, editor]);

  const onMountRef = useRef(onMount);
  useEffect(() => {
    onMountRef.current = onMount;
  }, [onMount]);
  useEffect(() => {
    const element = editor?.view.dom ?? null;
    onMountRef.current?.(element);
    return () => onMountRef.current?.(null);
  }, [editor]);

  useImperativeHandle(ref, () => ({
    focus: () => editor?.commands.focus(),
    insertText: (text) => { editor?.chain().focus().insertContent(text).run(); },
    hasSelection: () => Boolean(editor && !editor.state.selection.empty),
    isAtStart: () => Boolean(editor && (editor.isEmpty || editor.state.selection.from <= 1)),
    isAtEnd: () => Boolean(editor && (editor.isEmpty || editor.state.selection.to >= editor.state.doc.content.size - 1)),
    toggleBold: () => { editor?.chain().focus().toggleBold().run(); },
    toggleItalic: () => { editor?.chain().focus().toggleItalic().run(); },
    toggleUnderline: () => { editor?.chain().focus().toggleUnderline().run(); },
    toggleStrike: () => { editor?.chain().focus().toggleStrike().run(); },
    toggleCode: () => { editor?.chain().focus().toggleCode().run(); },
    toggleCodeBlock: () => { editor?.chain().focus().toggleCodeBlock().run(); },
    toggleBulletList: () => { editor?.chain().focus().toggleBulletList().run(); },
    toggleOrderedList: () => { editor?.chain().focus().toggleOrderedList().run(); },
    setLink: (href) => { editor?.chain().focus().setLink({ href }).run(); },
    setColor: (color) => { editor?.chain().focus().setColor(color).run(); },
    setBackgroundColor: (color) => { editor?.chain().focus().setBackgroundColor(color).run(); },
    setFontSize: (size) => { editor?.chain().focus().setFontSize(size).run(); },
  }), [editor]);

  return (
    <EditorContent
      editor={editor}
      onPaste={onPaste}
      className={`rounded-md border border-input bg-background ring-offset-background focus-within:ring-2 focus-within:ring-ring focus-within:ring-offset-2${disabled ? " cursor-not-allowed opacity-50" : ""}`}
    />
  );
});
