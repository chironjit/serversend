# Server development

The server module remains at `apps/server`. `cmd/serversend` now runs the TLS-only process with persistent identity and metadata, health/readiness endpoints and capability discovery. Authentication, file storage, pairing and the admin interface remain unimplemented. No setup, account or file endpoints are exposed.

Run the component checks from the repository root:

```sh
bash apps/server/scripts/check.sh
```

The script checks Go formatting, runs `go vet` and tests, and compiles the executable into a temporary directory. It disables workspace participation and toolchain/module downloads and uses read-only module mode. The existing local `shared` module is referenced through a relative replacement; no new workspace or third-party module is introduced. The approved SQLite driver requires CGO and a C compiler.

To additionally check data races, run `GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -mod=readonly -race ./...` from `apps/server`. Integration tests bind ephemeral loopback TCP ports; a sandbox must permit local listeners. Checks fail rather than silently skipping those tests.

## Run locally

The preferred private deployment uses numeric IPv4 addresses and a locally generated certificate authority. No domain, DNS record, public certificate issuance or internet connection is needed for provisioning/renewal. TLS remains required.

For a local process experiment, run from the repository root:

```sh
task_dir="$(mktemp -d)"
mkdir -m 700 "$task_dir/config" "$task_dir/data" "$task_dir/staging"
export SERVER_SEND_CONFIG_DIR="$task_dir/config"
export SERVER_SEND_DATA_DIR="$task_dir/data"
export SERVER_SEND_STAGING_DIR="$task_dir/staging"
export SERVER_SEND_TLS_IPS=127.0.0.1
export SERVER_SEND_TCP_ADDRESS=127.0.0.1:8443
unset SERVER_SEND_TLS_CERT_FILE SERVER_SEND_TLS_KEY_FILE

cd apps/server
go build -mod=readonly -o "$task_dir/serversend" ./cmd/serversend
"$task_dir/serversend" -check
"$task_dir/serversend"
```

In another terminal, use the actual path printed by the startup message:

```sh
curl --cacert /path/to/config/tls/ca.crt https://127.0.0.1:8443/readyz
```

`-check` validates TLS/storage and initializes certificates, metadata and identity, then exits without listening. It is not read-only. To use a LAN or Tailscale IPv4 address, set `SERVER_SEND_TLS_IPS` to the addresses clients will actually enter, e.g. `192.168.1.20,10.10.10.50`. The port belongs in the URL/listener settings, not this IP list. For Docker, these are the reachable host/published or dedicated container LAN addresses, not an internal bridge address that clients cannot reach. Reserve stable addresses where practical.

### Private certificate lifecycle and trust

The server generates an independent Ed25519 private CA for each deployment and issues a certificate with IP subject alternative names for exactly the configured IPv4 addresses. It makes no public DNS, ACME or certificate-transparency requests. Configuration deliberately requires explicit IPs; a wildcard listener or Docker interface cannot tell the server its client-visible address. A maximum of sixteen distinct unicast IPv4 addresses is supported; hostnames, IPv6, unspecified/multicast addresses, ports, duplicates and surrounding whitespace are rejected by this mode.

Material persists under the existing protected `/config/tls` directory:

| File | Contents |
| --- | --- |
| `authority.pem` | Private CA certificate and signing key; never export the key |
| `ca.crt` | Public CA certificate for authenticated distribution to clients |
| `server.pem` | Server certificate and private key, stored as one atomic bundle |

Directories/files containing keys require 0700/0600 permissions. TLS paths must be regular files and the private TLS directory must be a real directory, not symlinks. Preserve the entire config directory with the database and identity; deleting TLS material is not a renewal/repair procedure. Existing corrupt/mismatched keys or public CA exports fail clearly rather than silently rotating trust. A missing public export can be recreated from the intact authority; a missing authority while other TLS material exists is an error. A wholly new config has a new CA and requires new client trust.

Leaf certificates last up to 90 days and renew when about 30 days remain, both on startup and on subsequent TLS handshakes. Renewal uses fresh server keys and retains the CA and independent ServerSend identity. Certificate/key bundles are flushed and atomically published before use. Existing connections continue; idle servers renew on the next handshake before serving an expired certificate. Changing the configured IP set reissues the leaf under the same CA on restart. Clients still require explicit approval for new endpoints. The private CA lasts ten years; it is not silently replaced at expiry. Plan an explicit client trust replacement before then. Leaf validity/renewal windows shorten near CA expiry. Clock errors and failed renewal/storage writes are reported, with no insecure fallback.

Clients must verify the private CA and IP normally. Export **only** `ca.crt` through an authenticated operator channel, such as Docker access or a trusted file copy, and compare its SHA-256 certificate fingerprint before trusting it:

```sh
openssl x509 -in /path/to/ca.crt -noout -fingerprint -sha256
```

For command-line testing, `curl --cacert` scopes trust to that request. Browsers require importing the CA through their supported OS/browser trust settings before IP HTTPS works without a warning. This does not bypass hostname/IP or expiry checks. Browser trust installation is manual and not platform-qualified by Go tests. The future desktop pairing flow will keep approved private anchors per profile and verify the pinned application identity; it is not implemented yet. Downloading a CA over an unverified connection cannot authenticate its own first use. Never use `curl -k`, skip TLS verification, or silently accept a replacement CA.

