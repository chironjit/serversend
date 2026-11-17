# ServerSend feature implementation tasks

This is the active delivery roadmap, reorganized on 2026-11-17 around features in priority order. It supersedes the [historical chunk checklist](implementation-tasks-archive.md). The [implementation plan](implementation-plan.md) remains the full product specification. Earlier component documents and verification rows may still cite historical chunk numbers; the mapping below preserves their meaning.

## Current baseline

Already implemented: versioned contracts and identity-proof helpers; SQLite foundations and migrations; a TLS server with persistent identity and certificates; administrator bootstrap; Argon2id passwords, mandatory replacement, TOTP and recovery codes; seven-day admin cookies, rotating desktop refresh credentials with 30-day inactivity/60-day second-factor limits, desktop profiles and optional OS-keychain persistence; browser user listing/creation; sequential standard uploads, recursive folders and empty directories; folder browsing/creation; verified whole-file downloads; confirmed permanent deletion with restartable deleted-object cleanup; Tailwind components and theme selection in both interfaces; desktop throughput display; component checks and release workflows.

These are existing building blocks, not completed versions of the features below. The desktop queue remains in memory; remembered session credentials use the OS credential store. There is no transfer resume, private mode, trash or complete version-management experience. Catalogs currently stop at 1000 entries per directory. Hosted CI results, actual Unraid qualification and complete native UI workflows must be verified, not inferred from workflow files. This status review uses the current implementation and previously executed checks recorded in the verification map; it does not represent a fresh test run.

Current pending temporary passwords can be retrieved by administrators until replacement or one-hour expiry. The original specification calls for showing them once. Resolve that discrepancy in Complete account administration; do not silently treat the current behavior as the final security policy.

### Delivery status, 2026-11-17

