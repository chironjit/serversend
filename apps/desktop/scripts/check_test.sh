#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
fixture="$(mktemp -d "${TMPDIR:-/tmp}/serversend-desktop-check-test.XXXXXX")"
trap 'rm -rf -- "$fixture"' EXIT
fixture_desktop="$fixture/desktop with spaces"
mkdir -p "$fixture_desktop/scripts" "$fixture_desktop/frontend/node_modules/.bin" "$fixture/bin"
cp "$script_dir/check.sh" "$fixture_desktop/scripts/check.sh"
touch "$fixture_desktop/frontend/node_modules/.bin/vue-tsc" "$fixture_desktop/frontend/node_modules/.bin/vite"
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
      if [[ "${CHECK_TEST_FAIL:-}" == "$1" ]]; then exit 23; fi
    fi
    ;;
  npm) [[ "${CHECK_TEST_FAIL:-}" != frontend ]] || exit 24 ;;
  gofmt)
    if [[ "${CHECK_TEST_FAIL:-}" == format ]]; then printf './main.go\n'; fi
    ;;
  pkg-config) [[ "${CHECK_TEST_FAIL:-}" != libraries ]] || exit 25 ;;
esac
STUB
chmod +x "$fixture/bin/tool"
for tool in go gofmt npm node pkg-config; do
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
grep -F 'go:test -mod=readonly -race ./...' "$CHECK_TEST_LOG" >/dev/null
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
run_case
[[ "$result" == 0 ]]
if grep -F 'pkg-config:' "$CHECK_TEST_LOG" >/dev/null; then exit 1; fi
printf 'PASS: non-Linux checks do not require Linux libraries\n'
