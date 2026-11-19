# Changelog

## Unreleased

- Desktop layout: a **Settings** page holds the bandwidth limit, "Show all signed-in servers", launch at login and **Quit ServerSend**. **Transfers** has a prominent **Pause/Resume all transfers** button and separate **Uploads** and **Downloads** cards; each upload or download expands into one-line file rows, five visible before scrolling. **Check for updates** moved to the window header and tray menu. Linux and Windows no longer show an in-window "ServerSend" menu bar; macOS keeps it in the system menu bar.

- Private file limit raised from 1 GiB to 16 GiB. Larger files are skipped with a warning on their selection, where you choose **Upload unencrypted** (confirmed; stored as standard files in the same folder) or **Keep skipped**, per selection or per file. Previously they failed with the generic "needs attention; unlock and retry" message. Skip and private staging-capacity reasons are now shown for private items; other private errors stay masked. The desktop checks free disk space (keeping 1 GiB spare) and a 32 GiB staging budget before encrypting, and removes incomplete stages.

- Desktop background operation: closing the window keeps transfers running in a tray (Open, Pause all, Launch at login, Quit). Quit stops at checkpoints before exiting; launch at login starts hidden; transfers recover after sleep; relaunching shows the window. Removed the scaffold greeting service and demo component.

- Supported upgrades and restores: a schema migration first writes a full backup to `SERVER_SEND_UPGRADE_BACKUP_DIR` (`/backups`) and refuses to migrate without it (`none` opts out). New `-restore DIR -restore-as replace|clone` restores into empty volumes; clones get a new identity and TLS CA, revoke sessions and refuse private data. Backups record schema version and server ID. Adds a versioned Unraid template (`apps/server/deploy/unraid/serversend.xml`), a `/backups` mount in Compose, the `0.4` channel default, and a release guard against overwriting exact image versions. **Existing deployments must add the backup setting before the next schema upgrade.**

- Hardened chunk uploads: an acknowledged chunk can no longer be rewritten by a concurrent request for the same index, and a final chunk fixes the upload's chunk size before writing, so chunks cannot overlap.

- Files changed or damaged on the server's data volume after upload stay downloadable: the server starts and logs them instead of refusing to start, and the desktop restores the server's current bytes and marks the restore with a mismatch warning. Missing files still stop startup. Private files that fail verification still fail.

- Reworked the desktop layout: saved servers open straight into the library; a top navigation separates Library (one card per server with an in-place folder browser, upload, new folder, trash and versions), Transfers (rates, throughput, bandwidth and queues, with a running indicator in the navigation), Servers (profiles, storage mode, vault policy and usage) and Devices. Sign-in runs in a dialog. Each server asks once whether to use standard or private vault storage.

- Restore small-file batching for durable uploads: concurrently prepared standard files share bounded requests while retaining per-file checkpoints and crash recovery. Eight-worker local disk samples measured about 10× faster than individual requests; deployment throughput remains unqualified.

- Added simultaneous desktop server sessions, isolated catalogs, combined/scoped queue controls, editable profile names/approved routes, bounded verified route failover and explicit trust repair. Profile removal retains local queue/data; identity proofs reject reused nonces.

- Add experimental private vaults, encrypted names/content and durable tasks, recovery exports, rewrap/lock controls and private backup recovery. Current private ciphertext staging limits each file to 1 GiB; see `docs/private-storage.md`.

