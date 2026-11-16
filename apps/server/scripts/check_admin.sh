#!/usr/bin/env bash
set -euo pipefail

cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../admin"

for tool in node npm; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    printf 'Missing admin build prerequisite: %s\n' "$tool" >&2
    exit 1
  fi
done
for tool in vue-tsc vite oxlint tsgolint oxfmt vitest; do
  if [[ ! -f "node_modules/.bin/$tool" ]]; then
    printf 'Missing installed admin dependency: %s. Run npm ci in apps/server/admin.\n' "$tool" >&2
    exit 1
  fi
done

npm run format:check
npm run check
npm test
npm run build
