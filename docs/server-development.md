# Server development

The server module remains at `apps/server`. `cmd/serversend` runs the TLS-only process with persistent identity and metadata, health/readiness endpoints, capability discovery, browser administrator onboarding/user creation, and the standard-file transfer workflow described in the [vertical slice](vertical-slice.md). Complete pairing and account/device administration remain roadmap work.

Install Node.js 26 and restore the pinned admin dependencies once, then run the component checks from the repository root:

```sh
(cd apps/server/admin && npm ci)
make check-server
```

The script checks Go formatting, runs the admin Oxc formatting/static/type checks, Vue template checks and tests, builds production assets, runs `go vet` and tests, and compiles the executable into a temporary directory. Checks never install dependencies. Go checks disable workspace participation and toolchain/module downloads and use read-only module mode. The existing local `shared` module is referenced through a relative replacement; no new Go workspace module is introduced. The approved SQLite driver requires CGO and a C compiler.

The admin reuses the desktop's Vue/Vite/TypeScript, Oxc and Vitest tooling. Tailwind CSS and the explicitly approved `@tailwindcss/vite` build plugin are both pinned to `4.3.5`. A build-output guard rejects any remaining uncompiled Tailwind directives. Run `make check-admin` for its checks alone. Build the admin with `npm run build` in `apps/server/admin` before directly running `go run`, `go build` or `go test ./...` in the server module: Go embeds `admin/dist` at compile time. Generated assets and `node_modules` are ignored, so a fresh checkout requires this build. CI restores the admin lockfile, checks the UI on all three platforms, and builds it before Linux server checks. Docker builds its assets in a separate Node stage; the runtime image needs neither Node nor the source tree.

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
export SERVER_SEND_TCP_ADDRESS=127.0.0.1:58443
unset SERVER_SEND_TLS_CERT_FILE SERVER_SEND_TLS_KEY_FILE

