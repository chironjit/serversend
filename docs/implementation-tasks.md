# ServerSend implementation tasks

Implement the architecture in [Implementation plan](implementation-plan.md) in small reviewable changes. Dependencies indicate work that must be complete before a chunk can be accepted; interface exploration can happen earlier. Split an oversized chunk into smaller changes while retaining its acceptance tests.

## Current progress

| Chunk | Status | Implemented slice and remaining work |
| --- | --- | --- |
| 01 | In progress | Component check scripts and root Make targets are in place. Hosted CI, frontend behavioural test tooling and Wails binding regeneration remain. See [desktop development](desktop-development.md). |
| 02 | In progress | Version negotiation, capability/limit validation and typed API errors have initial code and tests. Identity, lifecycle, pagination and upload contracts remain. See [protocol foundation](protocol-foundation.md). |
| 03 | In progress | Shared SQLite opening policy and transactional migrations are implemented with real-database tests. Product schemas, repositories, backups and platform qualification remain. See [SQLite foundation](sqlite-foundation.md). |
| 04 | In progress | Environment configuration parsing and validation have initial code and tests. Persistence, server identity, listeners, health endpoints and container image remain. See [server development](server-development.md). |
| 06 | In progress | TOTP secret/QR generation and deterministic code verification helpers are implemented. Enrollment state, encrypted storage, atomic replay consumption, rate limits and recovery codes remain. See [TOTP foundation](twofactor-foundation.md). |
| All others | Not started | Follow the dependencies and acceptance criteria below. |

These independent foundation slices do not complete their chunks or bypass the integration dependencies. SQLite and OTP dependencies were added with the user's explicit approval, recorded in root `AGENTS.md`.

## Completion rules for every chunk

- [ ] New dependencies follow `AGENTS.md`: prefer official/upstream modules, reuse recorded approvals, and obtain approval for unselected modules before adding them.
- [ ] Every UI scenario supports light and dark modes with a persisted Light/Dark/System setting.
- [ ] Code is in `apps/desktop`, `apps/server`, or `shared` according to ownership; project documentation is in root `docs`. Reserve `shared` for code and its tests.
- [ ] Files have focused responsibilities and intuitive names; generated files are regenerated.
- [ ] Normal, invalid, boundary, permission, and relevant failure/recovery scenarios have tests.
- [ ] Tests use deterministic clocks/fixtures where possible; integration tests cover real persistence boundaries.
- [ ] Formatting, static checks, type checks, and relevant tests pass; results are recorded in the change description.
- [ ] No credentials, private metadata, or usable vault keys appear in logs or test artifacts.
- [ ] User-visible errors explain the next useful action without exposing internal secrets.
- [ ] Migrations, compatibility implications, and operational changes are documented.

## Milestones and order

| Milestone | Chunks | Demonstration |
| --- | --- | --- |
| Foundation | 01–04 | Validated workspace, tested contracts, running persistent container |
| Identity | 05–09 | Onboarding and independently authenticated server/device profiles |
| Standard storage | 10–16 | Background backup and verified restore across reconnects |
| Private storage | 17–20 | Encrypted uploads and clean-device recovery |
| Administration | 21–22 | Accurate statistics, live activity, device/user management |
| Performance and operations | 23–26 | QUIC fallback, Unraid deployment, upgrade and restore drills |
| Release | 27–28 | Packaged clients and completed qualification matrix |

Chunk 17's format prototype should run after chunks 02–03 and before chunk 11 freezes the production manifest. Its full integration follows the standard transfer implementation. The task numbers organize delivery; the explicit dependency list governs execution.

## Chunk 01 Validate the workspace and establish checks

**Location:** `apps/desktop`, `apps/server`, `shared`; minimal root workspace/CI entries. **Dependencies:** None.

