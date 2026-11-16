#!/usr/bin/env bash
# Builds and starts the desktop application for local testing against a
# server started with `make local-test-server-setup`. See docs/local-testing.md.
set -euo pipefail

cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

for tool in go node npm; do
  command -v "$tool" >/dev/null 2>&1 || { echo "$tool is required; see docs/desktop-development.md" >&2; exit 1; }
done

printf '\n\033[1m%s\033[0m\n' "Step 1 of 3: frontend dependencies"
if [[ ! -d frontend/node_modules ]]; then
  (cd frontend && npm ci)
else
  echo "Already installed (delete apps/desktop/frontend/node_modules to reinstall)"
fi

printf '\n\033[1m%s\033[0m\n' "Step 2 of 3: build"
(cd frontend && npm run build >/dev/null)
# Native toolkit deprecation warnings are noise here; real errors still stop the build.
go build -mod=readonly -tags production -o bin/desktop . 2>/dev/null || go build -mod=readonly -tags production -o bin/desktop .
echo "Built apps/desktop/bin/desktop"

printf '\n\033[1m%s\033[0m\n' "Step 3 of 3: start"
settings="${SERVER_SEND_LOCAL_DIR:-$HOME/serversend-demo}/settings.env"
if [[ -f "$settings" ]]; then
  # shellcheck disable=SC1090
  source "$settings"
  echo "Server address to enter in the app: https://$LOCAL_ADDRESS:$LOCAL_PORT"
else
  echo "No local test server found yet. Start one with: make local-test-server-setup"
fi
echo "Close the window to stop the app."
exec ./bin/desktop