cd apps/server
go build -mod=readonly -o "$task_dir/serversend" ./cmd/serversend
"$task_dir/serversend" -check
"$task_dir/serversend"
```

In another terminal, use the actual path printed by the startup message:

```sh
curl --cacert /path/to/config/tls/ca.crt https://127.0.0.1:58443/readyz
```

`-check` validates TLS/storage and initializes certificates, metadata and identity, then exits without listening. It is not read-only. To use a LAN or Tailscale IPv4 address, set `SERVER_SEND_TLS_IPS` to the addresses clients will actually enter, e.g. `192.168.1.20,10.10.10.50`. The port belongs in the URL/listener settings, not this IP list. For Docker, these are the reachable host/published or dedicated container LAN addresses, not an internal bridge address that clients cannot reach. Reserve stable addresses where practical.

### Private certificate lifecycle and trust

The server generates an independent ECDSA P-256 private CA for each deployment and issues a certificate with IP subject alternative names for exactly the configured IPv4 addresses. It makes no public DNS, ACME or certificate-transparency requests. Configuration deliberately requires explicit IPs; a wildcard listener or Docker interface cannot tell the server its client-visible address. A maximum of sixteen distinct unicast IPv4 addresses is supported; hostnames, IPv6, unspecified/multicast addresses, ports, duplicates and surrounding whitespace are rejected by this mode.

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
| `GET /admin` | Fixed same-origin redirect to `/admin/` |
| `GET /admin/` | Public development shell and embedded production assets; no login, credentials or enrollment actions |
| `GET /healthz` | Process liveness, HTTP 200 |
| `GET /readyz` | HTTP 200 when not draining and the metadata connection responds; otherwise a safe typed HTTP 503 error |
| `GET /api/v1/capabilities` | Protocol version 1, HTTP/2 and provisional transfer ceilings |
| `POST /api/v1/auth/password`, `/auth/password/replace`, `/auth/totp/enroll`, `/auth/totp/confirm` | Restricted onboarding only; see [Account onboarding](#account-onboarding). No session, cookie or file access is issued |
| `POST /api/v1/sessions`, `DELETE /api/v1/sessions/current`, `/api/v1/uploads…`, `/api/v1/catalog`, `/api/v1/files/…` | Device sign-in and the standard upload, list and download routes of the [vertical slice](vertical-slice.md); every transfer route requires a live device session |

HEAD is supported without response bodies on the GET endpoints. Other methods return a typed HTTP 405 error; unknown routes return a typed HTTP 404 error. Responses are not cached and contain no credentials or identity secrets. Unknown admin paths, directories and traversal attempts return typed errors rather than falling back to HTML. Only compiled production assets are embedded; source files, dependency directories and source maps are not served. Readiness checks use a one-second context and test the database connection, not ongoing storage capacity or filesystem integrity. Startup performs the filesystem probes.

The admin shell's readiness probe sends no credentials, follows no redirects, aborts after five seconds, and offers a manual retry with safe error text. Readiness is public process information, not authorization or proof of transfer support. The shell provides native keyboard-accessible Light/Dark/System radio choices, persists only that preference in browser local storage, follows OS changes in System mode, and syncs changes across tabs. Blocked storage still permits a theme choice for the current page and explains that it cannot be saved. A blocking same-origin theme script runs before CSS/application rendering. All loading, success and failure states support both themes. A skip link and visible keyboard focus are provided.

The shell uses a same-origin Content Security Policy without inline scripts/styles or eval, blocks framing, forms, objects and base-URL changes, and disables unused camera/microphone/geolocation permissions. These headers do not implement authenticated admin security: role checks, secure session cookies, CSRF/origin protection and sensitive-action reauthentication still belong to Chunk 22. The public shell never grants administrative access.

The executable now opens TCP and UDP listeners for the same API, advertising HTTP/3 only through the dual-transport entry point. TCP supports HTTP/2 and HTTP/1.1, with TLS 1.3 required. Desktop certificate inspection and authenticated connections try HTTP/3 first, then verified HTTP/2 if QUIC fails. Each replacement connection repeats that order, with no timed upgrades and no mutation 0-RTT; see [performance measurements and operation](performance-baseline.md). The provisional ceilings remain 1–8 MiB chunks, 8 MiB requests, 1 MiB manifests and 1,048,576 chunks. Local measurements do not qualify target-hardware resource ceilings.

## Application logs

After administrator sign-in, **Application logs** shows server startup/shutdown, transport warnings, HTTP outcomes/durations, safe rejection reasons and internal failure diagnostics. Search messages (case-insensitive), filter Info/OK/Warning/Error, refresh, or load older results. Access is checked against the live administrator session on every request; signing out removes the viewer. `GET /api/v1/admin/logs` accepts `q`, `level` and an exclusive numeric `before` cursor, returning up to 100 newest matching entries.

Logs persist in `SERVER_SEND_CONFIG_DIR/logs/app.jsonl`, rotating at 2 MiB with one retained file (`app.jsonl.1`). The viewer keeps at most 5,000 recent entries in memory; restart loads retained files. Directories/files use 0700/0600 permissions. Logging write failures are reported to stderr and recent events remain visible in memory. These are bounded operational diagnostics, not permanent audit history. Failures before server initialization remain in process/container output. Credentials, authentication bodies, file contents and URL query strings are excluded; request routes use templates rather than submitted names. Internal errors can contain storage paths, so logs are administrator-only.

Folder deletion refuses active uploads. Expired open uploads no longer block deletion indefinitely; finalizing uploads and legacy uploads with no expiry remain protected. Deletion is still permanent and removes the selected tree and its expired upload objects. Large deletions can spill SQLite temporary tables to disk: the Docker image sets `SQLITE_TMPDIR=/staging` to support its read-only root. See the [Unraid permission-error remedy](unraid-testing.md#folder-deletion-reports-a-sqlite-permission-error) for existing containers.

## Environment settings

`config.Load` reads process environment settings. `config.FromEnvironment` accepts a lookup function for isolated testing or composition. An unset variable keeps its default; an explicitly empty value is rejected except when both TLS paths are empty. Unknown variables are ignored.

| Variable | Default | Validation |
| --- | --- | --- |
| `SERVER_SEND_TCP_ADDRESS` | `:58443` | Numeric IPv4/IPv6 or empty host, decimal port 1–65535 |
| `SERVER_SEND_UDP_ADDRESS` | `:58443` | Same syntax; may use the same port as TCP |
| `SERVER_SEND_CONFIG_DIR` | `/config` | Absolute non-root path |
| `SERVER_SEND_DATA_DIR` | `/data` | Absolute non-root path |
| `SERVER_SEND_STAGING_DIR` | `/staging` | Absolute non-root path |
| `SERVER_SEND_TLS_CERT_FILE` | Empty | Absolute certificate path when provisioned |
| `SERVER_SEND_TLS_KEY_FILE` | Empty | Absolute key path when provisioned |
| `SERVER_SEND_TLS_IPS` | Empty | Comma-separated client-visible unicast IPv4 addresses; enables private provisioning instead of external files |

IPv6 listen addresses use brackets, such as `[::1]:58443`. Hostnames are deliberately unsupported for bind addresses; use an explicit local IP or an empty host to bind all interfaces. These are server bind settings, not client connection URLs.

Storage directories must be distinct and not lexically nested after path cleaning. Shared parent directories and similar names are permitted. The certificate and key paths must either both be supplied or both be empty; they may refer to the same combined PEM file. Whitespace surrounding a path and NUL bytes are rejected; spaces inside valid paths are supported.

Empty TLS paths remain valid for configuration parsing. Startup requires either a nonempty private IP list or both external certificate/key paths. Startup also rejects unreadable/malformed/mismatched certificate keys, expired/future leaf certificates and certificates restricted to non-server uses. It does not verify a certificate's hostname or trust chain against the deployment: connecting clients must do that.

`config.CheckStorage` requires all three directories to exist and probes file creation, writing, syncing, removal and directory syncing. It resolves symlinks and compares directory/ancestor identities to reject aliased or nested paths, including bind-mount aliases. It never creates a missing mount path. The config directory must have no group/other permissions (normally 0700); an existing database must be a regular private file (normally 0600), not a symlink. Protect these directories against modification by other local users. Startup checks do not eliminate filesystem races in an untrusted parent directory.

Operators must still provide persistent mounts and a local filesystem suitable for SQLite WAL; these checks cannot prove a directory is mounted or qualify SMB/NFS locking. Startup now holds native `.serversend.lock` files on config, data and staging before initializing certificates, metadata or objects. Another cooperating server or operator command sharing any volume is refused. Locks release on close/process exit; leave the lock files in place, because unlinking a live lock permits a second process to lock a different inode. Trusted directories and actual filesystem locking are required. Capacity reservations and general durable object reconciliation remain storage work. Independent copied volumes/identities cannot be fenced by these locks.

Use `serversend -backup /absolute/new/directory` with the server stopped for the initial standard-data snapshot. See [backup and isolated restore](backup-restore.md) for included keys/objects, private permissions, upgrade rollback and remaining qualification gates.

## Metadata and identity

`internal/database` owns schema version 5 in `/config/serversend.sqlite`, using the shared WAL/FULL-sync connection and transactional migration policy. The singleton identity contains a random 128-bit hexadecimal server ID and a 32-byte Ed25519 seed. Identity creation runs in an immediate transaction, so concurrent initializers converge on one identity. Invalid existing identity material fails startup without being replaced. A newer database schema is rejected without downgrade. Version 2 adds initial account, upload, reservation and chunk metadata foundations without changing the version-1 identity. Version 3 adds authentication state, scoped challenge hashes and audit events while preserving existing account/upload rows. Version 4 adds encrypted TOTP secrets with replay watermarks, recovery-code hashes and an independent second-factor backoff, preserving version-3 credentials, challenges and audit events (frozen `testdata/v3.sql`). Version 5 adds device sessions, upload intent columns, a flat file/version catalog and a per-account catalog revision for the [vertical slice](vertical-slice.md) (frozen `testdata/v4.sql`). See [SQLite foundation](sqlite-foundation.md) for repositories, upgrade fixtures and qualification status.

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
curl --cacert /tmp/serversend-ca.crt https://127.0.0.1:58443/readyz
```