- [ ] Retain `apps/desktop`, `apps/server`, root `shared`, and root `docs`; verify the existing workspace layout and scaffold assets.
- [ ] Verify existing module names, imports, `go.work`, frontend bindings, Taskfiles and packaging paths; correct inconsistencies without renaming or relocating the application modules.
- [ ] Remove or relocate scaffold examples only after identifying their purpose.
- [ ] Add component-owned formatting, static-analysis, type-check, unit-test and integration-test commands; wire CI to them.
- [ ] Add frontend test tooling and establish the Go test harness without placeholder behaviour tests.

**Tests and acceptance:** Build all Go modules and desktop frontend; regenerate bindings; run scaffold smoke checks; verify workspace, build and packaging references resolve to the existing `apps/desktop` and `apps/server` paths. Demonstrate a fresh checkout following the documented commands.

## Chunk 02 Define contracts and identity boundaries

**Location:** `shared/api`, `shared/protocol`, `docs`. **Dependencies:** 01.

- [ ] Specify version negotiation, capability limits, typed errors, identifiers and pagination.
- [ ] Specify account, upload, queue and catalog revision state transitions.
- [ ] Specify server identity authentication, endpoint pairing, certificate renewal and replacement behaviour.
- [ ] Define upload creation, missing-chunk queries, chunk PUT/GET, finalize, catalog and event contracts.
- [ ] Define standard/private manifest envelopes, leaving cryptographic internals gated by 17.
- [ ] Add a requirement-to-test mapping covering every section of the implementation plan.

**Tests and acceptance:** Serialization and compatibility fixtures; malformed/oversized inputs; unsupported versions; invalid state transitions; colliding IDs across servers; stable error decoding. Document examples sufficient to implement client and server independently.

## Chunk 03 Establish persistence and fault injection

**Location:** `apps/server/internal/database`, `apps/desktop/internal/database`, `shared/testutil`. **Dependencies:** 01–02.

- [x] Obtain approval and pin `github.com/mattn/go-sqlite3` for shared persistence.
- [ ] Define application-owned initial schemas and repositories using the shared migration helper.
- [ ] Add foreign-key enforcement, transaction helpers, busy-timeout policy and controlled writes.
- [ ] Add deterministic clocks, random-source test adapters, network interruption and filesystem-failure fixtures.
- [ ] Add upgrade compatibility guards and repository interfaces only where needed.

**Tests and acceptance:** Fresh database, repeated startup, migration from each supported prior fixture, migration failure, newer-schema rejection, concurrent writes, transaction rollback, missing/read-only paths and restart persistence. Cross-platform driver builds pass.

## Chunk 04 Build the server process and container skeleton

**Location:** `apps/server/cmd`, `apps/server/internal/config`, `apps/server/internal/identity`, `apps/server/deploy`. **Dependencies:** 02–03.

- [ ] Implement validated configuration, persistent server ID/key and graceful process lifecycle.
- [ ] Add health/readiness endpoints, TLS configuration and API capability endpoint.
- [ ] Build a non-root multi-stage image with explicit `/config`, `/data`, `/staging` mounts and TCP/UDP port declarations.
- [ ] Embed an initial admin shell without application credentials or open enrollment.

**Tests and acceptance:** Container restart retains identity; invalid certificates/configuration fail clearly; SIGTERM drains requests; permissions and missing mounts fail predictably; no persistent application data lands only in the container layer.

## Chunk 05 Bootstrap administrators and manage temporary passwords

**Location:** `apps/server/internal/auth`, `apps/server/internal/users`, `apps/server/internal/httpapi`. **Dependencies:** 03–04.

- [ ] Add expiring one-time bootstrap and admin/user roles.
- [ ] Implement user creation, random temporary passwords, Argon2id hashing and onboarding scopes.
- [ ] Implement password replacement, throttling and audited administrator resets.
- [ ] Define account disablement and session invalidation hooks.

**Tests and acceptance:** Bootstrap reuse/expiry, duplicate accounts, weak/malformed input, wrong/expired temporary password, concurrent replacement, direct API onboarding bypass, cross-user access and non-admin administration attempts. Hashes and secrets are absent from API/log outputs.