Delivered work includes consistent no-follow source opening and bounded skipped-item batch reports, desktop summaries, HTTP/3-first connections with verified HTTP/2 fallback, [reproducible performance measurements](performance-baseline.md), deployment-directory instance locks, and a [stopped-server backup command](backup-restore.md). Every replacement connection tries QUIC again; a working HTTP/2 fallback connection has no timed upgrade attempts. The local tmpfs results do not establish Unraid/tailnet performance. The [verification map](requirements-verification.md#feature-roadmap-execution-2026-11-17) records executed checks and remaining native/hardware gates.

Checked items mean the stated scope is implemented with recorded evidence. Compound tasks are split below where only part has been delivered. Full feature acceptance still requires its remaining checks; none of the 14 features is fully accepted yet.

| Feature | Status | Delivered scope / remaining work |
| --- | --- | --- |
| 1. Symlink handling | Partial | Skip/report, safe source opening and desktop summaries delivered; native Windows/macOS and picker qualification open |
| 2. Performance and QUIC | Partial | QUIC-first transport complete; deployment resource/performance validation open; later integrations tracked in features 5, 9, 10, 12 and 14 |
| 3. Recoverable storage | Partial | Instance locks, offline backup/restore, startup reconciliation, quota reservations and upload retirement delivered; actual filesystem/power-loss qualification open |
| 4. Persistent sign-in/devices | Partial | Refresh, installation deduplication, verified trust/scoped storage, self-service/admin devices and browser protections delivered; complete native qualification open |
| 5. Resumable uploads | Pending | Existing in-memory queue only; persistence and resume open |
| 6. Browsing/restores | Pending | Existing folder browsing and whole-file downloads only; pagination and resumable folder restores open |
| 7. Storage visibility | Pending | Existing local throughput display only; accounting and dashboards open |
| 8. Versions/trash | Pending | Existing version records and permanent deletion only; history, trash and retention open |
| 9. Private storage | Pending | Contracts only; format prototype, encryption and recovery open |
| 10. Multiple servers/routes | Pending | Saved profiles with one active connection; simultaneous connections and routes open |
| 11. Account administration | Pending | Existing user creation/listing and backend foundations only; complete controls and security policy open |
| 12. Deployment/backup/upgrades | Partial | Initial standard backup/restore and operator guidance delivered; actual Unraid, private recovery and version compatibility open |
| 13. Background desktop | Partial | Bounded batch summaries and skip/failure counts delivered; durable controls, tray and native lifecycle open |
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

- [x] Verify chunk length/digest, flush object bytes and publish by rename before recording the chunk; commit file metadata transactionally. Retain restartable cleanup of explicitly deleted objects. Crash-boundary qualification remains below.
- [x] Lock config/data/staging before initialization so a second process cannot modify the same deployment. Local exclusivity, rollback and crash-release tests pass.
- [x] Provide `serversend -backup DIR` with a stopped-server barrier and consistent SQLite snapshot. The isolated restore test preserves identity/account/catalog and downloads the original standard-file bytes.
- [x] Document pre-upgrade backups and matching-backup/image downgrade restoration in [backup/restore](backup-restore.md); reject unsupported schemas.
- [x] Reconcile interrupted publication at startup; flush shard/root/source directory entries before acknowledgement. Test process exit before metadata recording, metadata rollback, short writes and injected full/read-only/flush errors. Refuse missing/truncated acknowledged data.
- [ ] Qualify actual full/read-only filesystems and power-loss durability on the intended deployment; injected errors and tmpfs process exits do not establish this.
- [x] Add configurable per-user/global content quotas, transactional reservations and data-volume free-space checks. Concurrent admission cannot exceed declared capacity; external writers/filesystem overhead still require operator headroom. Existing request/chunk/manifest bounds remain in force.
- [x] Retire expired uploads at startup/admission and every minute; expose idempotent account-scoped cancellation, retain key tombstones, bound chunks by reservations and serialize cancellation/finalization/publication. Duplicate/conflicting chunks and cancellation races have regression coverage. Durable desktop controls remain feature 5 work.
- [x] Clean abandoned uploads through durable deletion intents, reject deletion of referenced objects, and remove orphan objects only at startup under the deployment lock. Repeat maintenance safely with bounded enumeration/deletion batches.

**Scope:** current object writes use `data/incoming` and publish into `data/objects` on the same filesystem. A separate staging-to-data copy path is unnecessary today. If a later feature uses the staging volume for object publication, add destination-side copy/verification/flush/rename and account for temporary overlap then. Private-format work in feature 9 must precede freezing shared/private contracts; it does not block fixing current standard-data safety. Actual Unraid locking/durability and upgrade qualification belong to feature 12.

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

**Existing:** sequential in-memory transfer manager, recursive scans and a server missing-chunk route; database queue foundations are not connected to this workflow.

**Depends on:** features 3, 4. Design server/account scoping for feature 10 now.

- [ ] Persist jobs before network work; stream discovered files/directories into SQLite and resume interrupted scans without unbounded batch buffering.
- [ ] Persist upload IDs, source fingerprints, acknowledgements, retry schedules and explicit queue/wait states.
- [ ] Reconcile missing chunks after reconnect, lost acknowledgements and restart; never mix source generations when files change, disappear or become unreadable.
- [ ] Add bounded workers, fair scheduling, bandwidth controls and typed retry/backoff; one failing job/profile must not block other eligible work. Preserve upload identity and acknowledged progress through transport fallback/reconnect.
- [ ] Implement durable pause/resume/cancel and retry-failed controls with race-safe server cancellation and reservation release.
- [ ] Preserve hierarchy, empty files/directories and completed partial-batch work; report skipped links, special files, source changes and failure totals.

**Accept when:** kill/restart both processes mid-batch and interrupt acknowledgements; completed files stay complete, missing work resumes, changed sources become fresh attempts and pause/cancel states survive restart. A large scan stays within recorded memory/handle budgets without an arbitrary file-count cap.

## 6. Reliable browsing and resumable restores

**Outcome:** users can browse large catalogs and safely restore selected files or folders across interruptions.

**Existing:** folder navigation and verified whole-file downloads; 1000-entry listing limit and no persisted download progress.

**Depends on:** features 3, 4, 5; version selection completes with feature 8.

- [ ] Replace truncated catalogs with account/query/snapshot-bound pagination and bounded desktop lists.
- [ ] Implement catalog revisions, reconnect reconciliation and full refresh after expired history; events are hints, not the source of truth.
- [ ] Add recursive folder and multi-file download selection, persistent temporary destinations and chunk-boundary resume.
- [ ] Verify complete content, flush and publish atomically; define explicit existing-file collision choices.
- [ ] Enforce destination containment, symlink refusal, Unicode/case collisions, reserved names and platform path-length rules.
- [ ] Handle disk-full, corruption, missing versions and interrupted publication without presenting partial files as complete.

**Accept when:** catalogs exceeding 1000 entries are fully reachable; concurrent catalog changes do not silently lose entries; interrupted folder restores resume and match source bytes; path attacks and corruption cannot overwrite unintended local files.

## 7. Accurate storage and transfer visibility

**Outcome:** users and administrators can see actual capacity, retained data and active work without misleading totals.

**Existing:** local progress and throughput chart. Storage accounting is incomplete.

**Depends on:** storage transitions from feature 3 and existing role enforcement. Deliver standard storage/transfer visibility now; extend accounting for retention in feature 8 and private overhead in feature 9, and integrate the completed administration UI in feature 11. Those later scenarios remain acceptance gates.

- [ ] Reconcile committed, retained/trash, staged and reserved physical bytes per account/server; report logical standard bytes separately where useful.
- [ ] Identify underlying filesystems and avoid summing duplicate free space across mounts.
- [ ] Publish useful versus wire/retry rates, last-update times and stale/offline status.
- [ ] Add bounded reconnectable events and dashboards with authorization, private-data redaction and slow-consumer isolation.

**Accept when:** known fixtures, retries, deletion, interrupted uploads and restarts converge to measured on-disk totals; duplicated mounts are not double-counted and slow dashboards do not stall transfers.

## 8. Versions, trash and retention

**Outcome:** overwrites and accidental deletions are recoverable, and concurrent devices preserve both edits.

**Existing:** basic immutable version records and confirmed permanent deletion. No user-facing history, trash or retention policy.

**Depends on:** features 3, 5, 6. Define private opaque IDs compatibly with feature 9.

- [ ] Publish new versions under stable file IDs with expected-revision checks; preserve both results of concurrent head updates and expose resolution.
- [ ] Add history, version download and restore-as-current flows.
- [ ] Replace ordinary permanent deletion with trash; add restore and explicit purge with clear confirmation. Document that previously deleted content cannot be recovered by this migration.
- [ ] Add configurable version/trash retention, initially proposed at 30 days for trash, and administrator controls.
- [ ] Coordinate reference-aware collection with active readers, uploads, reservations and backup snapshots; record retained/trash accounting and cleanup audit events.

**Accept when:** two devices race updates without losing either version; trash restore works; purge/restore/finalize/reader races and repeated cleanup crashes never delete referenced objects; retention changes have tested boundary behavior.

## 9. Private encrypted storage and recovery

**Outcome:** optional private contents and names leave the desktop only as ciphertext, and a clean device can recover them.

**Existing:** outer contracts and explicit unsupported-format rejection; no production private encryption or vault workflow.

**Early prerequisite work:** run the format/library evaluation, bounded-object prototype, test vectors and initial small-file impact review before feature 3 freezes contracts. Obtain approvals for new dependencies or new crypto usage. Measure encryption/KDF cost and bounded concurrency as part of this work. Full integration depends on features 4, 5, 6, 8. Use the server/account scope established by feature 4; extend to simultaneous profiles when feature 10 arrives.

- [ ] Specify and review authenticated, versioned objects/manifests, key hierarchy, associated data and nonce/randomness rules; test swaps, duplication, truncation, wrong keys and tampering.
- [ ] Create optional per-account vaults with bounded KDF parameters, passphrase and recovery wrappers, creation races and revision-checked atomic rewrap.
- [ ] Add setup/unlock/recovery export, explicit loss acknowledgement, optional remembered unlock and auto-lock policy separate from account login.
- [ ] Integrate encryption/decryption with resumable queues: retransmit identical persisted ciphertext, use fresh randomness for fresh attempts and bound staging space.
- [ ] Encrypt names, paths, sensitive metadata and the local private index; add client-assisted private-name conflict resolution and clear decrypted views on lock.
- [ ] Support per-batch mode and per-profile defaults without rewriting existing versions; standard work must remain usable with the vault locked or absent.
- [ ] Document rollback/freshness and device-revocation limits; extend backup/restore coverage to envelopes and recovery packages.

**Accept when:** clean-device recovery restores private files using exported recovery material; interrupted transfers and rewraps are safe; plaintext/key canaries never reach server disk/database/logs/API/telemetry; all format vectors and adversarial tests pass. Lost account credentials and lost vault keys remain distinct recovery cases.

## 10. Multiple servers and trusted routes

**Outcome:** multiple independent accounts/servers work simultaneously, with safe LAN/tailnet switching.

**Existing:** saved server profiles and certificate fingerprints, optional OS-keychain sessions, single active desktop connection and shared identity-proof contracts. Feature 2 preserves transport pinning; feature 4 establishes persistent single-profile trust.

**Depends on:** features 4, 5, 6. Add combined/per-server queue views here; feature 13 later completes their background lifecycle and UI qualification.

- [ ] Add/edit/remove persistent profiles containing scoped accounts, trusted identity, approved endpoints and preferences.
- [ ] Complete pairing and explicit identity/CA repair; validate signed challenges against the actual TLS connection, consume nonces and reject redirects/unapproved origins before sending credentials.
- [ ] Probe only configured routes with bounded timeouts/failure caching; retain upload identity when switching LAN/tailnet endpoints.
- [ ] Support simultaneous sign-in, combined/per-server queues and separate catalogs; an offline server must not block another.
- [ ] Document profile removal, credential deletion and local queue/data retention behavior.

**Accept when:** two servers with colliding usernames/file IDs remain isolated; route switches resume the same transfer; legitimate certificate renewal succeeds; replacement servers and relayed proofs receive no credentials.

## 11. Complete account administration

**Outcome:** administrators can manage account recovery and access through the browser without operator intervention for routine tasks.

**Existing:** user list/creation, admin device history/revocation and separate password/TOTP reset UI/API with recovery locking. Disablement services exist; UI, self-service recovery and remaining administration gates are open.

**Depends on:** feature 4; retention controls integrate with feature 8 and capacity views with feature 7.

- [x] Expose audited, role-checked password reset, distinct lost-TOTP recovery and individual device revocation through admin API and UI.
- [ ] Expose account disable/enable and remaining self-service session controls.
- [x] Prevent mixed password/TOTP resets with a transactional account recovery lock until full recovery login; allow same-kind retry. Both reset types revoke every device while preserving the other factor. Auth/database/HTTP and UI checks cover conflicting administrators and completion rules.
- [ ] Require fresh administrator reauthentication for sensitive actions; enforce invalidation and preserve the distinction between password and TOTP recovery.
- [ ] Complete self-service recovery-code replacement and account/device screens.
- [ ] Restore show-once temporary-password delivery, or obtain an explicit product decision to revise the specification; migrate away from retained pending-password material if show-once is retained.
- [ ] Add paginated audit history, meaningful empty/error states and reset/recovery guidance; account recovery must never imply vault-key recovery.
- [ ] Calibrate Argon2id costs on target hardware and verify bounded concurrent authentication resource use.

**Accept when:** every administration action is available to authorized callers, denied to other users and audited; recovery/revocation invalidates sessions as specified; temporary-password visibility matches the documented policy and expiry behavior.

## 12. Supported deployment, backup and upgrades

**Outcome:** the server can be operated, upgraded and restored predictably on the actual Unraid host.

**Existing:** non-root Docker build, Compose examples, certificates, release workflow and deployment/update documentation. Feature 3 delivers an initial offline standard backup/restore path.

**Depends on:** features 3, 4, 8 and 9; verify QUIC reachability after feature 2.

- [ ] Finish the versioned Unraid template and validate UID/GID, mounts, locking, resource guidance and pool/array/separate-staging layouts on actual hardware.
- [ ] Validate LAN, direct-tailnet and relayed-tailnet transfers, blocked-UDP fallback, private IPv4 CA provisioning, browser/per-profile trust and certificate renewal without public exposure.
- [x] Deliver an offline standard-data backup tool with a stopped-server maintenance barrier, identity/keys/object copies and consistent SQLite snapshot; document operator pre-upgrade backups and matching-image downgrade restore. Local backup/restore evidence is recorded under feature 3.
- [ ] Extend backup tooling to private envelopes/recovery material and qualify it on actual Unraid storage. Live snapshots, if implemented, require reference snapshots with GC suspension.
- [x] Add a local isolated standard restore integration check preserving identity and downloading original bytes; document replacement-only identity reuse and require the original process to remain stopped.
- [ ] Complete replacement-versus-clone enforcement and independent-clone identity migration; extend restore drills to recovery-key private downloads and actual Unraid.
- [ ] Automate pre-upgrade backup integration and migration compatibility checks. Record tested client/server version compatibility and avoid overwriting release tags; execute the documented downgrade restore procedure on supported deployments.

**Accept when:** an actual Unraid deployment survives restart/update and restores from backup onto an isolated instance with both storage modes intact; tested TCP/UDP, filesystem and permission outcomes are recorded. A workflow file or instructions alone do not close this feature.

## 13. Background desktop operation

**Outcome:** backups continue while the app is in the tray, with clear controls and predictable lifecycle behavior.

**Existing:** transfer screen, themes and throughput chart; no qualified background lifecycle.

**Depends on:** features 4, 5, 6.

- [ ] Add tray close behavior, explicit quit with durable checkpoints, optional launch at login and sleep/wake recovery.
- [x] Show per-file/batch progress and accurate completed/skipped/failed counts, bounded skipped paths/reasons, completed-with-skips and all-skipped outcomes. Retain bounded recent summaries/finished rows; Vue tests and the generated Batches bridge smoke pass. This remains an in-memory foreground workflow.
- [ ] Build complete queue controls and per-file/batch progress, skipped/failed counts, useful rates and retry actions without premature ETAs.
- [ ] Bound/throttle progress events and virtualize large lists; a slow frontend must not stall transfers.
- [ ] Complete keyboard/focus/contrast and both-theme checks for the real workflows; remove scaffold-only UI/services when their smoke coverage is replaced.

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
