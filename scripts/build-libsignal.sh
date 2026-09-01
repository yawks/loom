#!/bin/sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SOURCE_DIR="$ROOT_DIR/build/libsignal-source"
LIB_DIR="$ROOT_DIR/build/libsignal/lib"
MAUTRIX_SIGNAL_TAG="v0.2608.0"

if ! command -v cargo >/dev/null 2>&1; then
	echo "cargo is required (install Rust with rustup)" >&2
	exit 1
fi
if ! command -v protoc >/dev/null 2>&1; then
	echo "protoc is required (on macOS: brew install protobuf)" >&2
	exit 1
fi

if [ ! -d "$SOURCE_DIR/.git" ]; then
	git clone --depth 1 --branch "$MAUTRIX_SIGNAL_TAG" --recurse-submodules \
		https://github.com/mautrix/signal.git "$SOURCE_DIR"
fi

git -C "$SOURCE_DIR" submodule update --init --recursive
(
	cd "$SOURCE_DIR"
	./build-rust.sh
)

mkdir -p "$LIB_DIR"
cp "$SOURCE_DIR/pkg/libsignalgo/libsignal/target/release/libsignal_ffi.a" "$LIB_DIR/"

echo "libsignal_ffi.a installed in $LIB_DIR"
echo "Loom can now be built normally with: go test ./... or wails build"
