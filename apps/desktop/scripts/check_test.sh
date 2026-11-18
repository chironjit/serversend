#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
fixture="$(mktemp -d "${TMPDIR:-/tmp}/serversend-desktop-check-test.XXXXXX")"
trap 'rm -rf -- "$fixture"' EXIT
# TMPDIR commonly ends in / on macOS. Match the normalized paths reported by
# the child shell instead of comparing /tmp//... against /tmp/....
fixture="$(cd -- "$fixture" && pwd -P)"
fixture_desktop="$fixture/desktop with spaces"
mkdir -p "$fixture_desktop/scripts" "$fixture_desktop/build/darwin" "$fixture_desktop/frontend/node_modules/.bin" "$fixture/bin"
cp "$script_dir/check.sh" "$fixture_desktop/scripts/check.sh"
cp "$script_dir/build_env.sh" "$fixture_desktop/scripts/build_env.sh"
cp "$script_dir/../build/darwin/minimum-version" "$fixture_desktop/build/darwin/minimum-version"
for tool in vue-tsc vite oxlint tsgolint oxfmt vitest; do
  touch "$fixture_desktop/frontend/node_modules/.bin/$tool"
done
printf 'package main\n' > "$fixture_desktop/main.go"

cat > "$fixture/bin/tool" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
name="${0##*/}"
printf '%s:%s:%s\n' "$name" "$*" "$PWD" >> "$CHECK_TEST_LOG"
case "$name" in
  go)
    if [[ "${1:-}" == env ]]; then
      case "${2:-}" in
        GOOS) printf '%s\n' "${CHECK_TEST_GOOS:-linux}" ;;
        CGO_ENABLED) printf '%s\n' "${CHECK_TEST_CGO:-1}" ;;
      esac
    else
      [[ "$GOTOOLCHAIN" == local && "$GOPROXY" == off && "$GOSUMDB" == off ]]
      [[ "$*" == *-mod=readonly* ]]
      if [[ "${CHECK_TEST_GOOS:-linux}" == darwin ]]; then
        [[ "$MACOSX_DEPLOYMENT_TARGET" == 12.0 ]]
        [[ "$CGO_CFLAGS" == '-DKEEP_C -mmacosx-version-min=12.0' ]]
        [[ "$CGO_CXXFLAGS" == '-DKEEP_CXX -mmacosx-version-min=12.0' ]]
        [[ "$CGO_LDFLAGS" == '-Lkeep -mmacosx-version-min=12.0' ]]
        if [[ "$1" == test || "$1" == build ]]; then
          [[ "$*" == *'-ldflags=-extldflags=-mmacosx-version-min=12.0'* ]]
        fi
      fi
      if [[ "${CHECK_TEST_FAIL:-}" == "$1" ]]; then exit 23; fi
    fi
    ;;
  npm) [[ "${CHECK_TEST_FAIL:-}" != frontend && "${CHECK_TEST_FAIL:-}" != "npm-${2:-${1:-}}" ]] || exit 24 ;;
  gofmt)
    if [[ "${CHECK_TEST_FAIL:-}" == format ]]; then printf './main.go\n'; fi
    ;;
  pkg-config) [[ "${CHECK_TEST_FAIL:-}" != libraries ]] || exit 25 ;;
  otool)
    case "${CHECK_TEST_MACHO:-modern}" in
      modern) printf 'cmd LC_BUILD_VERSION\n minos %s\n' "${CHECK_TEST_MACOS_VERSION:-12.0}" ;;
      legacy) printf 'cmd LC_VERSION_MIN_MACOSX\n version %s\n' "${CHECK_TEST_MACOS_VERSION:-12.0.0}" ;;
      universal) printf 'cmd LC_BUILD_VERSION\n minos 12.0\ncmd LC_BUILD_VERSION\n minos 15.0\n' ;;
      missing) printf 'cmd LC_UUID\n' ;;
      failure) exit 26 ;;
    esac
    ;;
esac
STUB
chmod +x "$fixture/bin/tool"
for tool in go gofmt npm node pkg-config otool; do
  ln -s tool "$fixture/bin/$tool"
done

export PATH="$fixture/bin:$PATH"
export CHECK_TEST_LOG="$fixture/commands"

run_case() {
  : > "$CHECK_TEST_LOG"
  if bash "$fixture_desktop/scripts/check.sh" > "$fixture/output" 2>&1; then
    result=0
  else
    result=$?
  fi
}

