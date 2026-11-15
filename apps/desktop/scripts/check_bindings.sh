#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

if [[ $# -gt 1 || ( $# == 1 && "$1" != --write ) ]]; then
  printf 'Usage: check_bindings.sh [--write]\n' >&2
  exit 1
fi
wails_cli="${WAILS3:-wails3}"
if [[ "$("$wails_cli" version 2>&1)" != v3.0.0 ]]; then
  printf 'Binding generation requires Wails CLI v3.0.0 (set WAILS3 to its path).\n' >&2
  exit 1
fi
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
output_dir="$(mktemp -d "${TMPDIR:-/tmp}/serversend-bindings.XXXXXX")"
trap 'rm -rf -- "$output_dir"' EXIT
"$wails_cli" generate bindings -ts -i -clean=true -d "$output_dir" .
if [[ "${1:-}" == --write ]]; then
  rm -rf frontend/bindings
  mkdir -p frontend/bindings
  cp -R "$output_dir/." frontend/bindings/
else
  diff -ru frontend/bindings "$output_dir"
fi
