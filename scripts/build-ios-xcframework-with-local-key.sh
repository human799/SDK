#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

PRIVATE_KEY_PATH="${1:-config-templates/private_key.pem}"
OUT="${2:-sdk.xcframework}"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "This script must run on macOS." >&2
  exit 1
fi

if ! command -v gomobile >/dev/null 2>&1; then
  echo "gomobile not found. Install first:" >&2
  echo "  go install golang.org/x/mobile/cmd/gomobile@latest" >&2
  echo "  go install golang.org/x/mobile/cmd/gobind@latest" >&2
  exit 1
fi

if [[ ! -f "$PRIVATE_KEY_PATH" ]]; then
  echo "Private key file not found: $PRIVATE_KEY_PATH" >&2
  exit 1
fi

# Read PEM and encode as single-line base64 (portable between BSD/GNU base64)
if base64 --help >/dev/null 2>&1; then
  KEY_B64="$(base64 < "$PRIVATE_KEY_PATH" | tr -d '\r\n')"
else
  KEY_B64="$(base64 < "$PRIVATE_KEY_PATH" | tr -d '\r\n')"
fi

echo "Building iOS xcframework with embedded private key..."
echo "PrivateKeyPath: $PRIVATE_KEY_PATH"
echo "Output: $OUT"

gomobile init
gomobile bind -target=ios \
  -ldflags "-X proxy-system/sdk.EmbeddedPrivateKeyB64=$KEY_B64" \
  -o "$OUT" ./sdk

echo "Done: $OUT"