## Chunk 06 Implement mandatory TOTP and recovery codes

**Location:** `apps/server/internal/auth`, `shared/api`. **Dependencies:** 05.

- [x] Resolve the `rsc.io/2fa` CLI mismatch: the user approved `github.com/pquerna/otp`.
- [x] Implement and test TOTP enrollment material and deterministic verification helpers; persist replay consumption in the account flow before granting access.
- [ ] Add provisional enrollment, QR provisioning, code verification and encrypted secret storage.
- [ ] Add single-use hashed recovery codes and safe replacement.
- [ ] Enforce password-change plus TOTP completion before full access.
- [ ] Add distinct lost-TOTP recovery and password-reset flows with admin reauthentication.

**Tests and acceptance:** Correct/wrong codes, skew boundaries, replayed time steps, enrollment interruption, racing recovery-code use, expired onboarding session, rate limits, password reset preserving TOTP and TOTP reset forcing reenrollment. Admin onboarding has no exemption.

## Chunk 07 Implement device sessions and revocation

**Location:** `apps/server/internal/auth`, `apps/server/internal/users`, `shared/client`. **Dependencies:** 06.

- [ ] Register devices per account; issue short-lived access and rotating refresh credentials with silent renewal and persistent device authorization.
- [ ] Avoid routine inactivity/absolute expiry for desktop device authorization; retain explicit logout, revocation, account reset and compromise handling.
- [ ] Hash stored refresh tokens; specify replay detection and refresh-race recovery.
- [ ] Add self-service device listing/revocation and administrator account/session controls.
- [ ] Propagate session revocation to active streams and audit events.

**Tests and acceptance:** App/OS restart, long inactivity, offline recovery, access-token expiry and container upgrades cause no routine login prompt; revoked/lost credentials require login. Two devices remain independently signed in; revoking one leaves the other usable; refresh replay, concurrent refresh, interrupted refresh response, expired tokens, account disablement, password reset and revoked active requests behave according to contract.

## Chunk 08 Implement desktop profiles and secure credentials

**Location:** `apps/desktop/internal/profiles`, `apps/desktop/internal/identity`, `apps/desktop/frontend/src/features/servers`, `shared/client`. **Dependencies:** 02–04, 07.

- [ ] Add/edit/remove profiles with multiple verified endpoints and independent accounts.
- [ ] Implement server pairing, approved endpoint selection and identity mismatch repair.
- [ ] Add OS credential-store adapters and clear unavailable/locked-store behaviour.
- [ ] Keep tokens and client state scoped by server/account; serialize refresh.

**Tests and acceptance:** Two servers with identical usernames and remote IDs remain isolated; LAN/tailnet routes resolve to one profile; wrong server identity, untrusted certificates, cross-origin redirects and endpoint replacement never receive credentials. Profile restart and credential-store failures are handled.

## Chunk 09 Build desktop onboarding and account screens

**Location:** `apps/desktop/frontend/src/features/onboarding`, `apps/desktop/internal/app`. **Dependencies:** 06–08.

- [ ] Establish Tailwind CSS, accessible shared styling and persisted Light/Dark/System selection for desktop screens.
- [ ] Build temporary-password login, password replacement, TOTP enrollment, recovery-code display and regular login; keep optional vault setup outside mandatory onboarding.
- [ ] Add device list, logout, account state, and authentication-required queue signals.
- [ ] Add accessible loading/errors and retry paths; keep cryptographic secrets out of frontend logging.

**Tests and acceptance:** End-to-end onboarding, cancelled enrollment/restart, expired credentials, invalid codes, recovery-code login, revoked device and multiple profiles. User cannot enter file screens with an onboarding-only session. Test all onboarding states in both themes, persisted/system theme changes, and standard file access without a vault.

## Chunk 10 Implement file catalogs and version conflicts

**Location:** `apps/server/internal/catalog`, `shared/api`, `shared/protocol`. **Dependencies:** 03, 07.

