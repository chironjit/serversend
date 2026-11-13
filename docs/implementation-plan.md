# ServerSend implementation plan

ServerSend provides private file storage and backup through an Unraid Docker server and a desktop application. Users can upload large batches in the background, retrieve selected files, connect to multiple independent servers, and use multiple devices. Optional private uploads are encrypted on the desktop so the server never receives plaintext contents or usable decryption keys.

This is the implementation specification. Work is divided into dependency-ordered chunks in [Implementation tasks](implementation-tasks.md). Application implementation has not started as part of this planning change.

## Scope and operating assumptions

The first release supports one server process per storage directory, independent accounts on each server, Linux-first desktop clients, followed by Windows, with macOS also required, and a Linux Docker container deployed on Unraid. Validate all three desktop platforms before advertising support. Tailscale already runs on the Unraid host. The server does not require public internet exposure; access is through the LAN or tailnet.

Included features are administrator-created users, mandatory password replacement and authenticator 2FA, device sessions, resumable uploads/downloads, persistent desktop queues, private encryption, file versions, trash, storage accounting, live transfer statistics, and deployment/restore procedures.

Automatic folder watching, bidirectional synchronization, server-to-server replication, public sharing, shared identity across servers, cross-user deduplication, mobile clients, and unattended transfers while logged out of the operating system are deferred. Selecting a batch queues a backup of those files; deleting a source file does not delete its server copy. Uploads continue while the application runs in the tray, but not while the device is asleep or the application is fully exited.

No arbitrary batch file-count limit is imposed. Storage capacity, configurable resource limits, file-format limits, and operating-system limits still apply. Large batches must use bounded memory and paginated database work.

The primary workload is very large numbers of small files, with some large files. Maximize useful bandwidth within explicit memory budgets on both client and server. Measure per-file request, metadata, allocation and disk overhead as well as bulk throughput; do not achieve speed by buffering entire batches. Hardware, network speed and numeric memory budgets remain to be measured on the deployment.

## Dependency preferences

Prefer official or upstream-maintained modules where suitable, and established maintained implementations for QUIC and encryption. The user has selected Tailwind CSS and requested `rsc.io/2fa`. SQLite has no selected Go driver. Propose the exact driver and any other new dependencies before adding them, following `AGENTS.md`; a preference for established libraries does not name a particular module. Reuse approval already given for a named dependency rather than asking again.

The requested `rsc.io/2fa` module is a command-line TOTP/HOTP authenticator, not an importable server verification library. Keep it as the user's stated preference, but resolve a suitable library or reviewed adaptation with the user before implementing server TOTP. Do not silently substitute a different module or run its CLI as the production verification service. [rsc.io/2fa documentation](https://pkg.go.dev/rsc.io/2fa)

`quic-go` remains the proposed QUIC implementation; the encryption-format prototype evaluates established cryptographic libraries and standard Go primitives. Record exact new dependency choices before installation; no dependencies are added by this plan.

## Repository boundaries and coding rules

Keep the existing application layout: desktop code belongs in `apps/desktop`, server code in `apps/server`, reusable code and tests in root `shared`, and project documentation in root `docs`. Retain the existing Go module paths and workspace entries; no application-folder relocation is required.

```text
apps/
  desktop/
    main.go
    internal/
      app/                 application lifecycle and Wails bindings
      profiles/            server profiles and endpoint selection
      identity/            device sessions and credential-store adapter
      queue/               persistent jobs and scheduling
      transfer/            file scanning and transfer orchestration
      vault/               key unlocking and local encryption workflow
      catalog/             file browsing and private metadata index
      database/            desktop migrations and repositories
    frontend/src/
      features/            servers, onboarding, transfers, files, devices, vault
      components/          reusable desktop UI elements
    build/                 desktop packaging only
    tests/                 desktop integration and UI tests
    docs/                  desktop development and user instructions
  server/
    cmd/serversend/         composition and process startup
    internal/
      config/              environment and configuration validation
      identity/            persistent server identity
      auth/                onboarding, passwords, TOTP, sessions
      users/               accounts, roles, device management
      catalog/             files, versions, directories, trash
      uploads/             upload state and finalization
      storage/             durable object storage and recovery
      database/            schema, migrations, repositories
      metrics/             accounting and transfer observations
      audit/               administrative events
      httpapi/             handlers, middleware, transport setup
    admin/src/             browser admin interface and its tests
    deploy/                Dockerfile, Unraid template, examples
    tests/                 server integration and container tests
    docs/                  operations and deployment instructions
shared/
  api/                   versioned request, response, and error contracts
  protocol/              manifests, limits, capability negotiation
  client/                reusable typed API client
  fileformat/            versioned private-file format and crypto adapters
  testutil/              reusable fixtures and fault-injection helpers
  tests/                 cross-component acceptance suites
docs/                    project plans and cross-component architecture decisions
```

