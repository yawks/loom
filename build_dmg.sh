#!/usr/bin/env bash
set -euo pipefail

APP_NAME="Loom"
BUNDLE="${APP_NAME}.app"
DMG_NAME="${APP_NAME}.dmg"
BUILD_DIR="build/bin"
ARCH="${1:-universal}"   # universal | amd64 | arm64

echo "==> Building ${APP_NAME} (darwin/${ARCH})..."
wails build -platform "darwin/${ARCH}" -clean

APP_PATH="${BUILD_DIR}/${BUNDLE}"
if [[ ! -d "$APP_PATH" ]]; then
  echo "ERROR: ${APP_PATH} not found after build." >&2
  exit 1
fi

echo "==> Creating DMG..."
STAGING=$(mktemp -d)
trap 'rm -rf "$STAGING"' EXIT

cp -r "$APP_PATH" "$STAGING/"
ln -s /Applications "$STAGING/Applications"

# Remove existing DMG
rm -f "$DMG_NAME"

hdiutil create \
  -volname "$APP_NAME" \
  -srcfolder "$STAGING" \
  -ov \
  -format UDZO \
  "$DMG_NAME"

echo ""
echo "Done → ${DMG_NAME}"
echo "Install: open ${DMG_NAME}, drag ${BUNDLE} to Applications."