- Added desktop version history, downloads and revision-checked restore/conflict resolution, stable overwrite IDs, recoverable file/folder trash with explicit purge, and administrator retention controls (30-day trash; versions kept indefinitely by default). Schema 12 preserves existing versions and exposes earlier hidden displacements in trash. Previously permanently deleted content cannot be recovered. See [lifecycle semantics](docs/protocol-transfers.md#standard-versions-trash-and-retention).

- Added standard account/server storage allocation and reservation dashboards, deduplicated filesystem capacity, useful upload/payload/retry rates and bounded replayable events with stale/offline indicators. See [visibility semantics](docs/protocol-transfers.md#implemented-standard-visibility).

- Added storage quotas and free-space reservations, expired-upload cleanup and account-scoped cancellation. Startup reconciles orphan objects and rejects missing/truncated acknowledged data. Strengthened directory flush ordering; schema 8 adds recovery indexes. See [storage settings](docs/server-development.md#storage-capacity-and-recovery).

### Usability iteration

- Stable desktop installation registration, account-scoped keychain credentials and self-service device revocation/sign-out controls. Local logout disables restoration before keychain deletion, including unavailable stores.
- Private CA import, normal TLS validation and channel-bound signed server-identity verification on each HTTP/3 or HTTP/2 connection; leaf renewal preserves trust. Certificate-only profiles require explicit trust repair and a fresh login.
- Session-bound browser CSRF protection alongside secure cookies and exact-origin checks. Schema 10 invalidates legacy bearer-only sessions/challenges once and retains device history.

- Rotating desktop refresh credentials with ten-minute access tokens, 30-day refresh inactivity expiry and second-factor-only verification every 60 days. Pending keychain rotations survive interrupted requests and failed successor saves; conflicting reuse revokes the affected device.
- Admin per-user device history/revocation and separate password/TOTP reset controls, with fresh administrator authentication, confirmations, auditing, account-wide reset revocation and a transactional lock against mixed recovery.

- Saved desktop servers and certificate pins, server switching, and optional sign-in persistence through the OS credential store. Passwords are not saved.
- Seven-day browser admin cookies; separate Dashboard, Accounts, Logs and Settings views. Dashboard adds Linux host CPU/memory, stored bytes and available storage; accounts show committed file bytes per user.

- Persistent application logs in the admin panel, with search, level filters, coloured Info/OK/Warning/Error indicators and older-entry navigation.
- Server storage is now a browsable tree: each user's files appear at `data/<username>/<folders>/<name>`, with older versions, unfinished uploads and in-progress changes under `data/.serversend`. Uploading a same-named file replaces it and keeps the old version; re-uploading a folder merges into it. Pre-release data is not migrated: run `serversend -reset-files` (keeps accounts). Usernames can no longer start with a dot.
- Batched small-file uploads: queued files up to 1 MiB are sent up to 256 per request and published in one server transaction, about 75 times faster per file on a flushing disk. Servers advertise `batch_upload`; older servers use individual uploads.
- Uploads can be cancelled per file or per selection; scanning stops, queued files are skipped and interrupted uploads release their server reservations immediately.
- Deleting a file or folder now cancels unfinished uploads into it instead of refusing until they expire.
- Desktop uploads run on eight workers and the server no longer holds its upload lock while receiving and flushing chunk bytes. Upload selections show elapsed time, bytes and average speed; speeds under 1 KiB/s no longer overflow the throughput tiles.
- Fixed the desktop window freezing and uploads crawling for folders with many small files: transfer events are coalesced (≤5/s) off the transfer worker, folder scans run in the background, the queue renders at most 200 rows and file listings refresh at most every two seconds.
- Fixed expired open uploads blocking recursive folder deletion indefinitely; active uploads remain protected.

- Persistent admin user list with encrypted pending passwords until replacement or one-hour expiry; refreshes every five seconds. Passwords issued before this upgrade cannot be recovered from their hashes.
- Clickable desktop folder names and breadcrumb navigation; one Upload menu for native file and folder pickers.
- Confirmed permanent file and recursive folder deletion, scoped to the signed-in user. Active uploads block deletion; stored-object cleanup survives restarts.
- Bounded, independently scrollable transfer queue and file list.
- Vue-native workspace layouts, compact navigation, violet accents, and light/dark themes inspired by the Artifact template; no new dependencies.

### Build and release tooling

- Kept the shared QUIC transport alive until active HTTP/3 responses finish draining during shutdown.
- Fixed intermittent release-test failures when integration onboarding exhausts the real authentication limiter; test-only bounded retries replace fixed sleeps without changing production limits.

- Unified server/desktop releases with `make release`, automatic patch selection, combined checks, GHCR publishing and native desktop downloads with checksums. Historical component tags are preserved.
- Startup desktop update checks with an explicit Update and restart / Later popup, with required SHA-256 verification and embedded release versions. Linux downloads now use AppImage packaging (host GTK4/WebKitGTK required).
- Portable desktop formatter invocation on Windows.
- Removed the unsupported npm minimum-release-age setting (npm did not enforce it).

### Server and desktop initial feature baseline

First planned version: **0.1.0** (experimental network-testing release).

- Browser administrator onboarding and user creation, separated from desktop file transfers.
- Recursive folder uploads, server folders, verified downloads and transfer speed chart.
- Persistent Docker configuration, accounts, certificates and storage; TCP port 58443.
- Browser-compatible ECDSA TLS certificates and explicit migration for older development certificates.
- Versioned GHCR server releases, OCI version/commit labels and Unraid update channels.

Known limits: sequential transfers, no resume or persistent desktop queue, at most 1000 catalog entries per directory, and unsigned desktop downloads (no installers or independent release signatures).
