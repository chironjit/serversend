#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Validate this module independently, without implicit dependency/toolchain downloads.
export GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off

unformatted=$(gofmt -l .)
if [[ -n "$unformatted" ]]; then
  echo "Go files need formatting:" >&2
  echo "$unformatted" >&2
  exit 1
fi

go vet -mod=readonly ./...
go test -mod=readonly ./...
