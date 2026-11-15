#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

if [[ $# -gt 1 || ( $# == 1 && "$1" != --write ) ]]; then
  printf 'Usage: check_bindings.sh [--write]\n' >&2
  exit 1
fi
# Discover an existing installation only; checks never install tools. An
# explicit override or PATH selection remains authoritative, even if invalid.
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
wails_cli="${WAILS3:-}"
if [[ -z "$wails_cli" ]]; then
  wails_cli="$(command -v wails3 || true)"
  if [[ -z "$wails_cli" ]] && command -v go >/dev/null 2>&1; then
    go_bin="$(go env GOBIN)"
    if [[ -z "$go_bin" ]]; then
      go_path="$(go env GOPATH)"
      # GOPATH can be a list. Windows uses semicolons, preserving drive colons.
      if [[ "$go_path" == [a-zA-Z]:* ]]; then
        go_bin="${go_path%%;*}/bin"
      else
        go_bin="${go_path%%:*}/bin"
      fi
    fi
    for candidate in "$go_bin/wails3" "$go_bin/wails3.exe"; do
      if [[ -x "$candidate" ]]; then
        wails_cli="$candidate"
        break
      fi
    done
  fi
fi
if [[ -z "$wails_cli" ]]; then
  printf 'Wails CLI v3.0.0 was not found in PATH, GOBIN or GOPATH/bin. Set WAILS3 to its executable path.\n' >&2
  exit 1
fi
if ! cli_version="$("$wails_cli" version 2>&1)"; then
  printf 'Cannot run Wails CLI at %s. Set WAILS3 to a working v3.0.0 executable.\n' "$wails_cli" >&2
  exit 1
fi
if [[ "$cli_version" != v3.0.0 ]]; then
  printf 'Wails CLI at %s reports %s; binding generation requires v3.0.0. Set WAILS3 to the matching executable.\n' "$wails_cli" "$cli_version" >&2
  exit 1
fi
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
