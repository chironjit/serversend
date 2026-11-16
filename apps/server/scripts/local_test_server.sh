#!/usr/bin/env bash
# Interactive local test server: prepares storage, builds the server, creates
# the first administrator when needed, then runs the server in the foreground.
# Re-running reuses the saved settings and data. See docs/local-testing.md.
set -euo pipefail

cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

root="${SERVER_SEND_LOCAL_DIR:-$HOME/serversend-demo}"
settings="$root/settings.env"

say() { printf '\n\033[1m%s\033[0m\n' "$*"; }
ask() { # ask <prompt> <default>; prints the answer
  local answer=""
  read -r -p "$1 [$2]: " answer || true
  printf '%s' "${answer:-$2}"
}

command -v go >/dev/null 2>&1 || { echo "Go is required; see docs/server-development.md" >&2; exit 1; }

if [[ "${1:-}" == --reset ]]; then
  [[ -d "$root" ]] || { echo "Nothing to remove at $root"; exit 0; }
  say "This permanently deletes the test server at $root"
  echo "That includes its accounts, uploaded files, certificates and keys."
  [[ "$(ask 'Type "delete" to confirm' 'cancel')" == delete ]] || { echo "Cancelled; nothing was removed."; exit 0; }
  rm -rf -- "$root"
  echo "Removed $root"
  exit 0
fi

say "Step 1 of 4: storage"
mkdir -p "$root"
chmod 700 "$root"
for name in config data staging; do
  mkdir -p "$root/$name"
  chmod 700 "$root/$name"
done
echo "Using $root (set SERVER_SEND_LOCAL_DIR to use another location)"

say "Step 2 of 4: address"
if [[ -f "$settings" ]]; then
  # shellcheck disable=SC1090
  source "$settings"
  echo "Reusing saved address $LOCAL_ADDRESS, port $LOCAL_PORT (delete $settings to choose again)"
else
  echo "Use 127.0.0.1 to test on this computer only. To connect from another"
  echo "device, enter this computer's LAN or Tailscale IPv4 address instead."
  detected="$(hostname -I 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9]+(\.[0-9]+){3}$' | tr '\n' ' ' || true)"
  [[ -z "$detected" ]] || echo "Addresses on this computer: $detected"
  LOCAL_ADDRESS="$(ask 'Address clients will use' '127.0.0.1')"
  LOCAL_PORT="$(ask 'Port' '58443')"
  if ! [[ "$LOCAL_ADDRESS" =~ ^[0-9]+(\.[0-9]+){3}$ && "$LOCAL_PORT" =~ ^[0-9]+$ ]]; then
    echo "Enter a numeric IPv4 address and a numeric port." >&2
    exit 1
  fi
  printf 'LOCAL_ADDRESS=%q\nLOCAL_PORT=%q\n' "$LOCAL_ADDRESS" "$LOCAL_PORT" >"$settings"
fi

export SERVER_SEND_CONFIG_DIR="$root/config" SERVER_SEND_DATA_DIR="$root/data" SERVER_SEND_STAGING_DIR="$root/staging"
export SERVER_SEND_TLS_IPS="$LOCAL_ADDRESS"
unset SERVER_SEND_TLS_CERT_FILE SERVER_SEND_TLS_KEY_FILE
if [[ "$LOCAL_ADDRESS" == 127.0.0.1 ]]; then
  export SERVER_SEND_TCP_ADDRESS="127.0.0.1:$LOCAL_PORT"
else
  export SERVER_SEND_TCP_ADDRESS="0.0.0.0:$LOCAL_PORT"
fi

say "Step 3 of 4: build"
command -v npm >/dev/null 2>&1 || { echo "Node.js with npm is required to build the embedded admin page" >&2; exit 1; }
[[ -d admin/node_modules ]] || (cd admin && npm ci)
# Quiet on success; on failure run again so the real error is visible.
bash scripts/check_admin.sh >/dev/null 2>&1 || bash scripts/check_admin.sh
go build -mod=readonly -o "$root/serversend" ./cmd/serversend
echo "Built $root/serversend"

say "Step 4 of 4: administrator"
if [[ ! -f "$root/admin-created" ]]; then
  username="$(ask 'Administrator username' 'admin')"
  "$root/serversend" -bootstrap-admin "$username" 2>/dev/null
  printf '%s\n' "$username" >"$root/admin-created"
  echo
  echo "Copy the temporary password above now. It is valid for one hour."
elif [[ "$(ask "Administrator \"$(cat "$root/admin-created")\" exists. Issue a new temporary password? Only works before its first sign-in is completed (y/N)" 'N')" =~ ^[Yy] ]]; then
  "$root/serversend" -recover-bootstrap-admin "$(cat "$root/admin-created")" 2>/dev/null ||
    echo "Not reissued: this administrator already finished setting up, so sign in with its own password."
else
  echo "Keeping the existing administrator."
fi

say "Starting the server"
cat <<TEXT
Admin web interface:      https://$LOCAL_ADDRESS:$LOCAL_PORT/admin/
Sign in there as admin and create a user account for desktop transfers.
In another terminal run:   make local-test-desktop
In the app enter:          https://$LOCAL_ADDRESS:$LOCAL_PORT
Then compare the fingerprint the app shows with the
"Certificate fingerprint for desktop approval" line printed below.

Press Ctrl+C to stop the server. Run this command again to restart it with
the same accounts and files.
TEXT
echo
exec "$root/serversend"
