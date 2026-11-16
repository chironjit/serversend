#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOWORK=off
if [[ "$(go env CGO_ENABLED)" != 1 ]]; then
  printf 'Persistence checks require CGO_ENABLED=1 and a target C compiler; the non-CGO SQLite stub is not qualification.\n' >&2
  exit 1
fi
if [[ "$(go env GOOS)" != "$(go env GOHOSTOS)" || "$(go env GOARCH)" != "$(go env GOHOSTARCH)" ]]; then
  printf 'Run persistence checks natively on the target OS/architecture to test SQLite locking, paths and restart behavior.\n' >&2
  exit 1
fi
build_dir="$(mktemp -d "${TMPDIR:-/tmp}/serversend-persistence.XXXXXX")"
trap 'rm -rf -- "$build_dir"' EXIT
for component in shared apps/server apps/desktop; do
  case "$component" in
    shared) packages=(./sqlite ./testutil) ;;
    *) packages=(./internal/database) ;;
  esac
  (
    cd "$repo_dir/$component"
    go vet -mod=readonly "${packages[@]}"
    go test -mod=readonly -race "${packages[@]}"
    # Explicitly link each driver consumer as well as executing its real DB tests.
    for package in "${packages[@]}"; do
      go test -mod=readonly -c -o "$build_dir/persistence.test" "$package"
    done
  )
done
printf 'Native CGO persistence builds and tests passed for %s/%s\n' "$(go env GOOS)" "$(go env GOARCH)"
