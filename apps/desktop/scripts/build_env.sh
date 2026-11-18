#!/usr/bin/env bash
# Source from desktop build entry points; keep non-macOS builds unchanged.
desktop_goos="$(go env GOOS)"
desktop_ldflags=""
if [[ "$desktop_goos" == darwin ]]; then
  desktop_macos_minimum="$(cat "$(dirname -- "${BASH_SOURCE[0]}")/../build/darwin/minimum-version")"
  if [[ ! "$desktop_macos_minimum" =~ ^[0-9]+\.[0-9]+$ ]]; then
    printf 'Invalid desktop macOS minimum: %s\n' "$desktop_macos_minimum" >&2
    return 1
  fi
  export MACOSX_DEPLOYMENT_TARGET="$desktop_macos_minimum"
  export CGO_CFLAGS="${CGO_CFLAGS--O2 -g} -mmacosx-version-min=$desktop_macos_minimum"
  export CGO_CXXFLAGS="${CGO_CXXFLAGS--O2 -g} -mmacosx-version-min=$desktop_macos_minimum"
  export CGO_LDFLAGS="${CGO_LDFLAGS--O2 -g} -mmacosx-version-min=$desktop_macos_minimum"
  # Wails adds older #cgo deployment flags after the environment flags. Go's
  # extldflags reach the final compiler invocation last, fixing the link target.
  desktop_ldflags="-extldflags=-mmacosx-version-min=$desktop_macos_minimum"
fi

verify_macos_binary() {
  [[ "$desktop_goos" == darwin ]] || return 0
  local versions version
  versions="$(otool -l "$1" | awk '
    $1 == "cmd" { legacy = ($2 == "LC_VERSION_MIN_MACOSX") }
    $1 == "minos" || (legacy && $1 == "version") { print $2 }
  ')" || return 1
  if [[ -z "$versions" ]]; then
    printf 'No macOS deployment target found in %s\n' "$1" >&2
    return 1
  fi
  while IFS= read -r version; do
    if [[ "$version" != "$desktop_macos_minimum" && "$version" != "$desktop_macos_minimum.0" ]]; then
      printf 'macOS deployment target in %s is %s; expected %s\n' "$1" "$version" "$desktop_macos_minimum" >&2
      return 1
    fi
  done <<< "$versions"
}