External certificate/key files remain an explicit alternative: set both `SERVER_SEND_TLS_CERT_FILE` and `SERVER_SEND_TLS_KEY_FILE`, leave `SERVER_SEND_TLS_IPS` empty, and renew those files externally. The two modes cannot be combined. External files continue to be loaded only at startup.

SIGINT/SIGTERM marks the server unready, stops accepting connections and allows up to ten seconds for requests to drain. Requests still active after the deadline are closed and the process reports a failure. Set a container stop timeout longer than ten seconds. Externally supplied certificates are loaded at startup and require a restart after replacement. Private IP certificates renew during handshakes. Certificate changes retain the independent server identity.

## Public endpoints

| Endpoint | Behaviour |
| --- | --- |
| `GET /healthz` | Process liveness, HTTP 200 |
| `GET /readyz` | HTTP 200 when not draining and the metadata connection responds; otherwise a safe typed HTTP 503 error |
| `GET /api/v1/capabilities` | Protocol version 1, HTTP/2 and provisional transfer ceilings |

HEAD is supported without response bodies. Other methods return a typed HTTP 405 error; unknown routes return a typed HTTP 404 error. Responses are not cached and contain no credentials or identity secrets. Readiness checks use a one-second context and test the database connection, not ongoing storage capacity or filesystem integrity. Startup performs the filesystem probes.

The TLS listener supports HTTP/2 and the standard HTTP/1.1 fallback. It requires TLS 1.3 and has bounded request-header, read, write and idle times. UDP/HTTP/3 are not active. Capabilities advertise only HTTP/2; no file-format support is advertised. The provisional ceilings are 1–8 MiB chunks, 8 MiB requests, 1 MiB manifests and 1,048,576 chunks. These are bounds for future transfer integration, not benchmarked production settings or proof that upload endpoints are available.

## Environment settings

`config.Load` reads process environment settings. `config.FromEnvironment` accepts a lookup function for isolated testing or composition. An unset variable keeps its default; an explicitly empty value is rejected except when both TLS paths are empty. Unknown variables are ignored.

| Variable | Default | Validation |
| --- | --- | --- |
| `SERVER_SEND_TCP_ADDRESS` | `:8443` | Numeric IPv4/IPv6 or empty host, decimal port 1–65535 |
| `SERVER_SEND_UDP_ADDRESS` | `:8443` | Same syntax; may use the same port as TCP |
| `SERVER_SEND_CONFIG_DIR` | `/config` | Absolute non-root path |
| `SERVER_SEND_DATA_DIR` | `/data` | Absolute non-root path |
| `SERVER_SEND_STAGING_DIR` | `/staging` | Absolute non-root path |
| `SERVER_SEND_TLS_CERT_FILE` | Empty | Absolute certificate path when provisioned |
| `SERVER_SEND_TLS_KEY_FILE` | Empty | Absolute key path when provisioned |
| `SERVER_SEND_TLS_IPS` | Empty | Comma-separated client-visible unicast IPv4 addresses; enables private provisioning instead of external files |

IPv6 listen addresses use brackets, such as `[::1]:8443`. Hostnames are deliberately unsupported for bind addresses; use an explicit local IP or an empty host to bind all interfaces. These are server bind settings, not client connection URLs.

Storage directories must be distinct and not lexically nested after path cleaning. Shared parent directories and similar names are permitted. The certificate and key paths must either both be supplied or both be empty; they may refer to the same combined PEM file. Whitespace surrounding a path and NUL bytes are rejected; spaces inside valid paths are supported.

Empty TLS paths remain valid for configuration parsing. Startup requires either a nonempty private IP list or both external certificate/key paths. Startup also rejects unreadable/malformed/mismatched certificate keys, expired/future leaf certificates and certificates restricted to non-server uses. It does not verify a certificate's hostname or trust chain against the deployment: connecting clients must do that.

`config.CheckStorage` requires all three directories to exist and probes file creation, writing, syncing, removal and directory syncing. It resolves symlinks and compares directory/ancestor identities to reject aliased or nested paths, including bind-mount aliases. It never creates a missing mount path. The config directory must have no group/other permissions (normally 0700); an existing database must be a regular private file (normally 0600), not a symlink. Protect these directories against modification by other local users. Startup checks do not eliminate filesystem races in an untrusted parent directory.

Operators must still provide persistent mounts and a local filesystem suitable for SQLite WAL; these checks cannot prove a directory is mounted or qualify SMB/NFS locking. Capacity reservations, single-process deployment enforcement and durable object reconciliation remain storage work. Only one server process may use a deployment.

## Metadata and identity

`internal/database` owns schema version 2 in `/config/serversend.sqlite`, using the shared WAL/FULL-sync connection and transactional migration policy. The singleton identity contains a random 128-bit hexadecimal server ID and a 32-byte Ed25519 seed. Identity creation runs in an immediate transaction, so concurrent initializers converge on one identity. Invalid existing identity material fails startup without being replaced. A newer database schema is rejected without downgrade. Version 2 adds initial account, upload, reservation and chunk metadata foundations without changing the version-1 identity. See [SQLite foundation](sqlite-foundation.md) for repositories, upgrade fixtures and qualification status.

