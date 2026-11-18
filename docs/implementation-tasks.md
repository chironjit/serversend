# ServerSend feature implementation tasks

This is the active delivery roadmap, reorganized on 2026-11-17 around features in priority order. It supersedes the [historical chunk checklist](implementation-tasks-archive.md). The [implementation plan](implementation-plan.md) remains the full product specification. Earlier component documents and verification rows may still cite historical chunk numbers; the mapping below preserves their meaning.

## Current baseline

Already implemented: versioned contracts and identity-proof helpers; SQLite foundations and migrations; a TLS server with persistent identity and certificates; administrator bootstrap; Argon2id passwords, mandatory replacement, TOTP and recovery codes; seven-day admin cookies, rotating desktop refresh credentials with 30-day inactivity/60-day second-factor limits, desktop profiles and optional OS-keychain persistence; browser user listing/creation; concurrent standard uploads (eight desktop workers), recursive folders and empty directories; folder browsing/creation; verified whole-file downloads; recoverable trash and explicit purge with journaled cleanup; Tailwind components and theme selection in both interfaces; desktop throughput display; component checks and release workflows.

These are existing building blocks, not completed versions of the features below. The desktop queue now persists standard-mode uploads/restores in SQLite; remembered session credentials use the OS credential store. Upload identities, discovered work, retry schedules and controls survive desktop restart. Catalogs use bounded revision-bound pages, and selected files/folders restore from verified checkpoints. Private mode remains open; standard version history, conflict resolution and recoverable trash are implemented. Hosted CI results, actual Unraid qualification and complete native UI workflows must be verified, not inferred from workflow files. This status review uses the current implementation and previously executed checks recorded in the verification map; it does not represent a fresh test run.

### Delivery status, 2026-11-17

