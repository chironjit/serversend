#!/usr/bin/env bash
set -euo pipefail

desktop_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$desktop_dir"

for tool in go gofmt node npm; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    printf 'Missing desktop prerequisite: %s\n' "$tool" >&2
    exit 1
  fi
done

for tool in vue-tsc vite; do
  if [[ ! -f "frontend/node_modules/.bin/$tool" ]]; then
    printf 'Missing installed frontend dependency: %s. Restore the approved lockfile dependencies before checking.\n' "$tool" >&2
    exit 1
  fi
done

# Validation must not download toolchains or change dependency selections.
export GOTOOLCHAIN=local
export GOPROXY=off
export GOSUMDB=off

if [[ "$(go env GOOS)" == linux ]]; then
  if ! command -v pkg-config >/dev/null 2>&1; then
    printf 'Missing Linux desktop prerequisite: pkg-config\n' >&2
    exit 1
  fi
  if [[ "$(go env CGO_ENABLED)" != 1 ]]; then
    printf 'Linux desktop checks require CGO_ENABLED=1 and a C compiler.\n' >&2
    exit 1
  fi
  if ! pkg-config --exists gtk4 webkitgtk-6.0; then
    printf 'Missing Linux desktop prerequisites: GTK4 and WebKitGTK 6.0 development libraries.\n' >&2
    exit 1
  fi
fi

unformatted="$(find . -type d \( -name node_modules -o -name .git -o -name bin \) -prune -o -type f -name '*.go' -exec gofmt -l {} +)"
if [[ -n "$unformatted" ]]; then
  printf 'Go files require gofmt:\n%s\n' "$unformatted" >&2
  exit 1
fi

printf 'Checking desktop frontend types and production build\n'
(cd frontend && npm run build)

printf 'Checking desktop Go code\n'
go vet -mod=readonly ./...
go test -mod=readonly -race ./...

build_dir="$(mktemp -d "${TMPDIR:-/tmp}/serversend-desktop-check.XXXXXX")"
trap 'rm -rf -- "$build_dir"' EXIT
go build -mod=readonly -tags production -o "$build_dir/desktop" .
printf 'Desktop checks passed\n'