Wait until the logs report that HTTPS is listening before copying the certificate. To serve a LAN address instead, set the published host address and certificate IP explicitly (substitute the real Docker host address):

```sh
SERVER_SEND_BIND_IP=192.168.1.20 SERVER_SEND_TLS_IPS=192.168.1.20 \
  docker compose -f apps/server/deploy/compose.yaml up -d --build
```

Then use `https://192.168.1.20:58443/readyz` with the exported CA, or open `https://192.168.1.20:58443/admin/` after installing browser CA trust. The default host port is 58443; `SERVER_SEND_PORT` changes the published port independently of the certificate. Run `docker compose -f apps/server/deploy/compose.yaml down` to stop/remove the container while keeping its named config/data/staging volumes. Preserve those volumes across replacements. The Compose example supplies the process foundation and public admin shell; there are no login or upload workflows yet.

The optional container check requires Docker, OpenSSL, curl, grep and cmp already installed. It uses disposable named volumes. The default mode supplies a short-lived external test certificate; `--private-ipv4` supplies no certificate and verifies automatic provisioning, private key permissions and CA stability across restart. Both modes check verified HTTPS, embedded admin HTML/JavaScript/CSS before and after restart, private metadata permissions, a read-only root filesystem, identity persistence and SIGTERM exit status, then remove their test containers/volumes. It does not touch host deployment directories.

