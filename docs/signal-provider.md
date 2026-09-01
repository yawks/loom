# Signal provider

Loom uses `go.mau.fi/mautrix-signal/pkg/signalmeow` as a native linked-device
client. Signal protocol state is stored in a separate SQLite database for each
provider instance under the Loom configuration directory.

## Native dependency

`signalmeow` links to Signal's Rust `libsignal_ffi` static library. Build the
revision matching the pinned `mautrix-signal` module before building Loom:

```sh
./scripts/build-libsignal.sh
go test ./...
wails build
```

The build requires Rust/Cargo and `protoc`. On macOS, install the latter with
`brew install protobuf`.

The static library is intentionally excluded from git because it is a large,
platform-specific build artifact. The script pins `mautrix-signal` to the same
version as `go.mod` so that the Go bindings and Rust ABI stay aligned.
The Signal provider adds this local directory to CGO's linker search path, so
Wails binding generation and normal Go commands do not require `LIBRARY_PATH`.

## Authentication

Signal uses Loom's provider-neutral `authFlow: "qr"` configuration flow. Loom
opens a Signal provisioning websocket and displays its `sgnl://linkdevice` URI
as a QR code. Scan it from Signal's **Settings → Linked devices** screen.

Removing a Signal provider deletes its isolated protocol database and locally
downloaded attachments. Reauthentication removes only the linked-device
credentials and preserves Loom's canonical message database.

When the phone offers to transfer conversation history, accepting it uploads a
one-shot encrypted transfer archive. Loom downloads and decrypts that archive,
stores it in the per-instance Signal database, and exposes its direct chats,
groups and messages through Loom's canonical models. A contact sync by itself
does not contain conversation history.

If an older Loom build linked the device but never consumed the offered
archive, restart Loom and run a manual synchronization. If Signal has already
expired the one-shot archive, remove the linked device on the phone and pair it
again, accepting the history transfer during pairing.

## Data boundary

Signal UUIDs, group identifiers and protobuf payloads remain in the Go provider.
Only canonical Loom contacts, messages, attachments, reactions, receipts and
typing events are exposed to the frontend.