- [ ] Add files, directories, immutable versions, storage mode and revision preconditions.
- [ ] Implement paginated browsing, standard metadata queries and catalog revision feed.
- [ ] Define opaque private catalog entries and conflict behaviour without plaintext name indexes.
- [ ] Add authorization to every lookup, mutation and version reference.

**Tests and acceptance:** Nested/empty directories, pagination during concurrent changes, duplicate names, revision conflict, two-device updates, inaccessible foreign IDs and private metadata opacity. Concurrent uploads cannot silently replace each other.

## Chunk 11 Implement durable chunk storage

**Location:** `apps/server/internal/storage`, `apps/server/internal/uploads`, `shared/protocol`. **Dependencies:** 03–04, 10, 17 format decision.

- [ ] Implement immutable object paths, chunk validation and durable acknowledgements.
- [ ] Implement reservations, body-size limits and incomplete-upload metadata.
- [ ] Support destination-side temporary copies when staging and data are separate filesystems.
- [ ] Implement startup reconciliation for pending writes and orphaned objects.

**Tests and acceptance:** Partial writes, disk full, read-only disk, short reads, checksum mismatch, identical/conflicting retry, path traversal, symlink escape, missing objects, duplicate chunk races and crash injection around data flush/metadata update. Repeated recovery converges without data loss.

## Chunk 12 Implement upload lifecycle and finalization API

**Location:** `apps/server/internal/uploads`, `apps/server/internal/httpapi`, `shared/client`. **Dependencies:** 07, 10–11.

- [ ] Implement idempotent creation, chunk upload, missing-chunk reconciliation, cancellation and finalization.
- [ ] Validate manifests and expected revision; publish only complete durable versions.
- [ ] Enforce per-account ownership, capacity reservations, expiry and bounded concurrency.
- [ ] Define cancellation/finalization races and client-visible progress states.

**Tests and acceptance:** Empty file, out-of-order/missing/duplicate chunks, wrong length/hash, oversized manifest, uncertain finalization response, repeated finalize, expired upload, wrong owner and concurrent cancel/finalize. A successful acknowledgement survives container restart.

## Chunk 13 Implement persistent desktop scanning and queue

**Location:** `apps/desktop/internal/queue`, `apps/desktop/internal/transfer`, `apps/desktop/internal/database`. **Dependencies:** 03, 08, 10.

- [ ] Persist jobs and incrementally scan file/folder selections.
- [ ] Add queue transitions, fair scheduling, bounded workers and retry policy.
- [ ] Track source fingerprints, skipped items, partial batch success and per-server state.
- [ ] Add pause/cancel and waiting states for network, authentication and vault unlock.

**Tests and acceptance:** Large directory scan with bounded buffers, empty directories, symlink loops, unreadable/deleted files, Unicode/case collisions, restart mid-scan, queue-state races, cancelled jobs and one offline server alongside an active server. No whole-batch memory dependency.

## Chunk 14 Connect standard uploads to the queue

**Location:** `apps/desktop/internal/transfer`, `shared/client`. **Dependencies:** 12–13.

- [ ] Stream reads and checksums into bounded upload buffers; persist acknowledgements.
- [ ] Reconcile server progress after restart or lost acknowledgements.
- [ ] Detect source mutations and restart with a fresh upload generation.
- [ ] Add bandwidth limits and typed retry/authentication/capacity handling.

**Tests and acceptance:** Disconnect and process kill at each boundary, source replacement during transfer, source deletion before resume, server restart, token expiry, quota/disk exhaustion, retry backoff and duplicate-ack handling. Restored standard contents match the original stable source.

## Chunk 15 Implement browsing and resumable downloads

**Location:** `apps/desktop/internal/catalog`, `apps/desktop/internal/transfer`, `apps/desktop/frontend/src/features/files`, server read handlers. **Dependencies:** 10–12, 14.

- [ ] Build profile-specific browsing, version selection and file/folder download selection.
- [ ] Stream selected chunks into temporary files with persisted download progress.
- [ ] Add verification and safe destination publication, collision policy and path validation.
- [ ] Refresh catalogs by revisions and recover from expired event history.

