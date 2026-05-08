#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PS1_SCRIPT="$SCRIPT_DIR/build-aar-with-local-key.ps1"
if [[ ! -f "$PS1_SCRIPT" ]]; then
  echo "PowerShell script not found: $PS1_SCRIPT" >&2
  exit 1
fi
exec powershell.exe -ExecutionPolicy Bypass -File "$PS1_SCRIPT" "$@"

