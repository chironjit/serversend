# Server development

The server module remains at `apps/server`. `cmd/serversend` now runs the TLS-only process with persistent identity and metadata, health/readiness endpoints and capability discovery. Authentication, file storage, pairing and the admin interface remain unimplemented. No setup, account or file endpoints are exposed.

Run the component checks from the repository root:

```sh
bash apps/server/scripts/check.sh
```

The script checks Go formatting, runs `go vet` and tests, and compiles the executable into a temporary directory. It disables workspace participation and toolchain/module downloads and uses read-only module mode. The existing local `shared` module is referenced through a relative replacement; no new workspace or third-party module is introduced. The approved SQLite driver requires CGO and a C compiler.

To additionally check data races, run `GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -mod=readonly -race ./...` from `apps/server`. Integration tests bind ephemeral loopback TCP ports; a sandbox must permit local listeners. Checks fail rather than silently skipping those tests.

## Run locally

From the repository root, create three separate, writable directories and provide a certificate/key. For an isolated local experiment with OpenSSL installed:

```sh
task_dir="$(mktemp -d)"
mkdir -m 700 "$task_dir/config" "$task_dir/data" "$task_dir/staging"
openssl req -x509 -newkey ed25519 -noenc -days 7 \
  -subj /CN=localhost -addext 'subjectAltName=DNS:localhost,IP:127.0.0.1' \
  -keyout "$task_dir/key.pem" -out "$task_dir/cert.pem"

export SERVER_SEND_CONFIG_DIR="$task_dir/config"
export SERVER_SEND_DATA_DIR="$task_dir/data"
export SERVER_SEND_STAGING_DIR="$task_dir/staging"
export SERVER_SEND_TLS_CERT_FILE="$task_dir/cert.pem"
export SERVER_SEND_TLS_KEY_FILE="$task_dir/key.pem"
export SERVER_SEND_TCP_ADDRESS=127.0.0.1:8443

cd apps/server
go build -mod=readonly -o "$task_dir/serversend" ./cmd/serversend
"$task_dir/serversend" -check
"$task_dir/serversend"
```

`-check` validates TLS and storage and initializes/migrates metadata and identity, then exits without listening. It is not a read-only check. Use a separate terminal to request `https://localhost:8443/readyz` with `curl --cacert /path/to/cert.pem`. Use a properly provisioned certificate with the deployment's LAN/tailnet names outside this local experiment; clients must verify TLS and must not bypass certificate checks.

SIGINT/SIGTERM marks the server unready, stops accepting connections and allows up to ten seconds for requests to drain. Requests still active after the deadline are closed and the process reports a failure. Set a container stop timeout longer than ten seconds. Certificates are loaded at startup; renew them through a process restart. Certificate changes retain the independent server identity.

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

IPv6 listen addresses use brackets, such as `[::1]:8443`. Hostnames are deliberately unsupported for bind addresses; use an explicit local IP or an empty host to bind all interfaces. These are server bind settings, not client connection URLs.

Storage directories must be distinct and not lexically nested after path cleaning. Shared parent directories and similar names are permitted. The certificate and key paths must either both be supplied or both be empty; they may refer to the same combined PEM file. Whitespace surrounding a path and NUL bytes are rejected; spaces inside valid paths are supported.

Empty TLS paths remain valid for configuration parsing, but process startup rejects them. Startup also rejects unreadable/malformed/mismatched certificate keys, expired/future leaf certificates and certificates restricted to non-server uses. It does not verify a certificate's hostname or trust chain against the deployment: connecting clients must do that.

`config.CheckStorage` requires all three directories to exist and probes file creation, writing, syncing, removal and directory syncing. It resolves symlinks and compares directory/ancestor identities to reject aliased or nested paths, including bind-mount aliases. It never creates a missing mount path. The config directory must have no group/other permissions (normally 0700); an existing database must be a regular private file (normally 0600), not a symlink. Protect these directories against modification by other local users. Startup checks do not eliminate filesystem races in an untrusted parent directory.

Operators must still provide persistent mounts and a local filesystem suitable for SQLite WAL; these checks cannot prove a directory is mounted or qualify SMB/NFS locking. Capacity reservations, single-process deployment enforcement and durable object reconciliation remain storage work. Only one server process may use a deployment.

## Metadata and identity

`internal/database` owns schema version 1 in `/config/serversend.sqlite`, using the shared WAL/FULL-sync connection and transactional migration policy. The singleton identity contains a random 128-bit hexadecimal server ID and a 32-byte Ed25519 seed. Identity creation runs in an immediate transaction, so concurrent initializers converge on one identity. Invalid existing identity material fails startup without being replaced. A newer database schema is rejected without downgrade.

The seed is private server material, protected by the config directory/file permissions, not an encrypted-at-rest secret or a user vault key. Logs expose only the server ID. Public identity proof, signed challenges and certificate/channel association are specified and tested as shared contracts in [protocol identity](protocol-identity.md); the server proof endpoint and pairing integration remain unimplemented. The ID alone must never establish client trust. User/TOTP encryption keys are not implemented by this identity seed.

Keep `/config` together with `/data` and `/staging` across replacements. Until a live backup workflow exists, stop the process before backing up the config directory, including any SQLite WAL/SHM files. Do not copy only the main SQLite file from a running process. Losing config loses identity; do not delete it to bypass a startup error. There is no downgrade or automatic identity repair.

## Container

Build from the repository root:

```sh
docker build -f apps/server/deploy/Dockerfile -t serversend:dev .
bash apps/server/scripts/check_container.sh serversend:dev
```

The optional container check requires Docker, OpenSSL, curl and ripgrep already installed. It uses disposable named volumes and a short-lived test certificate, checks verified HTTPS, private metadata permissions, a read-only root filesystem, identity persistence across restart and SIGTERM exit status, then removes its test containers/volumes. It does not touch host deployment directories.

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

Tests cover configuration parsing/bounds; real filesystem permission and alias failures; metadata constraints and future-schema rejection; concurrent identity initialization and restart persistence; safe typed endpoint errors; verified TLS 1.3/HTTP/2 requests; invalid TLS before metadata mutation; certificate renewal; listener failures; and graceful draining. The admin shell, pairing endpoint integration, account/storage schemas and full deployment qualification remain, so chunks 03–04 are not complete. Chunk 02 shared contracts are complete; they do not expose these runtime workflows.