**Tests and acceptance:** Interrupted downloads, corrupt chunks, missing retained version, local disk full, Windows reserved names, case collisions, traversal, symlink destinations, Unicode names and existing local file conflicts. Two-device browsing sees committed versions only.

## Chunk 16 Build background operation and transfer UI

**Location:** `apps/desktop/internal/app`, `apps/desktop/frontend/src/features/transfers`, desktop packaging. **Dependencies:** 09, 14–15.

- [ ] Add combined and per-server queues with progress, speed, failures and pause/resume/cancel/retry.
- [ ] Implement tray lifecycle, explicit quit, optional launch at login and sleep/wake recovery.
- [ ] Throttle UI events and virtualize large lists.

**Tests and acceptance:** Window close preserves transfers; explicit quit checkpoints them; app restart and sleep/wake resume; slow frontend cannot exhaust buffers; partial success is visible. Exercise native lifecycle on each supported desktop OS.

## Chunk 17 Specify and prototype the private file format

**Location:** `shared/fileformat`, `docs`. **Dependencies:** 02–03. **Timing:** Before freezing chunk 11's manifest/storage contract.

- [ ] Evaluate maintained crypto APIs for independently resumable encrypted objects and authenticated manifests.
- [ ] Specify serialization, encryption key hierarchy, nonce/randomness rules, associated data, encrypted names/paths and version compatibility.
- [ ] Define bounded ciphertext staging, retransmission, fresh-attempt rules and recovery after lost state.
- [ ] Add test vectors and document privacy/freshness limitations; review the format before production use.

**Tests and acceptance:** Empty and boundary-sized objects, wrong key, wrong associated data, modified/truncated/swapped/reordered/duplicated chunks, forged manifest lengths, unsupported format and interrupted encryption. Demonstrate retry/restart without unsafe key/nonce reuse and bounded disk/memory use. This chunk does not depend on private UI.

## Chunk 18 Implement vault creation and device recovery

**Location:** `apps/desktop/internal/vault`, server vault-envelope endpoints, `shared/fileformat`. **Dependencies:** 07–09, 17.

- [ ] Make vault creation optional and independent of account password/2FA and persistent device sign-in.
- [ ] Generate account-specific vault keys, passphrase/recovery wrappers and versioned KDF parameters.
- [ ] Store only authenticated encrypted envelopes server-side; scope them to server/account.
- [ ] Implement vault creation races, local unlock, credential-store persistence and explicit recovery export.
- [ ] Add passphrase rewrap with revision checks and interrupted-update recovery.

**Tests and acceptance:** Clean second-device unlock, wrong passphrase/recovery key, damaged envelope, hostile KDF parameters, two-device creation/rewrap races, unavailable keychain, account password reset and recovery after local key deletion. Server never obtains a usable vault key.

## Chunk 19 Integrate private uploads and downloads

**Location:** `apps/desktop/internal/vault`, `apps/desktop/internal/transfer`, `shared/fileformat`, server opaque object validation. **Dependencies:** 14–15, 18.

- [ ] Add batch encryption choice and per-profile default without modifying existing versions.
- [ ] Encrypt contents and metadata before transmission and decrypt authenticated downloads locally.
- [ ] Persist safe ciphertext retry state and wrapped per-version keys.
- [ ] Support authentication/unlock waits and interrupted private transfers.

**Tests and acceptance:** Standard/private mixed queue, large private file, restart mid-encryption/upload/download, changed source, lost acknowledgement, wrong vault, corrupt ciphertext and second-device restore. Scan server disk/database/logs for plaintext canaries and key material.

## Chunk 20 Build private browsing and vault UX

**Location:** `apps/desktop/internal/catalog`, `apps/desktop/frontend/src/features/vault`, `apps/desktop/frontend/src/features/files`. **Dependencies:** 15, 18–19.

