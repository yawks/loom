# Conversation screen capture protection

The shield button in a conversation header saves a device-local preference.
While that conversation is open, Loom requests capture exclusion for its entire
main window. Linked conversations of the selected contact are considered
together because contact details and conversation search can display any of them.
The setting does not propagate to the messaging provider or to other devices.

Windows 10 version 2004 (build 19041) and later use
`SetWindowDisplayAffinity(WDA_EXCLUDEFROMCAPTURE)`. Older Windows versions and
Linux do not offer the control. macOS uses `NSWindowSharingNone` and explicitly
labels the protection as partial: ScreenCaptureKit-based capture applications
can ignore this property. Native success is not proof that a particular meeting
application excludes the window.

This feature applies to the open conversation. It does not redact previews in
other views, system notifications, global search results or externally opened
files. It is not the separate presentation mode discussed for hiding private
content throughout the application.

## Implementation

- Preferences live in `conversation_capture_protections`, separate from remote
  conversation state, so provider synchronization cannot reset them.
- The renderer loads preferences before revealing the interface. It hides the
  document, including portal dialogs, before changing native protection and
  waits for native acknowledgement before revealing content.
- Native changes are serialized. Before disabling protection, the renderer
  waits for a paint with hidden content so the previous conversation is removed
  from the displayed frame. Failed operations leave the interface hidden and
  expose retry/close controls.
- Windows selects the process-owned `LoomMainWindow` class. macOS selects the
  Wails main window rather than whichever dialog currently has focus and applies
  the property synchronously on the Cocoa main thread.

## Validation

Automated checks:

```sh
go test . ./pkg/screenprotection -run TestConversationCaptureProtection -count=1
node --experimental-strip-types --test frontend/tests/captureProtectionQueue.test.ts
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./pkg/screenprotection
```

Manual acceptance requires real Windows/macOS builds and the actual capture
applications; compilation cannot validate compositor behavior:

1. Protect a conversation, restart Loom, and verify its shield remains enabled.
2. Share both the full screen and the Loom window in the target meeting tools.
   Switch repeatedly between protected and unprotected conversations and inspect
   the receiving participant's capture, including transition frames.
3. While protected, open threads, search within the conversation and attachment
   previews; verify the whole main window remains excluded where supported.
4. Remove protection and verify normal capture returns. Verify another account's
   conversation with a similar remote ID retains its own preference.
5. On macOS, verify the partial-protection explanation and test both legacy and
   ScreenCaptureKit capture paths. Do not interpret a successful API call as a
   guarantee that ScreenCaptureKit will exclude the window.

References: [Wails options](https://wails.io/docs/reference/options/),
[Windows display affinity](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-setwindowdisplayaffinity),
[macOS capture caveat in Electron's documentation for the same native API](https://github.com/electron/electron/blob/main/docs/api/browser-window.md).
