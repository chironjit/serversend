# ServerSend feature implementation tasks

This is the active delivery roadmap, reorganized on 2026-11-17 around features in priority order. It supersedes the [historical chunk checklist](implementation-tasks-archive.md). The [implementation plan](implementation-plan.md) remains the full product specification. Earlier component documents and verification rows may still cite historical chunk numbers; the mapping below preserves their meaning.

## Current baseline

Already implemented: versioned contracts and identity-proof helpers; SQLite foundations and migrations; a TLS server with persistent identity and certificates; administrator bootstrap; Argon2id passwords, mandatory replacement, TOTP and recovery codes; temporary 12-hour sessions; browser user listing/creation; sequential standard uploads, recursive folders and empty directories; folder browsing/creation; verified whole-file downloads; confirmed permanent deletion with restartable deleted-object cleanup; Tailwind components and theme selection in both interfaces; desktop throughput display; component checks and release workflows.

These are existing building blocks, not completed versions of the features below. The desktop queue and credentials remain in memory. There is no transfer resume, private mode, trash or complete version-management experience. Catalogs currently stop at 1000 entries per directory. Hosted CI results, actual Unraid qualification and complete native UI workflows must be verified, not inferred from workflow files. This roadmap records source review, not a fresh execution of the tests.

Current pending temporary passwords can be retrieved by administrators until replacement or one-hour expiry. The original specification calls for showing them once. Resolve that discrepancy in Complete account administration; do not silently treat the current behavior as the final security policy.

### Implementation in progress, 2026-11-17

