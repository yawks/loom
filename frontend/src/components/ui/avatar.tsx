import * as React from "react"
import * as AvatarPrimitive from "@radix-ui/react-avatar"

import { cn } from "@/lib/utils"

const avatarFallbackColors = [
  { background: "#ffcdd2", foreground: "#7f1d1d" }, // red
  { background: "#f8bbd0", foreground: "#831843" }, // pink
  { background: "#e1bee7", foreground: "#581c87" }, // purple
  { background: "#c5cae9", foreground: "#312e81" }, // indigo
  { background: "#bbdefb", foreground: "#1e3a8a" }, // blue
  { background: "#b2ebf2", foreground: "#164e63" }, // cyan
  { background: "#b2dfdb", foreground: "#134e4a" }, // teal
  { background: "#c8e6c9", foreground: "#14532d" }, // green
  { background: "#dcedc8", foreground: "#365314" }, // light green
  { background: "#fff9c4", foreground: "#713f12" }, // yellow
  { background: "#ffe0b2", foreground: "#7c2d12" }, // orange
] as const

function getTextContent(node: React.ReactNode): string {
  if (typeof node === "string" || typeof node === "number") return String(node)
  if (Array.isArray(node)) return node.map(getTextContent).join("")
  if (React.isValidElement<{ children?: React.ReactNode }>(node)) {
    return getTextContent(node.props.children)
  }
  return ""
}

function getAvatarFallbackColors(children: React.ReactNode) {
  const text = getTextContent(children).trim().toLocaleUpperCase()
  let hash = 0

  for (const character of text) {
    hash = (hash * 31 + (character.codePointAt(0) ?? 0)) >>> 0
  }

  return avatarFallbackColors[hash % avatarFallbackColors.length]
}

const Avatar = React.forwardRef<
  React.ElementRef<typeof AvatarPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof AvatarPrimitive.Root>
>(({ className, ...props }, ref) => (
  <AvatarPrimitive.Root
    ref={ref}
    className={cn(
      "relative flex h-10 w-10 shrink-0 overflow-hidden rounded-full",
      className
    )}
    {...props}
  />
))
Avatar.displayName = AvatarPrimitive.Root.displayName

import { GetAvatar } from "../../../wailsjs/go/main/App";

const AvatarImage = React.forwardRef<
  React.ElementRef<typeof AvatarPrimitive.Image>,
  React.ComponentPropsWithoutRef<typeof AvatarPrimitive.Image>
>(({ className, src, ...props }, ref) => {
  const [avatarSrc, setAvatarSrc] = React.useState<string | undefined>(undefined);

  React.useEffect(() => {
    if (!src) {
      setAvatarSrc(undefined);
      return;
    }

    if (src.startsWith("data:") || src.startsWith("http")) {
      setAvatarSrc(src);
      return;
    }

    // It's a local path, fetch base64 on-demand
    GetAvatar(src).then((base64: string) => {
      setAvatarSrc(base64);
    }).catch((err: unknown) => {
      console.error("Failed to load avatar from path:", src, err);
      setAvatarSrc(undefined);
    });
  }, [src]);

  return (
    <AvatarPrimitive.Image
      ref={ref}
      className={cn("aspect-square h-full w-full", className)}
      src={avatarSrc}
      {...props}
    />
  );
})
AvatarImage.displayName = AvatarPrimitive.Image.displayName

const AvatarFallback = React.forwardRef<
  React.ElementRef<typeof AvatarPrimitive.Fallback>,
  React.ComponentPropsWithoutRef<typeof AvatarPrimitive.Fallback>
>(({ className, children, style, ...props }, ref) => {
  const colors = getAvatarFallbackColors(children)

  return (
    <AvatarPrimitive.Fallback
      ref={ref}
      className={cn(
        "flex h-full w-full items-center justify-center rounded-full bg-[var(--avatar-fallback-bg)] text-[var(--avatar-fallback-fg)]",
        className
      )}
      style={{
        "--avatar-fallback-bg": colors.background,
        "--avatar-fallback-fg": colors.foreground,
        ...style,
      } as React.CSSProperties}
      {...props}
    >
      {children}
    </AvatarPrimitive.Fallback>
  )
})
AvatarFallback.displayName = AvatarPrimitive.Fallback.displayName

export { Avatar, AvatarImage, AvatarFallback }