The multi-stage image builds the CGO executable and runs it as UID/GID `10001:10001`. Mount pre-created host directories with that ownership at `/config`, `/data` and `/staging`; set config permissions to 0700. Supply a readable certificate/key, for example in a separate read-only `/certs` bind mount:

```sh
docker run --rm --name serversend --read-only --stop-timeout 15 \
  --mount type=bind,src=/absolute/host/config,dst=/config \
  --mount type=bind,src=/absolute/host/data,dst=/data \
  --mount type=bind,src=/absolute/host/staging,dst=/staging \
  --mount type=bind,src=/absolute/host/certs,dst=/certs,readonly \
  -e SERVER_SEND_TLS_CERT_FILE=/certs/server.crt \
  -e SERVER_SEND_TLS_KEY_FILE=/certs/server.key \
  -p 58443:58443/tcp serversend:dev
```

Use bind mounts or explicitly managed named volumes that survive container replacement; do not rely on anonymous volumes. `--mount type=bind` rejects missing source paths instead of creating them. Publish UDP 58443 as well as TCP for HTTP/3 evaluation; the Compose examples include both. No app state is written into the image layer. The container image remains experimental, with actual Unraid deployment qualification open.

Tests cover configuration parsing/bounds; real filesystem permission and alias failures; metadata constraints and future-schema rejection; concurrent identity initialization and restart persistence; safe typed endpoint errors; embedded asset routing/MIME/security headers and rejected traversal/mutations; verified TLS 1.3/HTTP/2 requests; invalid TLS before metadata mutation; certificate renewal; listener failures; and graceful draining. Admin UI tests cover both themes, blocked storage, early theme application, OS/tab changes, malformed readiness responses, retry/timeout and disposal. Pairing endpoint integration, complete account/storage workflows and full deployment qualification remain. Chunk 03 still awaits Windows/macOS native persistence qualification; the Chunk 04 process/shell work does not close that dependency. Chunk 02 shared contracts are complete; they do not expose these runtime workflows.


Private IP TLS was verified locally with Go vet/race tests, real verified TLS 1.3/HTTP2 requests, untrusted/wrong-IP rejection, deterministic renewal/expiry, restart and IP changes, filesystem failures and both Docker smoke modes. The Compose example was also run with fresh named volumes on an ephemeral loopback port, returned a verified HTTPS readiness response, and was cleaned up. Public CA trust installation in Windows/macOS/Linux browsers remains a manual deployment step, not an automated acceptance claim.

## Account onboarding

The executable composes the account service at startup with production Argon2id password hashing (`golang.org/x/crypto/argon2`, approved 2026-11-16) and the durable TOTP protection key in `/config/totp.key`. Back up `totp.key` together with the database: once any account has enrolled, a missing key stops startup instead of being regenerated.

Create the first administrator from a shell on the host or inside the container. This is the only bootstrap path; there is no network setup endpoint.

```sh
serversend -bootstrap-admin admin
# Stop the serving container first; use a one-shot container with the same mounts/settings.
```

The command prints a one-hour temporary password to standard output only (never the log) and exits without serving. Stop the serving process before running it: deployment locks also apply to `-check`, bootstrap and recovery commands. If the password expires or is lost before that administrator finishes TOTP enrollment, run `serversend -recover-bootstrap-admin admin`; it works only for the sole, still-unenrolled first administrator. The administrator then signs in, replaces the password and enrolls TOTP through the endpoints below. A fully onboarded account can open a slice device session (`POST /api/v1/sessions`); administrators can create users with `POST /api/v1/admin/users` after a recent sign-in and list users with `GET /api/v1/admin/users`. Resets and disablement still have no network caller.

Passwords are hashed with Argon2id using 64 MiB, three passes and four lanes (the RFC 9106 memory-constrained profile), a random 128-bit salt and the self-describing PHC string format, so parameters can be raised later without invalidating stored hashes. At most two hashes run at once, bounding KDF memory near 128 MiB. Verification rejects stored parameters above 256 MiB, ten passes or sixteen lanes. These parameters are not yet calibrated on target Unraid hardware. State-machine and adapter tests inject an explicitly test-only fast hasher and are not cryptographic password tests; `auth/argon2id_test.go`, `server/accounts_test.go` and the command tests use the real one.