The first parallel delivery includes consistent no-follow source opening and bounded skipped-item batch reports, desktop summaries, HTTP/3 evaluation with verified HTTP/2 fallback, [reproducible performance measurements](performance-baseline.md), deployment-directory instance locks, and a [stopped-server backup command](backup-restore.md). Connections try HTTP/3 first and use HTTP/2 only after QUIC fails; the local tmpfs results do not establish Unraid/tailnet performance. This is partial delivery of features 1–3, not acceptance of the complete roadmap. The [verification map](requirements-verification.md#feature-roadmap-execution-2026-11-17) records executed checks and remaining native/hardware gates.

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

**Outcome:** encountering a link does not abort a folder upload or prevent unrelated files from being backed up. This is the critical first fix, independent of the later persistent queue work.

**Original defect:** `UploadFolder` used `Lstat` and returned an error for symlinks/special files, stopping the remaining traversal; direct file uploads could follow a file symlink. The implementation now applies one skip-and-report policy using the explicitly approved `golang.org/x/sys` filesystem APIs. Linux runtime/race and UI checks pass. Native Windows/macOS and picker qualification remain open, so the feature is not accepted as complete.

**Approved policy (2026-11-17):** skip symbolic links and directory redirections, continue scanning, and report them visibly. The user selected skip-and-report for the initial implementation; this is no longer an open policy decision. Implementation and native-platform verification remain pending. Following targets is a possible later opt-in, not part of the immediate fix or an approved requirement. Skipped links are not backed up as link objects and are not recreated on restore.

- [x] Apply consistent no-follow classification to recursive entries, selected root folders and directly selected files. Report an explicitly selected link as skipped with guidance to select its real target if desired. Linux runtime coverage passes; native Windows/macOS qualification remains below.
- [x] Continue past file/directory links, broken links and link cycles without resolving or reading their targets; retain normal files and empty directories before and after skipped entries. Linux scanner/source tests pass.
- [ ] Verify Linux/macOS symbolic links and Windows file/directory symlinks, junctions and other name-surrogate reparse points. Classify by platform semantics, not file extensions or a blanket assumption that every Windows reparse point is a symlink. Define and test behavior for unsupported/cloud-placeholder cases separately.
- [x] Skip unsupported special files with an explicit reason; distinguish policy skips from actual read/permission failures, cancellation and server errors. Linux FIFO, permission, cancellation and server-failure tests pass; genuine failures remain failures.
- [x] Show skipped counts and a bounded, reviewable list of paths/reasons, including a clear “completed with skipped items” result and a zero-files-uploaded result for all-skipped selections. Linux manager and Vue tests pass. Preservation when the persistent queue arrives remains an explicit feature 5 integration gate.
- [x] Guard source opening against a regular file/directory being replaced by a link after discovery; do not rely on a check-then-open sequence alone. Linux file/ancestor replacement races and worker rechecks pass. Link-bearing ancestors are refused; native picker resolution is documented as an outstanding platform qualification gate below.
- [x] Treat ordinary hard-linked regular files as regular files at each selected path; Linux source/selection tests pass. Link-relationship preservation and deduplication are outside this fix.
- [ ] Add scanner, direct-upload and UI coverage plus native Linux/Windows/macOS fixtures for nested links, broken links, outside-root targets, cycles, junctions, permission failures and replacement races. Record unavailable fixture privileges as an outstanding platform gate, not a passing test.

**Accept when:** mixed selections upload all eligible ordinary files and preserve empty directories despite links anywhere in the traversal; skipped entries have accurate visible reasons/counts; default uploads neither traverse directory redirections nor read linked targets; native platform evidence covers the cases above. No dependency on refresh tokens, persistent queues or QUIC is required to deliver this fix.

**Follow-target option: outside the approved scope.** A future proposal must decide separately whether to support files only or directories too. It must be explicit per selection, define outside-root/remote-target permission, detect cycles, bound traversal, handle changing targets and report duplicates. Store target contents as an ordinary file at the selected logical path, clearly explaining that this does not preserve the link. Users can explicitly select the real target meanwhile.

Platform references: [Go filesystem inspection](https://pkg.go.dev/os#Lstat), [Windows hard links and junctions](https://learn.microsoft.com/en-us/windows/win32/fileio/hard-links-and-junctions), [Windows reparse points](https://learn.microsoft.com/en-us/windows/win32/fileio/reparse-points).

## 2. Measured performance and QUIC

**Outcome:** many-small-file and large-file transfers use available bandwidth within explicit resource budgets.

**Early prerequisite work:** measure HTTP/2 per-file request/metadata overhead and candidate chunk sizes before feature 3 freezes APIs. Record hardware and provisional budgets then, not only at release.

**Delivery approach:** begin benchmarking and QUIC against the existing standard-file workflow now. Extend measurements and transport recovery tests as resumable transfers (5–6), visibility (7), private storage (9) and multiple routes (10) arrive; these later integrations remain acceptance gates, not prerequisites to starting this feature after the critical link-handling fix.

**Current implementation:** the user approved `quic-go` on 2026-11-17. The server serves the same API over TCP and UDP, with HTTP/3-first selection in the desktop/shared client. A credential-free same-origin probe is bounded to 500 ms on each connection establishment. HTTP/2 fallback lasts for that connection, with no timed upgrades; uncertain mutations are returned to the caller rather than blindly replayed. [The local baseline](performance-baseline.md) covers standard uploads, chunk sizes, hashing, directory enumeration and request reuse/batching candidates. Private mode, production batching, target-hardware limits, fairness and LAN/tailnet runs remain open.

- [ ] Benchmark scanning, hashing, encryption, network, metadata and finalization separately; test bounded batching/request reuse before increasing worker counts.
- [ ] Set measured defaults and configurable limits for workers, buffers, files, KDF concurrency and client/server memory; revise provisional format limits where measurements require it.
- [ ] Obtain approval before adding QUIC dependencies; implement HTTP/3 with verified HTTP/2 fallback, bounded probing/failure caching and no authentication/mutation 0-RTT.
- [ ] Preserve TLS/identity checks, upload identity and progress across route/transport changes.
- [ ] Measure LAN, direct tailnet and relayed tailnet with standard/private, small/large files and concurrent users.

**Accept when:** published results are reproducible, resource ceilings hold, fairness is demonstrated and blocked UDP falls back without lost work or weaker authentication. Choose transport defaults from results, not assumed speedups.

## 3. Safe and recoverable storage

**Outcome:** an acknowledged file remains recoverable after crashes, storage exhaustion and ordinary restarts. This follows the performance baseline and protects files users can already put into the application.

**Existing:** verified chunks, flush/rename storage, metadata transactions, filesystem validation and restartable cleanup of explicitly deleted objects. General orphan recovery and capacity reservation are absent.

**Current incremental safety work:** native deployment locks fence config, data and staging before TLS/metadata/object initialization; a duplicate instance sharing any volume is refused. `serversend -backup DIR` uses that stopped-server barrier, a consistent SQLite snapshot and streamed config/data/staging copies; see [backup and isolated restore](backup-restore.md). This does not freeze upload/private contracts or implement general orphan recovery, quotas, live snapshots or clone identity migration.

**Prerequisites:** before freezing manifests or upload APIs, run the private-format prototype in feature 9 and the small-file overhead baseline in feature 2. Performance/QUIC follows the critical link-handling fix; the private-format prototype must also precede contract freeze, with full encryption integration later.

- [ ] Define and test durable acknowledgement ordering for object writes, metadata and final publication; add recoverable pending transitions and idempotent startup reconciliation.
- [ ] Enforce per-user/global limits, request bounds, capacity reservations and filesystem-aware free-space checks, including temporary overlap on separate staging/data filesystems.
- [ ] Implement destination-side copy, verification, flush and rename for cross-filesystem staging; reject unsafe paths and symlink escapes.
- [ ] Complete scoped idempotency, duplicate/conflicting chunk handling, manifest validation, upload expiry and cancellation/finalization race rules. Record byte-accounting transitions for feature 7.
- [ ] Reconcile abandoned uploads and orphan objects without deleting referenced data or racing readers/writers; retain and test the existing deleted-object recovery path.
- [ ] Provide an initial consistent standard-data backup and isolated restore procedure covering metadata, identity, configuration keys and referenced objects. Enforce one process per storage directory and prevent accidental concurrent replacement instances.
- [ ] Add pre-migration backup/rollback guidance for changes introduced here; keep unsupported-schema rejection.

**Accept when:** injected crashes at each write/publication boundary, full/read-only disks, short writes and separate filesystems produce no acknowledged data loss or corrupt visible version; cleanup converges after repeated restarts; an isolated restored server downloads the original bytes. Record any hardware-dependent checks still needed for supported Unraid deployment.

## 4. Stay signed in and control device access

**Outcome:** users remain signed in across restarts and offline periods, while lost or revoked devices lose access promptly.

**Existing:** mandatory second-factor session issuance, hashed access credentials, 12-hour sessions, logout and live account-state checks. No refresh or OS credential storage.

- [ ] Introduce stable installation/device registration, short-lived access tokens and rotating refresh credentials stored hashed on the server.
- [ ] Define replay handling, family revocation, concurrent-worker serialization and interrupted rotation recovery without routine device authorization expiry.
- [ ] Integrate native secure credential storage on Linux, Windows and macOS; test locked, unavailable and lost stores without plaintext fallback. Obtain dependency approval first if needed.
- [ ] Persist credentials under server/account scope and verified trust only. Implement stable identity-key proof/CA validation and renewal safety needed before persisting credentials; feature 10 expands this into multiple profiles/routes.
- [ ] Add self-service device listing/revocation and session controls; enforce revocation on every request and cancel active streams within five seconds.
- [ ] Replace browser bearer-session handling with Secure/HttpOnly/SameSite cookies, CSRF/origin protections, browser expiry and sensitive-action reauthentication.
- [ ] Migrate or explicitly invalidate legacy sessions safely; retain mandatory onboarding and recovery-code enforcement.

**Accept when:** app/OS/server restarts, long inactivity, offline periods and a lost refresh response do not require routine login; concurrent refresh and replay tests pass; revocation blocks new work and active streams within the bound; forged browser mutations fail. Record native secure-store results on all three platforms.

## 5. Persistent, resumable uploads

**Outcome:** selected files survive application restarts in a durable queue and resume only missing work.

**Existing:** sequential in-memory transfer manager, recursive scans and a server missing-chunk route; database queue foundations are not connected to this workflow.

**Depends on:** features 3, 4. Design server/account scoping for feature 10 now.

- [ ] Persist jobs before network work; stream discovered files/directories into SQLite and resume interrupted scans without unbounded batch buffering.
- [ ] Persist upload IDs, source fingerprints, acknowledgements, retry schedules and explicit queue/wait states.
- [ ] Reconcile missing chunks after reconnect, lost acknowledgements and restart; never mix source generations when files change, disappear or become unreadable.
- [ ] Add bounded workers, fair scheduling, bandwidth controls and typed retry/backoff; one failing job/profile must not block other eligible work.
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

**Early prerequisite work:** run the format/library evaluation, bounded-object prototype, test vectors and initial small-file impact review before feature 3 freezes contracts. Obtain approvals for new dependencies or new crypto usage. Full integration depends on features 4, 5, 6, 8. Use the server/account scope established by feature 4; extend to simultaneous profiles when feature 10 arrives.

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

**Existing:** single active desktop connection, certificate-fingerprint approval and shared identity-proof contracts. Feature 2 establishes persistent single-profile trust.

**Depends on:** features 4, 5, 6. Add combined/per-server queue views here; feature 13 later completes their background lifecycle and UI qualification.

- [ ] Add/edit/remove persistent profiles containing scoped accounts, trusted identity, approved endpoints and preferences.
- [ ] Complete pairing and explicit identity/CA repair; validate signed challenges against the actual TLS connection, consume nonces and reject redirects/unapproved origins before sending credentials.
- [ ] Probe only configured routes with bounded timeouts/failure caching; retain upload identity when switching LAN/tailnet endpoints.
- [ ] Support simultaneous sign-in, combined/per-server queues and separate catalogs; an offline server must not block another.
- [ ] Document profile removal, credential deletion and local queue/data retention behavior.

**Accept when:** two servers with colliding usernames/file IDs remain isolated; route switches resume the same transfer; legitimate certificate renewal succeeds; replacement servers and relayed proofs receive no credentials.

## 11. Complete account administration

**Outcome:** administrators can manage account recovery and access through the browser without operator intervention for routine tasks.

**Existing:** user list/creation and backend password-reset, TOTP-reset and disablement services; complete runtime controls are missing.

**Depends on:** feature 4; retention controls integrate with feature 8 and capacity views with feature 7.

- [ ] Expose audited, role-checked password reset, distinct lost-TOTP recovery, account disable/enable and device/session revocation through API and UI.
- [ ] Require fresh administrator reauthentication for sensitive actions; enforce invalidation and preserve the distinction between password and TOTP recovery.
- [ ] Complete self-service recovery-code replacement and account/device screens.
- [ ] Restore show-once temporary-password delivery, or obtain an explicit product decision to revise the specification; migrate away from retained pending-password material if show-once is retained.
- [ ] Add paginated audit history, meaningful empty/error states and reset/recovery guidance; account recovery must never imply vault-key recovery.
- [ ] Calibrate Argon2id costs on target hardware and verify bounded concurrent authentication resource use.

**Accept when:** every administration action is available to authorized callers, denied to other users and audited; recovery/revocation invalidates sessions as specified; temporary-password visibility matches the documented policy and expiry behavior.

## 12. Supported deployment, backup and upgrades

**Outcome:** the server can be operated, upgraded and restored predictably on the actual Unraid host.

**Existing:** non-root Docker build, Compose examples, certificates, release workflow and deployment/update documentation. Feature 1 establishes an initial standard restore path.

**Depends on:** features 3, 4, 8 and 9; verify QUIC reachability after feature 2.

- [ ] Finish the versioned Unraid template and validate UID/GID, mounts, locking, resource guidance and pool/array/separate-staging layouts on actual hardware.
- [ ] Validate existing host Tailscale TCP/UDP reachability, private IPv4 CA provisioning, browser/per-profile trust and certificate renewal without public exposure.
- [ ] Complete consistent backup tooling using a maintenance barrier or reference snapshot with GC suspension; include identity, keys, envelopes and referenced blobs.
- [ ] Implement isolated restore checks, replacement-versus-clone rules and identity migration; extend restore drills to standard and recovery-key private downloads.
- [ ] Add pre-upgrade backups, migration compatibility checks and documented downgrade restore. Record tested client/server version compatibility and avoid overwriting release tags.

**Accept when:** an actual Unraid deployment survives restart/update and restores from backup onto an isolated instance with both storage modes intact; tested TCP/UDP, filesystem and permission outcomes are recorded. A workflow file or instructions alone do not close this feature.

## 13. Background desktop operation

**Outcome:** backups continue while the app is in the tray, with clear controls and predictable lifecycle behavior.

**Existing:** transfer screen, themes and throughput chart; no qualified background lifecycle.

**Depends on:** features 4, 5, 6.

- [ ] Add tray close behavior, explicit quit with durable checkpoints, optional launch at login and sleep/wake recovery.
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
- [ ] Run a real non-sparse >100-GiB file and at least 100,000 small files, concurrent clients, process kills, network failures, full disks, corruption and revocation in standard/private modes.
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