Create folders when their first implementation needs them, rather than creating empty abstractions. Shared code must have a plausible reusable purpose; server database models and desktop UI state stay with their owners. Shared packages never import desktop or server packages, and desktop and server never import each other's internal packages. Server deployment assets belong in `apps/server/deploy`; the existing Wails Docker build files must be inspected before deciding which are desktop build tooling.

Keep entry points thin. Separate transport handling, business rules, and persistence. Prefer files named for a responsibility such as `password_reset.go`, `upload_finalize.go`, or `queue_retry.go`; split a file when it mixes independent responsibilities. Avoid general-purpose `utils` packages and interfaces that merely duplicate every method of a concrete type. Shared protocol interfaces belong in `shared`; implementation-specific dependency interfaces belong near their consumer.

Every implemented scenario includes tests in the same change: normal operation, invalid input, authorization, relevant boundaries, and failure/recovery behaviour. Keep Go unit tests beside their implementations, UI tests beside their features, and integration suites in the owning application's `tests` directory. Generated bindings are regenerated, not manually patched.

Project documentation lives in the root `docs` directory. Reserve `shared` for reusable code and its tests, not project documentation. Other root-level files are limited to workspace, repository, and CI entry points; component-specific scripts and configuration remain under their owner. CI entry points invoke those scripts.

## Application architecture

The Go server serves the API and a compiled Vue/TypeScript admin interface. The desktop retains Wails with Vue/TypeScript; its Go layer performs filesystem access, transfers, and encryption. File bytes never pass through the frontend bridge. The frontend receives bounded progress events and paginated results.

Use Tailwind CSS for both the desktop UI and server admin UI. Every screen, dialog, error, empty state and loading state must support light and dark themes. Provide Light, Dark and System choices, persist the user's choice, and apply it before initial rendering to avoid a wrong-theme flash. Test keyboard focus, readable contrast and theme changes in both applications. Additional component libraries remain subject to dependency approval. Tailwind supports dark variants and explicit/system-driven theme selection. [Tailwind dark mode](https://tailwindcss.com/docs/dark-mode)