run_case
[[ "$result" == 0 ]]
grep -F 'Desktop checks passed' "$fixture/output" >/dev/null
grep -F "npm:run build:$fixture_desktop/frontend" "$CHECK_TEST_LOG" >/dev/null
grep -F 'go:test -mod=readonly -race -ldflags= ./...' "$CHECK_TEST_LOG" >/dev/null
grep -F 'go:build -mod=readonly -tags production' "$CHECK_TEST_LOG" >/dev/null
printf 'PASS: all stages run from the component directory with offline read-only Go settings\n'

for failure in format libraries; do
  export CHECK_TEST_FAIL="$failure"
  run_case
  [[ "$result" != 0 ]]
  if grep -F 'npm:' "$CHECK_TEST_LOG" >/dev/null; then exit 1; fi
  printf 'PASS: %s failure prevents build stages\n' "$failure"
done
unset CHECK_TEST_FAIL

for stage in format:check check test; do
  export CHECK_TEST_FAIL="npm-$stage"
  run_case
  [[ "$result" != 0 ]]
  if grep -F 'go:vet' "$CHECK_TEST_LOG" >/dev/null; then exit 1; fi
  printf 'PASS: frontend %s failure stops subsequent checks\n' "$stage"
done
unset CHECK_TEST_FAIL

export CHECK_TEST_CGO=0
run_case
[[ "$result" != 0 ]]
grep -F 'require CGO_ENABLED=1' "$fixture/output" >/dev/null
unset CHECK_TEST_CGO
printf 'PASS: disabled Linux CGO reports a prerequisite failure\n'

for failure in frontend vet test build; do
  export CHECK_TEST_FAIL="$failure"
  run_case
  [[ "$result" != 0 ]]
  if grep -F 'Desktop checks passed' "$fixture/output" >/dev/null; then exit 1; fi
  case "$failure" in
    frontend) if grep -F 'go:vet' "$CHECK_TEST_LOG" >/dev/null; then exit 1; fi ;;
    vet) if grep -F 'go:test' "$CHECK_TEST_LOG" >/dev/null; then exit 1; fi ;;
    test) if grep -F 'go:build' "$CHECK_TEST_LOG" >/dev/null; then exit 1; fi ;;
  esac
  printf 'PASS: %s failure propagates without reporting success\n' "$failure"
done
unset CHECK_TEST_FAIL

restricted_bin="$fixture/without-node"
mkdir "$restricted_bin"
for tool in go gofmt npm; do
  ln -s "$fixture/bin/tool" "$restricted_bin/$tool"
done
ln -s "$(command -v dirname)" "$restricted_bin/dirname"
bash_path="$(command -v bash)"
if PATH="$restricted_bin" "$bash_path" "$fixture_desktop/scripts/check.sh" > "$fixture/output" 2>&1; then
  exit 1
fi
grep -F 'Missing desktop prerequisite: node' "$fixture/output" >/dev/null
printf 'PASS: missing executable is reported before build stages\n'

rm "$fixture_desktop/frontend/node_modules/.bin/vite"
run_case
[[ "$result" != 0 ]]
grep -F 'Missing installed frontend dependency: vite' "$fixture/output" >/dev/null
touch "$fixture_desktop/frontend/node_modules/.bin/vite"
printf 'PASS: missing frontend dependency is reported without installation\n'

export CHECK_TEST_GOOS=darwin
export CHECK_TEST_FAIL=libraries
export CGO_CFLAGS=-DKEEP_C CGO_CXXFLAGS=-DKEEP_CXX CGO_LDFLAGS=-Lkeep
export MACOSX_DEPLOYMENT_TARGET=15.0
run_case
[[ "$result" == 0 ]]
if grep -F 'pkg-config:' "$CHECK_TEST_LOG" >/dev/null; then exit 1; fi
printf 'PASS: non-Linux checks do not require Linux libraries\n'
printf 'PASS: macOS compile/link targets override host defaults and preserve caller flags\n'

export CHECK_TEST_MACHO=legacy
run_case
[[ "$result" == 0 ]]
printf 'PASS: legacy Mach-O deployment metadata is accepted\n'

export CHECK_TEST_MACHO=modern CHECK_TEST_MACOS_VERSION=11.0
run_case
[[ "$result" != 0 ]]
grep -F 'expected 12.0' "$fixture/output" >/dev/null
unset CHECK_TEST_MACOS_VERSION

for metadata in universal missing failure; do
  export CHECK_TEST_MACHO="$metadata"
  run_case
  [[ "$result" != 0 ]]
  if grep -F 'Desktop checks passed' "$fixture/output" >/dev/null; then exit 1; fi
done
printf 'PASS: incorrect architecture targets, missing metadata and inspection errors fail validation\n'