The seed is private server material, protected by the config directory/file permissions, not an encrypted-at-rest secret or a user vault key. Logs expose only the server ID. Public identity proof, signed challenges and certificate/channel association are specified and tested as shared contracts in [protocol identity](protocol-identity.md); the server proof endpoint and pairing integration remain unimplemented. The ID alone must never establish client trust. User/TOTP encryption keys are not implemented by this identity seed.

Keep `/config` together with `/data` and `/staging` across replacements. Until a live backup workflow exists, stop the process before backing up the config directory, including any SQLite WAL/SHM files. Do not copy only the main SQLite file from a running process. Losing config loses identity; do not delete it to bypass a startup error. There is no downgrade or automatic identity repair.

## Container

Build from the repository root:

```sh
docker build -f apps/server/deploy/Dockerfile -t serversend:dev .
bash apps/server/scripts/check_container.sh serversend:dev
bash apps/server/scripts/check_container.sh serversend:dev --private-ipv4
```

For an interactive loopback example with automatic private certificates:

```sh
docker compose -f apps/server/deploy/compose.yaml up -d --build
docker compose -f apps/server/deploy/compose.yaml cp server:/config/tls/ca.crt /tmp/serversend-ca.crt
curl --cacert /tmp/serversend-ca.crt https://127.0.0.1:8443/readyz
```

Wait until the logs report that HTTPS is listening before copying the certificate. To serve a LAN address instead, set the published host address and certificate IP explicitly (substitute the real Docker host address):

```sh
SERVER_SEND_BIND_IP=192.168.1.20 SERVER_SEND_TLS_IPS=192.168.1.20 \
  docker compose -f apps/server/deploy/compose.yaml up -d --build
```

Then use `https://192.168.1.20:8443/readyz` with the exported CA. The default host port is 8443; `SERVER_SEND_PORT` changes the published port independently of the certificate. Run `docker compose -f apps/server/deploy/compose.yaml down` to stop/remove the container while keeping its named config/data/staging volumes. Preserve those volumes across replacements. The Compose example supplies only the backend foundation; there is no admin/login/upload UI yet.

The optional container check requires Docker, OpenSSL, curl, ripgrep and cmp already installed. It uses disposable named volumes. The default mode supplies a short-lived external test certificate; `--private-ipv4` supplies no certificate and verifies automatic provisioning, private key permissions and CA stability across restart. Both modes check verified HTTPS, private metadata permissions, a read-only root filesystem, identity persistence and SIGTERM exit status, then remove their test containers/volumes. It does not touch host deployment directories.

The multi-stage image builds the CGO executable and runs it as UID/GID `10001:10001`. Mount pre-created host directories with that ownership at `/config`, `/data` and `/staging`; set config permissions to 0700. Supply a readable certificate/key, for example in a separate read-only `/certs` bind mount:

```sh
docker run --rm --name serversend --read-only --stop-timeout 15 \
  --mount type=bind,src=/absolute/host/config,dst=/config \
  --mount type=bind,src=/absolute/host/data,dst=/data \
  --mount type=bind,src=/absolute/host/staging,dst=/staging \
  --mount type=bind,src=/absolute/host/certs,dst=/certs,readonly \
  -e SERVER_SEND_TLS_CERT_FILE=/certs/server.crt \
  -e SERVER_SEND_TLS_KEY_FILE=/certs/server.key \
  -p 8443:8443/tcp serversend:dev
```

Use bind mounts or explicitly managed named volumes that survive container replacement; do not rely on anonymous volumes. `--mount type=bind` rejects missing source paths instead of creating them. The image declares TCP and UDP port 8443, but UDP is reserved and need not be published yet. No app state is written into the image layer. The container image is a process foundation, not a production-ready storage service or a completed Unraid deployment template.

Tests cover configuration parsing/bounds; real filesystem permission and alias failures; metadata constraints and future-schema rejection; concurrent identity initialization and restart persistence; safe typed endpoint errors; verified TLS 1.3/HTTP/2 requests; invalid TLS before metadata mutation; certificate renewal; listener failures; and graceful draining. The admin shell, pairing endpoint integration, complete account/storage workflows and full deployment qualification remain. Chunk 03 still awaits Windows/macOS native persistence qualification; Chunk 04 remains in progress. Chunk 02 shared contracts are complete; they do not expose these runtime workflows.


Private IP TLS was verified locally with Go vet/race tests, real verified TLS 1.3/HTTP2 requests, untrusted/wrong-IP rejection, deterministic renewal/expiry, restart and IP changes, filesystem failures and both Docker smoke modes. The Compose example was also run with fresh named volumes on an ephemeral loopback port, returned a verified HTTPS readiness response, and was cleaned up. Public CA trust installation in Windows/macOS/Linux browsers remains a manual deployment step, not an automated acceptance claim.