Delivered work includes consistent no-follow source opening and bounded skipped-item batch reports, desktop summaries, HTTP/3-first connections with verified HTTP/2 fallback, [reproducible performance measurements](performance-baseline.md), deployment-directory instance locks, and a [stopped-server backup command](backup-restore.md). Every replacement connection tries QUIC again; a working HTTP/2 fallback connection has no timed upgrade attempts. The local tmpfs results do not establish Unraid/tailnet performance. The [verification map](requirements-verification.md#feature-roadmap-execution-2026-11-17) records executed checks and remaining native/hardware gates.

Checked items mean the stated scope is implemented with recorded evidence. Compound tasks are split below where only part has been delivered. Full feature acceptance still requires its remaining checks; none of the 14 features is fully accepted yet.

| Feature | Status | Delivered scope / remaining work |
| --- | --- | --- |
| 1. Symlink handling | Partial | Skip/report, safe source opening and desktop summaries delivered; native Windows/macOS and picker qualification open |
| 2. Performance and QUIC | Partial | QUIC-first transport complete; deployment resource/performance validation open; later integrations tracked in features 5, 9, 10, 12 and 14 |
| 3. Recoverable storage | Partial | Instance locks, offline backup/restore, startup reconciliation, quota reservations and upload retirement delivered; actual filesystem/power-loss qualification open |
| 4. Persistent sign-in/devices | Partial | Refresh, installation deduplication, verified trust/scoped storage, self-service/admin devices and browser protections delivered; complete native qualification open |
| 5. Resumable uploads | Partial | Durable queue/scans, checkpoint reconciliation, controls and bandwidth implemented; native/deployment qualification open |
| 6. Browsing/restores | Partial | Revision-bound paging, durable file/folder restores and collision/path checks implemented; native/deployment qualification open |
| 7. Storage visibility | Pending | Existing local throughput display only; accounting and dashboards open |
| 8. Versions/trash | Implemented | Standard history/conflict resolution, trash/restore/purge and retention delivered; native filesystem and deployment qualification remain open |
| 9. Private storage | Pending | Contracts only; format prototype, encryption and recovery open |
| 10. Multiple servers/routes | Implemented | Simultaneous scoped clients, combined queues, approved-route failover and explicit trust repair; native LAN/tailnet qualification open |
| 11. Account administration | Pending | Existing user creation/listing and backend foundations only; complete controls and security policy open |
| 12. Deployment/backup/upgrades | Partial | Unraid template, restore command with replace/clone modes, automatic pre-upgrade backups, compatibility record and release-tag guard delivered; actual Unraid/network qualification open |
| 13. Background desktop | Partial | Tray/close-to-tray, graceful quit, launch at login, wake recovery, durable global pause, queue controls and bounded events delivered; native platform lifecycle and keyboard/contrast review open |
| 14. Distribution/qualification | Pending | Existing workflows only; published installers and full platform/scale qualification open |

## How to execute this plan

Work down the user-selected feature order, taking prerequisite work earlier where stated. Where a feature starts before later integrations exist, deliver its current scope first and keep the later acceptance gates explicitly open. Deliver each feature through small reviewable changes covering persistence, server/client behavior, UI and tests as needed; do not create another numbered chunk sequence. Update the baseline and checkboxes only with implementation and acceptance evidence. No remaining feature is currently accepted as complete.

For every feature:

- Follow `AGENTS.md`; obtain explicit approval before adding any unapproved module or dependency. Planning a library is not approval to install it.
- Keep existing repository ownership and regenerate bindings. Do not send file bytes through the frontend bridge.
- Support persisted Light/Dark/System, keyboard interaction, useful loading/error states and bounded frontend state for every affected UI.
- Test normal, invalid, authorization, boundary and relevant interruption/recovery behavior. Use real SQLite/filesystem/TLS integration where the boundary matters.
- Run relevant component checks; record commands, environment, outcomes and outstanding native/hardware gates in the requirement-to-test map.
- Document migrations, compatibility, operational changes and limitations. Preserve account/server scoping and keep credentials, keys and private metadata out of logs.
- Acceptance requires observable end-to-end behavior, not only schemas, contracts or helper tests.

## 1. Graceful cross-platform symlink handling

**Status:** implemented; native Windows/macOS and picker verification remain open.

**Policy:** skip links, directory redirections and unsupported special files, continue uploading ordinary files and empty directories, and report skipped paths/reasons. Do not follow targets or preserve links on restore. Ordinary hard-linked files upload normally at each selected path.

- [x] Apply the same policy to selected files, selected folders and recursive entries, including broken links and cycles.
- [x] Open sources without following links, including when a file or ancestor is replaced after scanning.
- [x] Show bounded skip reports and clear completed-with-skips/all-skipped outcomes; keep permission, cancellation and server failures distinct from skips.
- [x] Pass Linux source/scanner/worker and UI checks. Windows/macOS code cross-compiles; Windows junction/reparse/ACL fixtures are present.
- [ ] Run native Windows/macOS tests and picker workflows, covering links, junctions, cloud placeholders, permissions and replacement races. Cross-compilation does not close this check.

**Why still open:** the remaining work verifies platform behavior; it is not a missing skip/report implementation. Preserve these reports when the persistent queue is added in feature 5.

## 2. HTTP/3-first connections and measured performance

**Status:** transport implementation complete; deployment performance and resource validation remain open.

**Policy:** try HTTP/3 over QUIC on every connection establishment. If QUIC fails, use verified HTTP/2 for that connection. Do not periodically attempt an upgrade; reconnecting starts with QUIC again. Use the approved `quic-go` module.

- [x] Apply this policy to desktop certificate inspection and authenticated requests; serve the same API over QUIC and HTTP/2.
- [x] Bound QUIC probing to 500 ms, retain certificate checks, reject redirects and disable authentication/mutation 0-RTT. Keep HTTP/2 available if the server's UDP listener fails.
- [x] Test connection reuse, fallback and reconnection. Never automatically replay a write whose outcome is uncertain.
- [x] Record local scanning/hash, request-reuse/batching and standard-transfer measurements in the [performance baseline](performance-baseline.md). Retain the measured 4-MiB preferred chunk size and source-sized small-file buffers.
- [ ] Measure useful throughput and peak client/server memory on the intended deployment, then set practical worker/buffer/concurrent-transfer limits. Add batching or more workers only if measurements justify them.

**Later integration:** upload resume belongs to feature 5, private-transfer/KDF measurements to feature 9, and route switching to feature 10. Validate LAN/direct/relayed-tailnet operation in feature 12 and concurrent-user fairness/scale in feature 14. These remain required there; they do not mean the QUIC connection policy is unfinished. Local loopback results do not establish deployment performance.

## 3. Safe and recoverable storage

**Status:** standard-data recovery, capacity reservations and lifecycle cleanup implemented; actual filesystem/power-loss qualification remains open.

- [x] Verify chunk length/digest and flush bytes at their offset before recording the chunk; commit file metadata and journaled tree changes transactionally, replaying the journal at startup. Crash-boundary qualification remains below.
- [x] Lock config/data/staging before initialization so a second process cannot modify the same deployment. Local exclusivity, rollback and crash-release tests pass.
- [x] Provide `serversend -backup DIR` with a stopped-server barrier and consistent SQLite snapshot. The isolated restore test preserves identity/account/catalog and downloads the original standard-file bytes.
- [x] Document pre-upgrade backups and matching-backup/image downgrade restoration in [backup/restore](backup-restore.md); reject unsupported schemas.
- [x] Reconcile interrupted publication at startup; flush shard/root/source directory entries before acknowledgement. Test process exit before metadata recording, metadata rollback, short writes and injected full/read-only/flush errors. Refuse missing acknowledged data; versions changed on the data volume stay downloadable with a warning.
- [ ] Qualify actual full/read-only filesystems and power-loss durability on the intended deployment; injected errors and tmpfs process exits do not establish this.
- [x] Add configurable per-user/global content quotas, transactional reservations and data-volume free-space checks. Concurrent admission cannot exceed declared capacity; external writers/filesystem overhead still require operator headroom. Existing request/chunk/manifest bounds remain in force.
- [x] Retire expired uploads at startup/admission and every minute; expose idempotent account-scoped cancellation, retain key tombstones, bound chunks by reservations and serialize cancellation/finalization/publication. Duplicate/conflicting chunks and cancellation races have regression coverage. Durable desktop controls remain feature 5 work.
- [x] Clean abandoned uploads and deletions through the storage journal, and remove orphaned upload files only at startup under the deployment lock. Repeat maintenance safely with bounded enumeration/deletion batches.

**Scope:** uploads are written under `data/.serversend` and published by rename into the user's tree on the same filesystem. A separate staging-to-data copy path is unnecessary today. If a later feature uses the staging volume for object publication, add destination-side copy/verification/flush/rename and account for temporary overlap then. Private-format work in feature 9 must precede freezing shared/private contracts; it does not block fixing current standard-data safety. Actual Unraid locking/durability and upgrade qualification belong to feature 12.

## 4. Stay signed in and control device access

**Outcome:** users remain signed in across restarts and offline periods, while lost or revoked devices lose access promptly.

**Existing:** mandatory second-factor issuance, ten-minute desktop access, rotating refresh credentials, 30-day inactivity and 60-day TOTP gates, optional OS-keychain persistence, seven-day browser cookies, device history/revocation and active-request cancellation. Stable installation registration, verified CA/identity trust, account-scoped credentials, self-service controls and browser CSRF protection are implemented. Complete native credential-store qualification remains open.

- [x] Issue per-login device authorization, short-lived access tokens and rotating refresh credentials stored hashed on the server.
- [x] Add stable installation identity/deduplication across new full logins; current refresh/TOTP renewal already preserves the device ID.
- [x] Implement bounded current/previous refresh tuples, conflicting-reuse revocation, serialized workers and interrupted rotation recovery, with 30-day refresh inactivity and 60-day second-factor verification. Full password plus TOTP login is required after inactivity expiry. Fake-clock and race tests cover the boundaries and replay handling.
- [x] Integrate the approved native secure-store library on Linux, Windows and macOS without plaintext fallback; cover locked/unavailable/lost stores with fault tests and provide a native qualification command. Linux real-store process roundtrip, deletion/loss and unavailable-service checks pass.
- [ ] Finish native qualification on all three platforms: locked stores and OS restarts, plus Windows/macOS real-store roundtrip/unavailable/loss checks. Record results; compile-only checks do not close this gate.
- [x] Persist credentials under server/account scope and verified trust only. Implement stable identity-key proof/CA validation and renewal safety needed before persisting credentials; feature 10 expands this into multiple profiles/routes.
- [x] Add admin per-user device history (including offline devices), last-seen/recent-activity indication and individual audited revocation with recent admin reauthentication. Heartbeat activity stays separate from refresh/TOTP deadlines.
- [x] Add self-service device listing/revocation and session controls; enforce revocation on every request and cancel active streams within five seconds.
- [x] Replace browser bearer-session handling with Secure/HttpOnly/SameSite cookies, CSRF/origin protections, browser expiry and sensitive-action reauthentication.
- [x] Migrate or explicitly invalidate legacy sessions safely; retain mandatory onboarding and recovery-code enforcement.

**Accept when:** app/OS/server restarts, offline periods and a lost refresh response restore silently within policy limits; exact 30-day inactivity/60-day TOTP boundaries are tested; concurrent refresh and replay tests pass; revocation blocks new work and active streams within the bound; forged browser mutations fail. Record native secure-store results on all three platforms.

## 5. Persistent, resumable uploads

**Outcome:** selected files survive application restarts in a durable queue and resume only missing work.

**Implemented:** standard-mode selections and incremental source discovery are persisted before networking. Eight workers claim tasks fairly across batches, retain immutable upload checkpoints, reconcile missing work after reconnect/restart and schedule typed network/authentication/capacity waits. Pause/resume/cancel/retry and an app-wide persisted bandwidth limit are exposed in the desktop. Completed work and bounded skip reports survive restart. Durable uploads currently use per-file requests; small-file batch API support remains available but is not the durable queue path.

**Depends on:** features 3, 4. Design server/account scoping for feature 10 now.

- [x] Recover brief connection failures in directory creation, upload admission, identical chunk writes, finalization and prepared small-file batches without generating new identities. Immediate request retries exclude trust/authentication/source/capacity failures; the durable queue applies the separate waits/recovery below.
- [x] Persist jobs before network work; stream discovered files/directories into SQLite and resume interrupted scans without unbounded batch buffering.
- [x] Persist upload IDs, source fingerprints, acknowledgements, retry schedules and explicit queue/wait states.
- [x] Reconcile missing chunks after reconnect, lost acknowledgements and restart; never mix source generations when files change, disappear or become unreadable.
- [x] Add bounded workers, fair scheduling, bandwidth controls and typed retry/backoff; one failing job/profile must not block other eligible work. Preserve upload identity and acknowledged progress through transport fallback/reconnect.
- [x] Implement durable pause/resume/cancel and retry-failed controls with race-safe server cancellation and reservation release.
- [x] Preserve hierarchy, empty files/directories and completed partial-batch work; report skipped links, special files, source changes and failure totals.

**Remaining qualification:** native Windows/macOS and deployment filesystem/power-loss/resource checks. Linux SQLite/TLS process-kill, interrupted scan and scope/control regressions pass; this does not close those hardware gates.

**Accept when:** kill/restart both processes mid-batch and interrupt acknowledgements; completed files stay complete, missing work resumes, changed sources become fresh attempts and pause/cancel states survive restart. A large scan stays within recorded memory/handle budgets without an arbitrary file-count cap.

## 6. Reliable browsing and resumable restores

**Outcome:** users can browse large catalogs and safely restore selected files or folders across interruptions.

**Implemented:** 100-entry catalog pages use account/folder/revision-bound signed cursors. Changed, expired or restarted snapshots require a full refresh; unchanged revision probes preserve the current page. File/folder selections, child discoveries, collision choices and download checkpoints persist. Unsafe paths/collisions fail explicitly; verified complete content is flushed before publication.

**Depends on:** features 3, 4, 5; version selection completes with feature 8.

- [x] Retry interrupted single-file reads and retain flushed chunk checkpoints across attempts. Revalidate checkpoints, copy into a fresh exclusive file without writing through existing links, and publish by destination-directory rename.
- [x] Replace truncated catalogs with account/query/snapshot-bound pagination and bounded desktop lists.
- [x] Implement catalog revisions, reconnect reconciliation and full refresh after expired history; events are hints, not the source of truth.
- [x] Add recursive folder and multi-file download selection, persistent temporary destinations and chunk-boundary resume.
- [x] Verify complete content, flush and publish atomically; define explicit existing-file collision choices.
- [x] Enforce destination containment, symlink refusal, Unicode/case collisions, reserved names and platform path-length rules.
- [x] Handle disk-full, corruption, missing versions and interrupted publication without presenting partial files as complete.

**Remaining qualification:** native Windows/macOS path/ACL/atomic-publication and actual disk-full/power-loss checks. Linux tests cover large catalogs, interrupted recursive restores, corruption, link/path refusal and publication acknowledgement loss.

**Accept when:** catalogs exceeding 1000 entries are fully reachable; concurrent catalog changes do not silently lose entries; interrupted folder restores resume and match source bytes; path attacks and corruption cannot overwrite unintended local files.

## 7. Accurate storage and transfer visibility

**Outcome:** users and administrators can see actual capacity, retained data and active work without misleading totals.

**Implemented:** standard-mode account/server allocation and reservation snapshots, deduplicated filesystem capacity, ten-second payload/useful-upload/retry rates, redacted replayable SSE and desktop/admin dashboards. See [wire semantics and limits](protocol-transfers.md#implemented-standard-visibility).

**Depends on:** storage transitions from feature 3 and existing role enforcement. Deliver standard storage/transfer visibility now; extend accounting for retention in feature 8 and private overhead in feature 9, and integrate the completed administration UI in feature 11. Those later scenarios remain acceptance gates.

- [x] Reconcile committed, retained/trash, staged and reserved physical bytes per account/server; report logical standard bytes separately where useful.
- [x] Identify underlying filesystems and avoid summing duplicate free space across mounts.
- [x] Publish useful versus wire/retry rates, last-update times and stale/offline status.
- [x] Add bounded reconnectable events and dashboards with authorization, private-data redaction and slow-consumer isolation.

**Evidence:** fixtures cover reservation/chunk retry/cancel/restart, current/retained/deleted data, filesystem deduplication, account/role isolation, event replay/restart and slow-consumer isolation. Native Windows/macOS, real Unraid storage, recoverable trash and private overhead remain later acceptance gates.

**Accept when:** known fixtures, retries, deletion, interrupted uploads and restarts converge to measured on-disk totals; duplicated mounts are not double-counted and slow dashboards do not stall transfers.

## 8. Versions, trash and retention

**Outcome:** overwrites and accidental deletions are recoverable, and concurrent devices preserve both edits.

**Implemented:** standard-mode stable overwrite IDs, revision-checked head selection, bounded history/version downloads, recoverable file/folder trash and administrator retention controls. Schema 12 preserves existing versions and exposes earlier displaced files in trash. Native Windows/macOS filesystem and actual Unraid/power-loss qualification remain open.

**Depends on:** features 3, 5, 6. Define private opaque IDs compatibly with feature 9.

- [x] Publish new versions under stable file IDs with expected-revision checks; preserve both results of concurrent head updates and expose resolution.
- [x] Add history, version download and restore-as-current flows.
- [x] Replace ordinary permanent deletion with trash; add restore and explicit purge with clear confirmation. Document that previously deleted content cannot be recovered by this migration.
- [x] Add configurable version/trash retention, initially proposed at 30 days for trash, and administrator controls.
- [x] Coordinate reference-aware collection with active readers, uploads, reservations and backup snapshots; record retained/trash accounting and cleanup audit events.

**Accept when:** two devices race updates without losing either version; trash restore works; purge/restore/finalize/reader races and repeated cleanup crashes never delete referenced objects; retention changes have tested boundary behavior.

## 9. Private encrypted storage and recovery

**Outcome:** optional private contents and names leave the desktop only as ciphertext, and a clean device can recover them.

**Implemented (experimental):** authenticated private objects, account vault envelopes, desktop setup/unlock/recovery export and native-store preferences, encrypted durable tasks, private catalog/folder restore, history/trash and backup recovery. See [format, usage and bounds](private-storage.md). Server schema 13 and desktop schema 3 preserve existing data.

**Early prerequisite work:** run the format/library evaluation, bounded-object prototype, test vectors and initial small-file impact review before feature 3 freezes contracts. Obtain approvals for new dependencies or new crypto usage. Measure encryption/KDF cost and bounded concurrency as part of this work. Full integration depends on features 4, 5, 6, 8. Use the server/account scope established by feature 4; extend to simultaneous profiles when feature 10 arrives.

- [x] Specify and review authenticated, versioned objects/manifests, key hierarchy, associated data and nonce/randomness rules; test swaps, duplication, truncation, wrong keys and tampering.
- [x] Create optional per-account vaults with bounded KDF parameters, passphrase and recovery wrappers, creation races and revision-checked atomic rewrap.
- [x] Add setup/unlock/recovery export, explicit loss acknowledgement, optional remembered unlock and auto-lock policy separate from account login.
- [x] Integrate encryption/decryption with resumable queues: retransmit identical persisted ciphertext, use fresh randomness for fresh attempts and bound staging space.
- [x] Encrypt names, paths, sensitive metadata and the local private index; add client-assisted private-name conflict resolution and clear decrypted views on lock.
- [x] Support per-batch mode and per-profile defaults without rewriting existing versions; standard work must remain usable with the vault locked or absent.
- [x] Document rollback/freshness and device-revocation limits; extend backup/restore coverage to envelopes and recovery packages.

**Remaining qualification:** independent crypto review; native Windows/macOS filesystem/credential-store/UI checks; actual Unraid/power-loss drills; streaming staging beyond the current 1 GiB private-file limit. Local integration verifies interrupted ciphertext retransmission, clean-client and backup recovery, revision races and server disk canaries. These limits keep release acceptance open.

**Accept when:** clean-device recovery restores private files using exported recovery material; interrupted transfers and rewraps are safe; plaintext/key canaries never reach server disk/database/logs/API/telemetry; all format vectors and adversarial tests pass. Lost account credentials and lost vault keys remain distinct recovery cases.

## 10. Multiple servers and trusted routes

**Outcome:** multiple independent accounts/servers work simultaneously, with safe LAN/tailnet switching.

**Status:** implementation complete; Linux integration evidence is recorded in the [verification map](requirements-verification.md#multiple-servers-and-trusted-routes--2026-11-18). Native Windows/macOS and actual LAN/tailnet qualification remain open.

**Depends on:** features 4, 5, 6. Add combined/per-server queue views here; feature 13 later completes their background lifecycle and UI qualification.

- [x] Add/edit/remove persistent profiles containing scoped accounts, trusted identity, approved endpoints and preferences.
- [x] Complete pairing and explicit identity/CA repair; validate signed challenges against the actual TLS connection, consume nonces and reject redirects/unapproved origins before sending credentials.
- [x] Probe only configured routes with bounded timeouts/failure caching; retain upload identity when switching LAN/tailnet endpoints.
- [x] Support simultaneous sign-in, combined/per-server queues and separate catalogs; an offline server must not block another.
- [x] Document profile removal, credential deletion and local queue/data retention behavior.

**Accept when:** two servers with colliding usernames/file IDs remain isolated; route switches resume the same transfer; legitimate certificate renewal succeeds; replacement servers and relayed proofs receive no credentials.

## 11. Complete account administration

**Outcome:** administrators can manage account recovery and access through the browser without operator intervention for routine tasks.

**Status:** browser and desktop account controls are implemented. Target Unraid Argon2id calibration remains open.

**Depends on:** feature 4; retention controls integrate with feature 8 and capacity views with feature 7.

- [x] Expose audited, role-checked password reset, distinct lost-TOTP recovery and individual device revocation through admin API and UI.
- [x] Expose account disable/enable and remaining self-service session controls.
- [x] Prevent mixed password/TOTP resets with a transactional account recovery lock until full recovery login; allow same-kind retry. Both reset types revoke every device while preserving the other factor. Auth/database/HTTP and UI checks cover conflicting administrators and completion rules.
- [x] Require fresh administrator reauthentication for sensitive actions; enforce invalidation and preserve the distinction between password and TOTP recovery.
- [x] Complete self-service recovery-code replacement and account/device screens.
- [x] Restore show-once temporary-password delivery. Schema 14 removes retained encrypted material and expires credentials that had retained copies.
- [x] Add paginated audit history, meaningful empty/error states and reset/recovery guidance; account recovery must never imply vault-key recovery.
- [x] Verify bounded concurrent authentication resource use and provide a repeatable Argon2id benchmark.
- [ ] Calibrate Argon2id costs on the target Unraid host. See [server development](server-development.md#argon2id-calibration).

**Accept when:** every administration action is available to authorized callers, denied to other users and audited; recovery/revocation invalidates sessions as specified; temporary-password visibility matches the documented policy and expiry behavior.

## 12. Supported deployment, backup and upgrades

**Outcome:** the server can be operated, upgraded and restored predictably on the actual Unraid host.

**Status:** tooling implemented; every remaining box needs the actual Unraid host and networks. Run and record the [deployment qualification](unraid-testing.md#deployment-qualification-record).

**Implemented:** versioned Unraid template with a `/backups` mount; `serversend -restore DIR -restore-as replace|clone` into empty volumes; automatic pre-upgrade backups, refused migration without a backup policy; backup manifests with schema version and server ID; a [compatibility record](releases.md#compatibility); and a publish guard against overwriting exact image versions. Linux tests cover upgrade refusal, schema-13 backup and migration, downgrade restore, protected rollback backups, replace/clone identity and sessions, private-data clone refusal and failed-restore cleanup.

**Depends on:** features 3, 4, 8 and 9; verify QUIC reachability after feature 2.

- [x] Finish the versioned Unraid template (`apps/server/deploy/unraid/serversend.xml`) with ports, four paths, certificate IPs and upgrade backups.
- [ ] Validate the template, UID/GID, mounts, locking, resource guidance and pool/array/separate-staging layouts on actual hardware.
- [ ] Validate LAN, direct-tailnet and relayed-tailnet transfers, blocked-UDP fallback, private IPv4 CA provisioning, browser/per-profile trust and certificate renewal without public exposure.
- [x] Deliver an offline standard-data backup tool with a stopped-server maintenance barrier, identity/keys/object copies and consistent SQLite snapshot; document operator pre-upgrade backups and matching-image downgrade restore. Local backup/restore evidence is recorded under feature 3.
- [x] Extend backup tooling to private envelopes; recovery packages stay client-side and are exported separately (feature 9). Live snapshots, if implemented, require reference snapshots with GC suspension.
- [ ] Qualify backup and restore on actual Unraid storage.
- [x] Add a local isolated standard restore integration check preserving identity and downloading original bytes; document replacement-only identity reuse and require the original process to remain stopped.
- [x] Enforce an explicit replace/clone restore mode; clones get a new identity and CA, revoke sessions and refuse private-mode data bound to the original server ID. Local private recovery-key drills pass (feature 9).
- [ ] Run replacement and clone restore drills on actual Unraid.
- [x] Automate pre-upgrade backups before migrations and test migration from every prior schema plus downgrade restore. Record tested client/server compatibility; refuse to overwrite exact image versions.
- [ ] Execute an image update with migration and the documented downgrade restore on the actual deployment.

**Accept when:** an actual Unraid deployment survives restart/update and restores from backup onto an isolated instance with both storage modes intact; tested TCP/UDP, filesystem and permission outcomes are recorded. A workflow file or instructions alone do not close this feature.

## 13. Background desktop operation

**Outcome:** backups continue while the app is in the tray, with clear controls and predictable lifecycle behavior.

**Existing:** transfer screen, themes and throughput chart; no qualified background lifecycle.

**Depends on:** features 4, 5, 6.

- [x] Add tray close behavior, explicit quit with durable checkpoints, optional launch at login and sleep/wake recovery. Closing hides to the tray; relaunching shows the window (also where no tray is shown). Quit cancels workers and waits for the queue to close; login launch starts hidden (`--background`). Wake interrupts in-flight requests, drops pooled connections and clears network back-off. `TestBackgroundWakePauseAndQuit`, the live smoke and a Linux native hidden-start/second-instance/SIGTERM run pass.
- [x] Show per-file/batch progress and accurate completed/skipped/failed counts, bounded skipped paths/reasons, completed-with-skips and all-skipped outcomes. Retain bounded recent summaries/finished rows; Vue tests and the generated Batches bridge smoke pass. This remains an in-memory foreground workflow.
- [x] Build complete queue controls and per-file/batch progress, skipped/failed counts, useful rates and retry actions without premature ETAs. Per-item/selection pause, resume, cancel and retry, plus a durable global pause shared by the window and tray; no ETAs.
- [x] Bound/throttle progress events and virtualize large lists; a slow frontend must not stall transfers. Events coalesce to five per second from their own goroutine with at most 200 pending items; lists are bounded (200 rows, 100 summaries, 100-row pages) rather than virtualized (`TestSlowWindowPendingMapIsBounded`).
- [ ] Complete keyboard/focus/contrast and both-theme checks for the real workflows. Scaffold greeting service, `HelloWorld` and the time event were removed; the smoke now uses `BackgroundService`.

**Accept when:** close-to-tray keeps work running; explicit quit/relaunch and sleep/wake resume safely; platform-native tests cover Linux, Windows and macOS and confirm transfers do not run while the app is exited or the device sleeps.

## 14. Desktop distribution and release qualification

**Outcome:** installable, supported clients and evidence that the full intended product works under realistic failures and scale.

**Existing:** native build/check workflows; they do not constitute published, signed installers or proven platform support.

**Depends on:** acceptance of all preceding features. Execute smaller platform/integration checks throughout development, not only here.

- [ ] Build and publish Linux packages first, then Windows and required macOS packages; verify native dialogs, secure storage, tray, startup, migration and uninstall/data-retention behavior.
- [ ] Configure signing/notarization where credentials exist; clearly label unsigned development artifacts.
- [ ] Record hosted Linux/Windows/macOS checks, native SQLite/CGO qualification and real UI walkthroughs; do not infer results from configured jobs.
- [ ] Update every planned row in the requirement-to-test map with owning tests, execution evidence and remaining release blockers.
- [ ] Run two-server/two-user/multiple-device isolation, concurrent updates and replacement-server scenarios.
- [ ] Run a real non-sparse >100-GiB file and at least 100,000 small files, concurrent clients and fairness/resource limits, process kills, network failures, full disks, corruption and revocation in standard/private modes.
- [ ] Complete race/fuzz/dependency/format review, record hardware/resource measurements and demonstrate clean-device restores and operational recovery.

**Accept when:** all intended features and platform gates have recorded passing evidence, with no unresolved data-loss, authorization-bypass, plaintext-leak or recovery defects. Until then, label releases experimental and describe specific limits.

## Historical requirement mapping

This table preserves references in older documents; historical chunk numbers are not the work sequence.

| Original chunks | Current feature ownership |
| --- | --- |
| 01–04: workspace, contracts, persistence, process | Existing baseline; remaining gates in 3, 12, 14; checks apply to every feature |
| 05–06: passwords and TOTP | 4, 11 |
| 07: sessions | 4 |
| 08: profiles/credentials | 4, 10 |
| 09: onboarding UI | 4, 11, 13 |
| 10: catalog/versions | 6, 8, 9 |
| 11–12: storage/upload lifecycle | 3, 5 |
| 13–14: scan/queue/uploads | 1, 5 |
| 15: downloads | 6 |
| 16: background/UI | 13 |
| 17–20: private format/vault/transfers/UX | 9; format prototype precedes storage contract freeze in 3 |
| 21: accounting/events | 6, 7 |
| 22: administration | 4, 8, 11, 7 |
| 23: retention/cleanup | 3, 8 |
| 24: performance/QUIC | 2; baseline measurements precede contract freeze in 3 |
| 25–26: deployment/backup/upgrades | 3, 12 |
| 27–28: packaging/qualification | 14; relevant native/failure checks accompany each feature |

## Still outside scope

Automatic folder watching, bidirectional synchronization, server replication, public sharing, shared identity across servers, cross-user deduplication, mobile clients and transfers while logged out of the OS remain deferred. Filesystem snapshot integration and emergency vault-key rotation remain separate designs. This reordering does not add them to the release or remove any original release requirement.
