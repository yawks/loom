import { BrowserOpenURL as runtimeBrowserOpenURL } from "../../wailsjs/runtime/runtime.js";

export function BrowserOpenURL(url: string): void {
  // Wails rejects shell punctuation even inside valid URLs. Percent-encode it
  // without changing the scheme or hiding control characters from validation.
  runtimeBrowserOpenURL(url.replace(/[;|`$\\<>*{}\[\]()~! ]/g, (character) =>
    `%${character.charCodeAt(0).toString(16).toUpperCase()}`,
  ));
}
