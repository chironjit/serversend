#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
output_dir="$(mktemp -d "${TMPDIR:-/tmp}/serversend-scaffold-smoke.XXXXXX")"
trap 'rm -rf -- "$output_dir"' EXIT
go build -mod=readonly -tags server,production -o "$output_dir/scaffold" .
node scripts/smoke.mjs "$output_dir/scaffold"
