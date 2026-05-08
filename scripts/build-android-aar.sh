#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

# Reuse PowerShell script to embed local private key.
exec powershell.exe -ExecutionPolicy Bypass -File ./scripts/build-aar-with-local-key.ps1 "$@"

