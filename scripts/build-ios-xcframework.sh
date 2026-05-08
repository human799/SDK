#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

OUT="${1:-sdk.xcframework}"

if ! command -v gomobile >/dev/null 2>&1; then
  echo "gomobile not found. Install with:" >&2
  echo "  go install golang.org/x/mobile/cmd/gomobile@latest" >&2
  echo "  go install golang.org/x/mobile/cmd/gobind@latest" >&2
  exit 1
fi

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "iOS build requires macOS (Darwin)." >&2
  exit 1
fi

gomobile init
gomobile bind -target=ios -o "$OUT" ./sdk

echo "Done: $OUT"