- [ ] Add encrypted local metadata index, incremental refresh and client-assisted private-name conflicts.
- [ ] Add vault setup/unlock/recovery/passphrase-change screens and auto-lock controls.
- [ ] Explain secure recovery-key storage and unrecoverable loss of all unlock methods; require acknowledgement before the first private upload.
- [ ] Offer separate remembered vault unlock on trusted devices; keep account sign-in usable when the vault is locked.
- [ ] Explain recovery requirements and revocation limits at the relevant actions.
- [ ] Clear decrypted views on lock and define active-job unlock retention.

**Tests and acceptance:** Private search across devices, locked vault, stale catalog refresh, equal path uploads from two devices, cancelled recovery setup, passphrase change, auto-lock during idle/transfer and loss of all local state with recovery package available. Persistent indexes contain no plaintext names. Test skipping/cancelling vault setup, standard transfers while locked, recovery acknowledgement, and remembered device sign-in with and without remembered vault unlock.

## Chunk 21 Implement accounting and live transfer observations

**Location:** `apps/server/internal/metrics`, `apps/server/internal/audit`, event endpoints. **Dependencies:** 12, 19.

- [ ] Account for committed, retained, staged and reserved bytes with reconciliation.
- [ ] Measure per-transfer/user/server upload/download rates and mark stale observations.
- [ ] Add bounded reconnectable events and redact private data.
- [ ] Report free space by underlying filesystem without double-counting mounts.

**Tests and acceptance:** Concurrent transfer totals, retries without double-counting, cancelled/expired uploads, private overhead, multiple mount layouts, restart reconciliation, slow/disconnected event consumers and denied foreign-user subscriptions. Usage matches fixture storage.

## Chunk 22 Complete the admin interface

**Location:** `apps/server/admin`, `apps/server/internal/httpapi`. **Dependencies:** 05–07, 21.

- [ ] Use Tailwind CSS with persisted Light/Dark/System selection; test every admin state in both themes.
- [ ] Add administrator login/2FA, user creation, temporary-password display, password reset and distinct TOTP recovery.
- [ ] Add account disablement, device revocation, storage tables and live transfers.
- [ ] Add audit history, retention configuration and meaningful empty/error states.
- [ ] Implement cookie security, CSRF/origin checks, role enforcement and reauthentication for sensitive operations.

**Tests and acceptance:** Full admin flows, non-admin access, forged cross-site mutation, expired session, reset/disable during active transfer, live statistics reconnect and private-name redaction. Browser UI and direct API enforce the same permissions.

## Chunk 23 Implement versions retention and safe cleanup

**Location:** `apps/server/internal/catalog`, `apps/server/internal/storage`, `apps/desktop/frontend/src/features/files`. **Dependencies:** 15, 19, 21–22.

- [ ] Add trash, restore, explicit purge and configurable version/trash retention.
- [ ] Expire abandoned uploads and release reservations.
- [ ] Implement reference-aware garbage collection coordinated with readers and finalization.
- [ ] Add cleanup reporting and audit records.

**Tests and acceptance:** Trash restore, expiry boundary, concurrent download/purge, finalize/cleanup races, active versus abandoned uploads, crash during purge and repeat cleanup. Referenced data is never removed, and accounting converges.

## Chunk 24 Add QUIC and benchmark transfer policies

**Location:** `apps/server/internal/httpapi`, `shared/client`, `apps/desktop/internal/profiles`, benchmark fixtures. **Dependencies:** 14–16, 19, 21.

- [ ] Add HTTP/3 listener/client with explicit HTTP/2 fallback, bounded probing and failure caching.
- [ ] Keep TLS/identity validation identical and disable mutation/authentication 0-RTT.
- [ ] Benchmark chunk sizes, concurrency, fairness, CPU, peak memory and useful throughput in both storage modes, prioritizing many small files and retaining large-file cases.
- [ ] Measure request/metadata overhead, bounded batching and buffer reuse before increasing concurrency; enforce measured client/server memory budgets.
- [ ] Record LAN, direct tailnet and relayed-tailnet conditions; select measured defaults and configurable limits.