Use SQLite for metadata and local queue state, and filesystem objects for file contents. Choose and pin a maintained Go SQLite driver during the foundation chunk, validating platform builds and migrations. Use short transactions, foreign keys, a busy timeout, and controlled write concurrency. SQLite WAL requires same-host storage, so the server database must not live on an SMB/NFS mount. [SQLite WAL documentation](https://www.sqlite.org/wal.html)

The server database tracks users, onboarding state, TOTP configuration, recovery-code hashes, devices, sessions, server settings, vault envelopes, files, immutable versions, upload sessions, chunks, reservations, trash, and audit events. Use opaque identifiers and explicit ownership on all user resources. Blob paths derive from validated opaque IDs, never user filenames.

The desktop database tracks profiles, approved endpoints, device registrations per server, jobs, file tasks, chunk acknowledgements, source fingerprints, retry schedules, catalog revisions, and encrypted metadata caches. Key all remote state by server ID and account ID. Two servers may use the same username or file ID without collision.

## Server identity and connection profiles

Persist a random server ID and a server identity key under `/config`. The ID identifies a logical server; it is not itself proof of trust. Pairing must authenticate the identity key over verified TLS or by verifying an administrator-provided fingerprint through an independent channel. Specify the signed challenge and certificate/identity association in the protocol decision before implementing pairing.

A desktop profile holds the display name, server ID, trusted identity, approved LAN and tailnet endpoints, account ID, and preferences. Credentials are scoped to this profile and never sent to an unverified replacement endpoint. Redirects must not forward authorization to a different origin. Certificate failures or identity changes require an explicit repair flow, not silent fallback.

Support multiple simultaneously signed-in profiles with a combined transfer queue and separate file catalogs. LAN and Tailscale addresses of the same server are routes within one profile. Route changes preserve upload IDs and retry only missing work. Automatic endpoint selection probes only configured endpoints, uses timeouts, and remembers recent failures. Independent servers have independent passwords, TOTP enrollment, administrators, and vaults.

## Accounts and device sessions

Bootstrap the first administrator through a local container command or one-time setup token with an expiration. No default administrator password or permanently open setup endpoint is permitted. Administrators complete password setup and 2FA before receiving administrative access.

Account onboarding is a server-enforced state machine:

```text
temporary password issued
  -> temporary credential validated
  -> permanent password chosen
  -> TOTP enrollment verified and recovery codes issued
  -> active account
```

Onboarding credentials only authorize their required next steps. Reject file, device-management, and admin API access until onboarding is complete. Temporary passwords are randomly generated, stored as hashes, expire, and are replaced atomically; interrupted enrollment can safely continue through a new restricted authentication attempt. Show a generated temporary password once and require the administrator to deliver it outside the application.

Use Argon2id for password hashing with versioned parameters and deployment-calibrated cost. Rate-limit password and TOTP attempts with bounded backoff. Store TOTP secrets encrypted under a server configuration key; store recovery codes as hashes. Verify enrollment before activating it, bound clock skew, and prevent reuse of an accepted TOTP time step. Never log passwords, codes, bearer tokens, or vault keys.

After onboarding, a new device signs in with password plus TOTP or a single-use recovery code. Each installation has a local device identity and each server issues a distinct registration. Use short-lived access tokens renewed silently through rotating refresh credentials. Persist the usable refresh credential in the operating system's credential store and store only its hash on the server. Serialize refresh per account to avoid races between transfer workers. Define refresh replay handling and interrupted rotation recovery explicitly.

Authenticated desktop devices stay signed in without routine password/2FA prompts, including after application or OS restart, long inactivity, offline periods and ordinary server/container upgrades. Device authorization has no routine inactivity or absolute expiry; individual access tokens still expire and refresh credentials rotate. Require interactive sign-in only after explicit logout, device/session revocation, account reset/disablement, detected credential compromise, lost local credentials or a trust-breaking server change. A locked operating-system credential store may require local OS unlock, not a fresh ServerSend login. Never fall back to plaintext credentials when secure storage is unavailable. Browser admin sessions retain their own expiry and sensitive-action reauthentication policy.

Check active session/device status on authorized requests so revocation does not wait for access-token expiry. Revocation stops new requests and cancels active streams within a tested bounded interval. Users can list and revoke their own devices; administrators can disable accounts and revoke their sessions. The UI describes device names and last activity without implying the name is a trusted identity.

An administrator password reset issues a new temporary password, requires replacement, and revokes sessions; existing TOTP remains required. Lost-TOTP recovery is a separate audited action that requires administrator reauthentication, preserves the password requirement, revokes sessions, and forces fresh enrollment. Neither operation changes or reveals the private vault key.

Admin browser sessions use secure HttpOnly cookies, appropriate SameSite policy, CSRF protection for mutations, and origin validation. Administrative actions enforce roles server-side and produce audit events. Being on the tailnet never replaces application authorization.

## Transfer protocol and durable storage

Version the API under `/api/v1`. Publish capabilities including supported protocol/file-format versions, maximum request size, chunk-size range, and transport support. Use typed errors for authentication required, conflict, capacity exhaustion, retryable overload, unsupported format, and missing source. Never interpret all failures as retryable.

The API surface includes onboarding/login/session endpoints, user/admin endpoints, catalog/version endpoints, vault-envelope endpoints, upload lifecycle endpoints, chunk reads/writes, and a reconnectable event stream. Upload creation and finalization use idempotency keys scoped to account and operation.

```text
Create upload and reserve capacity
  -> send bounded chunks
  -> persist and acknowledge individual chunks
  -> reconcile missing chunks after reconnect
  -> validate manifest and finalize immutable version
  -> publish catalog revision
```

Choose an initial chunk size through a benchmark; evaluate 8 MiB as a starting point, not a fixed correctness assumption. Limit workers, open files, queued buffers, server request bodies, and global/per-user work. Persist discovered file tasks incrementally. The server independently enforces limits rather than trusting client capability declarations.

Each chunk has an upload ID, index, byte count, and checksum of the transmitted bytes. Repeating an identical chunk is safe; a conflicting replacement is rejected. Track checksums incrementally while reading, not by loading whole files. Server acknowledgements require durable chunk data and recoverable metadata. The client reconciles the server's committed chunk list after uncertain acknowledgements.

Use immutable chunk objects plus manifests for the first storage layout, avoiding assembly of a second complete copy. Ordinary objects contain plaintext bytes; private objects contain ciphertext bytes. Keep a storage abstraction so packing small objects can be introduced after measurement without changing the public API. Empty files have explicit valid manifests.

Finalization checks ownership, expected chunk count/order/length, checksums, capacity, and catalog preconditions. Commit the visible version only after all referenced objects are durable. Database and filesystem writes are not one transaction: record pending transitions, use idempotent reconciliation, and tolerate recoverable orphan objects. Test crashes at every transition.

When `/staging` and `/data` are different filesystems, rename is not atomic across them. Copy into a temporary object on the destination filesystem, verify and flush it, rename within that filesystem, then record the committed reference. Reserve capacity for this temporary overlap. A finalization acknowledgement must never precede this process.

Downloads are resumable into local temporary files, verified, flushed, and renamed into the requested destination. Private downloads authenticate each encrypted object and the complete manifest before publishing a completed file. Never overwrite a user's existing local file without an explicit conflict policy.

## Desktop queue and file behaviour

Persist jobs before starting network work. Model states such as queued, scanning, running, paused, waiting for network, waiting for authentication, waiting for vault unlock, retry scheduled, complete, cancelled, and failed. State transitions and reasons are durable. One unavailable server must not block other profiles.

Folder scans stream into the local database. Preserve relative hierarchy, empty directories, Unicode names, and zero-length files. Do not follow symlinks by default; report skipped links and unsupported special files. Define case collisions, reserved names, invalid destination characters, and path-length errors for each destination platform. Resolve paths safely and refuse traversal or symlink escape during downloads.

Detect deleted, unreadable, replaced, or changed source files before resuming and after reading. Restart changed files as new upload attempts; never combine chunks from different file generations. Ordinary portable checks cannot guarantee a consistent snapshot of an actively changing file. Report that limitation and require stable sources for a successful backup; filesystem snapshot integration is deferred.

Provide pause/resume/cancel, retry failed items, bandwidth limits, per-file/batch progress, useful byte rates, and clear completion with skipped/failed counts. Completed files in a partially failed batch remain available. Avoid promising an ETA before the batch size is known. Tray operation, optional launch at login, sleep/wake recovery, and explicit quit behaviour receive platform tests.

## Private encryption and recovery

Users choose Standard or Private for a batch and can remember a default per profile. Record the mode on each immutable version; changing a default does not retroactively transform stored files. Standard transfers use TLS and may be plaintext at rest. Private data is encrypted before leaving the desktop, including names, paths, original hashes, and sensitive metadata.

Private encryption and vault setup are optional and separate from mandatory account password/2FA setup. Users can skip vault creation and use Standard uploads/downloads immediately. Offer vault creation when Private is first selected; cancelling it leaves ordinary account access usable. Remembering device sign-in does not automatically unlock the vault. A separate opt-in to remember the vault in secure local storage allows private transfers without repeated vault-passphrase entry while that storage is accessible.

One independent vault belongs to each server account. Generate a random vault master key locally. Generate per-file-version encryption keys and wrap them under the vault key. The server stores only wrapped keys, encrypted metadata, and ciphertext. The server still sees ownership, object counts, ciphertext sizes, and activity times. Private search uses a local index; persist that index encrypted and discard decrypted views when locking the vault.

Wrap the vault key separately for a strong vault passphrase and a randomly generated recovery secret. The vault passphrase is distinct from the account password. Use a memory-hard, salted passphrase derivation function with versioned, bounded parameters. Authenticated server access retrieves the encrypted envelopes; successful local unlock adds the vault to a new device. A recovery package contains the recovery secret, identifiers, and format information, and must be stored away from the server. Recovery still requires the server's intact encrypted data and appropriate account access.

Store remembered local secrets through platform credential-store adapters. If secure storage is unavailable or locked, require manual unlock; never silently persist plaintext keys. Define vault auto-lock and whether active private jobs keep it unlocked, and expose that choice clearly. Password reset does not change envelopes. Changing the vault passphrase rewraps the key atomically using a revision precondition; concurrent devices must refresh before overwriting envelopes.

The encryption-format prototype is a prerequisite for production private uploads. Use a maintained authenticated-encryption implementation, authenticated manifests, and versioned serialization; do not invent cryptographic primitives. Tink Streaming AEAD is an evaluation candidate, but a continuous encrypted stream cannot simply be resumed by starting a new stream in the middle. Tink binds segments to their position within the ciphertext. [Tink Streaming AEAD](https://developers.google.com/tink/streaming-aead)

Evaluate independently authenticated bounded objects using an established AEAD API, with a manifest authenticating the file version, ordered objects, lengths, and encrypted metadata. Specify key/nonce uniqueness, associated data binding to server/account/file/version/chunk, and protection against chunk swapping, truncation, and duplication. A retransmission sends persisted identical ciphertext; a fresh encryption attempt uses fresh library-managed randomness and must not overwrite an already accepted immutable object. Bound encrypted staging space and persist enough state to recover safely. Release is gated on format review, test vectors, tamper tests, and restart tests.

An authenticated manifest detects content tampering, but a server may replay an older valid catalog to a new device. Document this freshness limitation; do not claim protection against all malicious-server rollback without an additional trusted revision mechanism.

Revoking a device blocks future server access; it cannot erase files or keys already copied. Emergency vault-key rotation for future versions is a separate design; it does not restore confidentiality of previously downloaded data. Loss of every usable vault key, passphrase, and recovery secret makes private data unrecoverable by administrators. Vault setup and recovery export must tell the user to store the recovery key securely outside the server: if they lose their passphrase and all usable keys or recovery copies, their private files cannot be recovered, even after an administrator resets their account password. Require acknowledgement of this explanation before the first private upload. Losing only the exported recovery key does not destroy access while another valid unlock method remains available.

## Versions and multiple devices

Files have stable logical IDs and immutable version IDs. Changes create new versions; publishing a new head uses an expected revision. Concurrent uploads from two devices preserve both versions and surface a conflict instead of silently discarding either. Use opaque directory and file IDs in private catalogs; names are decrypted on the client. Resolving equal private paths across devices is client-assisted and revision-checked, without introducing plaintext name indexes on the server.

Device queues do not synchronize. Other devices see committed catalog revisions and can download available versions, but cannot continue a local job unless they explicitly start a new job with their own source files. Catalog events are hints; reconnect uses revision-based reconciliation or a paginated full refresh after history expiry.

Deletion moves items into trash with a configurable retention period. Proposed initial retention is 30 days; expose it in server configuration and the admin UI. Referenced objects cannot be garbage-collected. Purging old versions/trash and expiring abandoned uploads must coordinate with readers, writers, reservations, and restore operations.

## Unraid deployment and operations

Talos Linux is an operating system for Kubernetes hosts, not the application base image for this Unraid Docker deployment. Use a conventional minimal runtime image, with the exact choice based on the approved SQLite driver's runtime requirements. Keep Unraid as the host. [Talos Linux](https://www.siderolabs.com/talos-linux)

Build a multi-stage Docker image containing the Go server and compiled admin assets. Run as a configurable non-root UID/GID, provide health/readiness checks and graceful shutdown, and avoid privileged mode and the Docker socket. Package a versioned Unraid template and a Docker example under `apps/server/deploy`.

| Mount | Contents | Requirement |
| --- | --- | --- |
| `/config` | SQLite database, server identity, TOTP protection key, configuration and certificates | Persistent local appdata storage with tested locking and permissions |
| `/data` | Committed objects, manifests and retained versions | Persistent Unraid storage with capacity monitoring |
| `/staging` | Incomplete objects and transfer staging | Persistent storage for restart recovery; may use a separate pool |

Document pool/array selection and test the actual Unraid storage paths. Never assume Docker persistence alone provides a backup. Check writability and available capacity at startup; fail clearly on invalid mounts. Display free space separately for each filesystem rather than summing duplicate mounts of the same filesystem.

Expose configurable TCP and UDP ports, initially 8443 for both, avoiding collisions with the Unraid administration UI. Use the existing Unraid-host Tailscale installation and verify both protocols reach the container. A sidecar is unnecessary for the primary deployment; document it only as an optional alternative. Tailscale Serve documents HTTP/TCP forwarding, so its availability alone does not establish an HTTP/3 path. [Tailscale Serve](https://tailscale.com/docs/features/tailscale-serve)

Use verified TLS for LAN and tailnet routes. Tailnet certificates must match the configured DNS identity; a raw LAN IP needs a trusted certificate with the correct IP identity, or a deliberately designed desktop pairing flow. Browser admin access should use a trusted hostname/certificate. Never use insecure TLS verification as a convenience fallback. Certificate renewal and restart behaviour belong in operations tests.

Back up the SQLite database using a consistent backup operation and include identity, configuration keys, vault envelopes, and referenced blobs. Coordinate a short maintenance barrier or a reference snapshot with garbage-collection suspension so the copied database and blobs agree. Do not copy a live SQLite main file alone. Restore to an isolated instance and validate account access, file manifests, standard downloads, and private downloads with a recovery key. A cloned independent server receives a new identity and an explicit migration workflow; a replacement restoring the same server retains identity and must not run alongside the original.

Use schema versions and pre-upgrade backups. Refuse unsupported newer schemas. Downgrades require a documented compatible backup restore, not blindly launching an older image against a migrated database.

## Transport performance and statistics

Implement HTTPS/HTTP/2 first and add HTTP/3 through `quic-go` behind the same typed client and API. The library provides an HTTP/3 transport; the application explicitly owns fallback and replay policy. Disable 0-RTT for authentication and mutations. [quic-go HTTP/3 client](https://quic-go.net/docs/http3/client/)

Probe QUIC with bounded timeouts, cache failures briefly, and fall back to verified HTTPS when UDP is unavailable. Preserve transfer identity across transport changes. Compare QUIC and HTTP/2 under LAN, direct tailnet, and relayed-tailnet conditions; adopt defaults based on measured useful throughput, CPU, memory, and fairness. QUIC is not an unconditional throughput guarantee.

Measure scanning, disk reads/writes, hashing, encryption, network transfer, and finalization independently. Start without compression or deduplication. Tune chunk size and worker counts with large-file and small-file workloads. Apply global and per-user limits so one batch cannot exhaust memory, disk handles, or bandwidth. Prioritize small-file throughput: benchmark bounded metadata batching and request reuse before increasing workers. If object packing is needed, preserve individual file retrieval, independent ownership and private-file boundaries. Include buffers, metadata, cryptographic KDFs, caches and frontend state in peak-memory measurements.

Admin statistics distinguish committed stored bytes, retained-version/trash bytes, staged bytes, reserved capacity, and filesystem free space. Per-user totals represent physical application bytes attributed to that user, including encryption overhead. Standard logical bytes can be reported separately; private original sizes need not be exposed. Total application storage need not equal total filesystem usage.

Publish upload/download rates as rolling bytes-per-second measurements, separate retries/wire work from useful committed progress, and show stale/offline status. Deliver events with bounded buffering and reconnection; slow dashboards must not stall transfers. Audit logs and metrics must not expose private names, keys, or credentials.

## Verification and release gates

Every task chunk includes tests and an observable acceptance result. Unit tests cover deterministic rules; integration tests use real SQLite and temporary filesystems; protocol tests exercise real TLS and both transports; UI tests exercise onboarding, queue controls, and error states. Use fake clocks for expiry/backoff and injectable filesystem/network faults for reproducible recovery. Test races with Go's race detector where supported and fuzz parsers, manifests, path validation, and authorization boundaries.

Cross-component acceptance fixtures include two servers, two users on each, multiple devices for one user, colliding identifiers, and a server replacement attempt. Standard and private modes run through the same interrupted-upload/download scenarios. Assert that private plaintext canaries and keys never appear in server storage, logs, telemetry, or metadata responses.

Release-scale qualification includes a real non-sparse file above 100 GiB, at least 100,000 small files, concurrent clients, a full staging/data filesystem, container restarts, desktop restarts, blocked UDP, corrupted objects, and a clean-device restore. These are qualification targets, not advertised hard limits. Run reduced equivalents in normal CI and record long-run hardware, dataset, throughput, memory, and failures. Set memory and performance budgets from measured hardware baselines before optimization is accepted.

Completion requires passing component tests, shared contract tests, restore drills, and deployment checks; no chunk is complete merely because its happy path works. Test cases must describe supported and intentionally rejected scenarios rather than mirror private implementation details.