The service issues random 256-bit temporary passwords for one hour and bootstraps only while no accounts exist. An explicit local recovery operation can reissue credentials only for the sole, still-unenrolled first administrator. Recovery never enables a disabled account or resets an active account. Usernames are 1–128 ASCII letters/digits/dots/underscores/hyphens, stored lowercase; Unicode aliases and surrounding whitespace are rejected. Permanent passwords require 12–128 Unicode characters, at most 256 UTF-8 bytes, without control characters; the temporary password cannot be reused.

Password verification yields a ten-minute-or-shorter, single-use challenge restricted to `replace_password`, `enroll_totp` or `second_factor`. Only its domain-separated SHA-256 hash is stored. Reauthentication invalidates the previous attempt for that account; password replacement consumes all attempts atomically and requires a fresh permanent-password attempt for TOTP. Neither replacing a password nor account state `active` grants a device/browser session. Reset preserves enrolled TOTP. Disable, enable, reset and replacement increment a revocation epoch; future session checks must compare it on every request. Audit and state updates share the same short transaction.

Failed password attempts persist account backoff from one second up to five minutes. Unknown and blocked accounts perform dummy verification. A fixed-size global token bucket (burst ten, two attempts per second) and at most two simultaneous password operations bound unknown-username attacks and KDF resource use without per-input maps. These are provisional limits, pending KDF calibration on target hardware.

The adapter recognizes only `POST /api/v1/auth/password`, `/auth/password/replace`, `/auth/totp/enroll` and `/auth/totp/confirm`. All but the first carry their challenge in a Bearer header. Replacement ends with an empty 204 response; enrollment (body `{}`) returns the secret, provisioning URI and QR PNG; confirmation (body `{"code":"123456"}`) returns the recovery codes once. See [TOTP foundation](twofactor-foundation.md). JSON bodies are capped at 4096 bytes; malformed/ambiguous/trailing bodies, compressed bodies, cross-origin browser requests, scope bypass and challenge replay are rejected. Responses are uncached, contain safe errors and set no session cookies. No administrator, device, file or setup endpoint is opened by this adapter. It is not the completed browser session/CSRF/reauthentication implementation.

## Firefox certificate compatibility

Generated private HTTPS authorities and server certificates use ECDSA P-256.
Earlier development images generated Ed25519 certificates, which Firefox does
not support for TLS authentication (distinct from X25519 key exchange). This
causes `SSL_ERROR_NO_CYPHER_OVERLAP`; changing cipher preferences does not fix it.
See [Mozilla’s issue](https://bugzilla.mozilla.org/show_bug.cgi?id=2037389).

Existing TLS material is deliberately preserved on image updates. To migrate a
local Compose deployment, build the fixed image, stop the serving process, and
move only its TLS directory to a private backup before restarting:

```sh
docker compose -f apps/server/deploy/compose.yaml build
docker compose -f apps/server/deploy/compose.yaml stop server
docker compose -f apps/server/deploy/compose.yaml run --rm --no-deps --entrypoint sh server -c 'test ! -e /config/tls-ed25519-backup && mv /config/tls /config/tls-ed25519-backup'
docker compose -f apps/server/deploy/compose.yaml up -d --force-recreate server
```

Run this migration once, only for the affected Ed25519 installation. If the
backup path already exists, the command refuses to overwrite it; inspect the
existing backup before proceeding. Keep it protected because it contains keys.
The new certificates change the desktop approval fingerprint and public CA.
Compare the new fingerprint with the server log and approve it in the desktop;
if you previously imported the old CA into Firefox, replace that trust with the
new `/config/tls/ca.crt` through the same authenticated operator channel.
A browser without the new CA will show an unknown-issuer warning after the TLS
handshake succeeds. Accounts, TOTP keys, uploaded files and server identity are
outside the TLS directory and are retained. Do not delete Docker volumes.


## User list and file deletion iteration

The admin list refreshes every five seconds. Newly issued user temporary passwords are stored encrypted under the existing `/config/totp.key`, with authenticated context distinct from TOTP secrets. They survive reloads and restarts until replacement or the existing one-hour expiry. Replacing or resetting the password clears the old envelope transactionally; a list request purges expired envelopes. Old hash-only credentials cannot be recovered. The administrator bootstrap credential remains a one-time local output. Responses containing pending passwords use `Cache-Control: no-store`; the browser does not persist passwords locally.

`DELETE /api/v1/catalog/{entry}` permanently deletes the authenticated user's file (including all versions) or complete directory tree. Other accounts see NotFound; admin sessions cannot use transfer routes. Active uploads block deletion. Metadata changes and catalog revision updates commit together; object removals are journaled so interrupted cleanup resumes on server restart. The desktop asks for confirmation before sending the request.
