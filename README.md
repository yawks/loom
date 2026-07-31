# Loom — Unified Messaging Desktop App

> **Note:** This is a vibecoded project — built fast, iterated freely, and shaped by experimentation rather than a formal spec.

Loom is a desktop application that brings multiple messaging services into a single interface. Connect WhatsApp, Slack, Microsoft Teams, Google Chat, and Google Messages, and manage all your conversations from one place.

---

## How it works

Loom runs entirely on your machine. There is **no intermediate server** — unlike bridge-based approaches (such as a Matrix homeserver), the app connects directly to each messaging service using their native protocols or APIs. Your messages are never routed through a third-party relay.

When you add a new account, Loom fetches a few dozen recent messages to give you immediate context. On subsequent launches, it picks up where it left off: messages received since the last session are synced automatically.

---

## Screenshots

| Dark theme | Light theme |
|---|---|
| ![Loom dark theme](pictures/loom-black-theme.png) | ![Loom light theme](pictures/loom-light-theme.png) |
| ![Thread — dark theme](pictures/loom-thread-black-theme.png) | ![Thread — light theme](pictures/loom-thread-light-theme.png) |

---

## Supported providers

### WhatsApp

| Feature | Supported |
|---|---|
| Send / receive messages | ✓ |
| Reactions | ✓ |
| Typing indicator | ✓ |
| Group management | ✓ |
| Edit message | ✓ |
| Delete message | ✓ |
| Read receipts | ✓ |
| Pin / mute conversations | ✓ |
| QR code authentication | ✓ |
| Threads | — |

### Slack

| Feature | Supported |
|---|---|
| Send / receive messages | ✓ |
| Threads | ✓ |
| Reactions | ✓ |
| Custom emoji | ✓ |
| Typing indicator | ✓ |
| Channel / group management | ✓ |
| Edit message | ✓ |
| Delete message | ✓ |

### Microsoft Teams

| Feature | Supported |
|---|---|
| Send / receive messages | ✓ |
| Direct messages and group conversations | ✓ |
| Reactions | ✓ |
| Typing indicator | ✓ |
| Edit message | ✓ |
| Delete message | ✓ |
| Read receipts | ✓ |
| Replies | ✓ |
| Send / receive files and inline images | ✓ |
| Call events and Teams meeting links | ✓ |
| Contact avatars | ✓ |
| Contact presence (online, away, busy, in a meeting, do not disturb) | ✓ |
| Automatic discovery of new conversations | ✓ |
| Catch-up sync after startup and system wake | ✓ |

Teams authentication uses Microsoft's browser-based device-code flow. Loom opens
a browser, fills in the temporary code and lets Microsoft handle credentials,
MFA and Conditional Access. Passwords and browser cookies are not stored by Loom,
and the provider does not require Microsoft Graph application permissions.

Presence is currently available for one-to-one conversations and is refreshed
approximately once per minute. New conversations are discovered in the background,
and Loom reconnects the Teams event stream and synchronizes missed messages after
the computer resumes from sleep.

> **Note:** The Teams integration uses the same private web services as the Teams
> client through a maintained fork of `mautrix-teams`. As these are not public
> Microsoft Graph APIs, Microsoft may change them without notice.

### Google Chat

| Feature | Supported |
|---|---|
| Send / receive messages | ✓ |
| Threads | ✓ |
| Reactions | ✓ |
| Edit message | ✓ |
| Delete message | ✓ |

### Google Messages

| Feature | Supported |
|---|---|
| Send / receive messages | ✓ |
| Reactions | ✓ |
| Delete message | ✓ |

---

## Tech stack

- **Backend:** Go + [Wails v2](https://wails.io/) (no Electron, no Node.js runtime)
- **Frontend:** React + TypeScript + Vite + TailwindCSS + [shadcn/ui](https://ui.shadcn.com/)
- **State management:** Zustand
- **Database:** SQLite (local, on-device)
- **i18n:** react-i18next

### Protocol libraries

The protocol layer relies on the excellent [mau.fi](https://mau.fi/) libraries:

- [whatsmeow](https://github.com/tulir/whatsmeow) — WhatsApp
- [mautrix-gmessages](https://github.com/mautrix/gmessages) — Google Messages
- [mautrix-googlechat](https://github.com/mautrix/googlechat) — Google Chat
- [mautrix-teams](https://github.com/yawks/mautrix-teams) — Microsoft Teams

### UI inspiration

The layout draws heavily from [Element Web](https://github.com/element-hq/element-web), the reference Matrix client, whose three-panel design (sidebar / conversation list / message view) sets the standard for multi-account messaging interfaces.

---

## Development

### Prerequisites

- [Go 1.21+](https://golang.org/dl/)
- [Node.js LTS](https://nodejs.org/)
- [Wails CLI v2](https://wails.io/docs/gettingstarted/installation)

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

### Run in dev mode

```bash
cd frontend && npm install
cd ..
wails dev
```

### Mock mode

Launch Loom with a fully isolated, deterministic set of fake accounts,
conversations and messages:

```bash
wails dev -appargs "--mock"
```

For a built application, pass the flag directly:

```bash
./Loom --mock
```

Mock mode uses an in-memory database and never loads real accounts or messages.
The General settings remain usable and Accounts displays fake Slack and WhatsApp
accounts.

### Build for production

```bash
cd frontend && npm install && npm run build
cd ..
wails build
# output: build/bin/
```
