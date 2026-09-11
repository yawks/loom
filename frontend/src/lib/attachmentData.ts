// The backend returns attachment contents as data URLs. Decode in memory:
// fetching these URLs is rejected by some native WebViews, even after a
// successful download. This is shared by PDF and Office previews.
export function dataUrlToBytes(dataUrl: string): Uint8Array<ArrayBuffer> {
  const comma = dataUrl.indexOf(",");
  if (!dataUrl.startsWith("data:") || comma < 0) {
    throw new Error("Invalid attachment data URL");
  }
  const metadata = dataUrl.slice(5, comma);
  const encoded = dataUrl.slice(comma + 1);
  return /;base64$/i.test(metadata)
    ? Uint8Array.from(atob(encoded), (character) => character.charCodeAt(0))
    : new TextEncoder().encode(decodeURIComponent(encoded));
}