**Tests and acceptance:** UDP blocked, QUIC timeout, route change, TCP fallback, certificate failure, stream reset, server restart, large/small-file workloads and concurrent users. Fallback preserves upload identity and never duplicates committed versions.

## Chunk 25 Finish Unraid deployment and network setup

**Location:** `apps/server/deploy`, `apps/server/docs`, container integration tests. **Dependencies:** 04, 22–24.

- [ ] Produce Unraid template, documented bind mounts, UID/GID settings and TCP/UDP port mappings.
- [ ] Use the existing Unraid-host Tailscale installation; validate container TCP/UDP reachability without exposing the admin UI publicly.
- [ ] Use a conventional minimal Docker runtime image compatible with the selected SQLite driver; Talos is a Kubernetes host OS and is not the base image for this deployment.
- [ ] Add trusted certificates, renewal procedures, startup storage validation and resource guidance.
- [ ] Validate pool/array paths and separate staging/data placement on an actual Unraid instance.

**Tests and acceptance:** Install/restart/update with persistence; permissions; expired/renewed certificates; LAN and tailnet connectivity; blocked UDP fallback; staging/data on different filesystems; low-capacity behaviour. Record manual hardware checks separately from automated container tests.

## Chunk 26 Implement backup restore and upgrade procedures

**Location:** `apps/server/internal/storage`, maintenance commands, `apps/server/docs`. **Dependencies:** 23, 25.

- [ ] Implement consistent metadata backup with reference snapshot/maintenance coordination and garbage-collection suspension.
- [ ] Include server identity, configuration keys, encrypted vault envelopes and referenced objects.
- [ ] Add restore validation and explicit replacement-versus-clone identity rules.
- [ ] Add pre-migration backup, version checks and documented downgrade recovery.

**Tests and acceptance:** Restore onto clean volumes, interrupted backup, missing object, incompatible schema, failed migration, retained server identity, rejected conflicting live identity and encrypted restore with recovery key. A live SQLite main-file copy alone is never treated as a valid backup.

## Chunk 27 Package desktop applications

**Location:** `apps/desktop/build`, `apps/desktop/docs`, platform CI entry points. **Dependencies:** 16, 20, 24.

- [ ] Build and validate Linux installers/packages first, Windows second, and macOS as a required supported platform; include application icons/metadata.
- [ ] Verify credential-store integration, tray operation, file dialogs and optional startup registration.
- [ ] Document server pairing, recovery, uninstall/data retention and upgrade behaviour.
- [ ] Add signing/notarization configuration where release credentials are available; document unsigned development builds accurately.

**Tests and acceptance:** Clean install, upgrade preserving profiles/queues, uninstall retention policy, non-ASCII filesystem paths, OS login/keychain lock, tray lifecycle and resumed jobs on each supported platform. Missing signing credentials remain an explicit release dependency.

## Chunk 28 Run release qualification

**Location:** `shared/tests`, component regression suites, `docs`. **Dependencies:** All preceding chunks.

- [ ] Execute the requirement-to-test mapping and close uncovered scenarios.
- [ ] Run two-server/two-user/multiple-device isolation and concurrent-update suites.
- [ ] Run 100 GiB-plus real-file and 100,000-small-file datasets with recorded resource budgets.
- [ ] Run process kills, network interruptions, full disks, corrupted storage, token revocation and clean-machine recovery in both storage modes.
- [ ] Run race detection, parser fuzzing, dependency checks and format/security review follow-ups.
- [ ] Record throughput, memory, hardware, limitations, deployment instructions and restore evidence.

**Tests and acceptance:** All mandatory automated checks pass; platform and Unraid checks have recorded outcomes; no unresolved data-loss, authorization-bypass, plaintext-leak or recovery defect remains. Performance claims match reproducible measurements. Implementation is complete only when a clean device restores the selected standard and private files correctly.
