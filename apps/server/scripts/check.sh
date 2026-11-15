#!/usr/bin/env bash
set -euo pipefail

cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

for tool in go gofmt; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    printf 'Missing server prerequisite: %s\n' "$tool" >&2
    exit 1
  fi
done

# Validation must not download toolchains or change dependency selections.
export GOTOOLCHAIN=local
export GOPROXY=off
export GOSUMDB=off
export GOWORK=off

unformatted="$(find . -type d \( -name vendor -o -name .git \) -prune -o -type f -name '*.go' -exec gofmt -l {} +)"
if [[ -n "$unformatted" ]]; then
  printf 'Go files need formatting:\n%s\n' "$unformatted" >&2
  exit 1
fi

go vet -mod=readonly ./...
go test -mod=readonly ./...

# Compile the process entry point too; keep build artifacts outside the checkout.
build_dir="$(mktemp -d "${TMPDIR:-/tmp}/serversend-server-check.XXXXXX")"
trap 'rm -rf -- "$build_dir"' EXIT
go build -mod=readonly -o "$build_dir/serversend" ./cmd/serversend
