#!/usr/bin/env bash
set -euo pipefail

# Run after building the image. Only disposable, uniquely named test resources
# are touched; no host bind mounts or production deployment are used.
image="${1:-serversend:dev}"
mode="${2:-external}"
if [[ "$mode" != external && "$mode" != --private-ipv4 ]]; then
  printf 'Usage: %s [image] [--private-ipv4]\n' "$0" >&2
  exit 1
fi
for tool in docker openssl curl grep cmp; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    printf 'Missing container-check prerequisite: %s\n' "$tool" >&2
    exit 1
  fi
done
docker image inspect "$image" >/dev/null

fixture_dir="$(mktemp -d "${TMPDIR:-/tmp}/serversend-container.XXXXXX")"
resource_name="${fixture_dir##*/}"
container_name="$resource_name-server"
volumes=("$resource_name-config" "$resource_name-data" "$resource_name-staging")

cleanup() {
  result=$?
  if [[ "$result" != 0 ]]; then
    docker logs "$container_name" >&2 2>/dev/null || true
  fi
  docker rm -f -v "$container_name" >/dev/null 2>&1 || true
  for volume in "${volumes[@]}"; do
    docker volume rm "$volume" >/dev/null 2>&1 || true
  done
  rm -rf -- "$fixture_dir"
  exit "$result"
}
trap cleanup EXIT

tls_settings=(-e SERVER_SEND_TLS_IPS=127.0.0.1)
if [[ "$mode" == external ]]; then
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -noenc -days 1 \
    -subj /CN=localhost -addext 'subjectAltName=DNS:localhost,IP:127.0.0.1' \
    -keyout "$fixture_dir/key.pem" -out "$fixture_dir/cert.pem" >/dev/null 2>&1
  tls_settings=(-e SERVER_SEND_TLS_CERT_FILE=/config/cert.pem -e SERVER_SEND_TLS_KEY_FILE=/config/key.pem)
fi

mounts=(
  --mount "type=volume,src=${volumes[0]},dst=/config"
  --mount "type=volume,src=${volumes[1]},dst=/data"
  --mount "type=volume,src=${volumes[2]},dst=/staging"
)
docker create --name "$container_name" --read-only --stop-timeout 15 \
  "${mounts[@]}" -p 127.0.0.1::58443/tcp \
  "${tls_settings[@]}" "$image" >/dev/null
if [[ "$mode" == external ]]; then
  docker cp "$fixture_dir/cert.pem" "$container_name:/config/cert.pem"
  docker cp "$fixture_dir/key.pem" "$container_name:/config/key.pem"
fi
# Set ownership on these test volumes only, while the application is stopped.
docker run --rm --user 0:0 --entrypoint sh "${mounts[@]}" "$image" -c \
  'chown -R 10001:10001 /config /data /staging; chmod 0700 /config /data /staging; if [ -f /config/key.pem ]; then chmod 0600 /config/key.pem; fi' >/dev/null

[[ "$(docker inspect --format '{{.Config.User}}' "$container_name")" == 10001:10001 ]]

wait_ready() {
  local address
  address="$(docker port "$container_name" 58443/tcp)"
  curl --silent --show-error --fail --retry 20 --retry-all-errors \
    --retry-delay 1 --retry-max-time 15 --max-time 2 \
    --cacert "$fixture_dir/cert.pem" "https://$address/readyz" >"$fixture_dir/ready.json"
  grep -q '"status":"ready"' "$fixture_dir/ready.json"
}

check_browser_tls() {
  local address
  address="$(docker port "$container_name" 58443/tcp)"
  # Restrict certificate authentication to browser-supported ECDSA P-256.
  # Go/curl default clients also accept Ed25519 and missed this regression.
  printf 'GET /readyz HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n' |
    openssl s_client -quiet -tls1_3 -sigalgs ecdsa_secp256r1_sha256 \
      -verify_return_error -verify_ip 127.0.0.1 -CAfile "$fixture_dir/cert.pem" \
      -connect "$address" >"$fixture_dir/browser-response.txt" 2>"$fixture_dir/browser-tls.log"
  grep -q '"status":"ready"' "$fixture_dir/browser-response.txt"
}

check_admin() {
  local address script_path style_path
  address="$(docker port "$container_name" 58443/tcp)"
  curl --silent --show-error --fail --max-time 2 \
    --cacert "$fixture_dir/cert.pem" "https://$address/admin/" >"$fixture_dir/admin.html"
  grep -q 'ServerSend administration' "$fixture_dir/admin.html"
  script_path="$(grep -Eo '/admin/assets/[^" ]+\.js' "$fixture_dir/admin.html" | head -n 1)"
  style_path="$(grep -Eo '/admin/assets/[^" ]+\.css' "$fixture_dir/admin.html" | head -n 1)"
  [[ -n "$script_path" && -n "$style_path" ]]
  for path in "$script_path" "$style_path" /admin/theme-init.js; do
    curl --silent --show-error --fail --max-time 2 \
      --cacert "$fixture_dir/cert.pem" "https://$address$path" >/dev/null
  done
}

docker start "$container_name" >/dev/null
# SQLite must be able to spill large deletion tables with a read-only root.
[[ "$(docker exec "$container_name" printenv SQLITE_TMPDIR)" == /staging ]]
docker exec "$container_name" sh -c 'test -w "$SQLITE_TMPDIR"'
if [[ "$mode" == --private-ipv4 ]]; then
  # Wait only for initial generation, then export the public CA through Docker's
  # authenticated operator channel. No TLS verification is disabled.
  for attempt in {1..30}; do
    if docker cp "$container_name:/config/tls/ca.crt" "$fixture_dir/cert.pem" 2>/dev/null; then break; fi
    sleep 0.1
  done
  [[ -s "$fixture_dir/cert.pem" ]]
  [[ "$(docker exec "$container_name" stat -c '%a' /config/tls/authority.pem)" == 600 ]]
  [[ "$(docker exec "$container_name" stat -c '%a' /config/tls/server.pem)" == 600 ]]
fi
wait_ready
check_browser_tls
check_admin
first_identity="$(docker logs "$container_name" 2>&1 | grep -Eo 'Server [0-9a-f]{32} listening' | tail -n 1)"
[[ -n "$first_identity" ]]
[[ "$(docker exec "$container_name" stat -c '%a' /config/serversend.sqlite)" == 600 ]]
[[ -z "$(docker diff "$container_name")" ]]

docker stop "$container_name" >/dev/null
[[ "$(docker inspect --format '{{.State.ExitCode}}' "$container_name")" == 0 ]]
docker start "$container_name" >/dev/null
wait_ready
check_browser_tls
check_admin
if [[ "$mode" == --private-ipv4 ]]; then
  docker cp "$container_name:/config/tls/ca.crt" "$fixture_dir/ca-after-restart.crt"
  cmp -s "$fixture_dir/cert.pem" "$fixture_dir/ca-after-restart.crt"
fi
second_identity="$(docker logs "$container_name" 2>&1 | grep -Eo 'Server [0-9a-f]{32} listening' | tail -n 1)"
[[ "$first_identity" == "$second_identity" ]]
docker stop "$container_name" >/dev/null
[[ "$(docker inspect --format '{{.State.ExitCode}}' "$container_name")" == 0 ]]

printf 'Container checks passed (%s): verified TLS, embedded admin assets, non-root user, read-only root, restart identity/trust, SIGTERM\n' "$mode"
