# End-to-end vertical slice

This slice exists so the server and desktop can be exercised together early. It was pulled ahead of the chunk order on 2026-11-16 at the user's request. It follows the published v1 routes and contracts, so it can grow into the full implementation, but it deliberately omits most of each chunk's acceptance scope. Nothing here closes a chunk.

## What works

Run a server, bootstrap an administrator, onboard the administrator in `/admin/` and create a user, then in the desktop app as that user: approve the server's certificate, replace the temporary password, enroll an authenticator, sign in, upload files of any size, see them listed, and download them again with every chunk verified.

## Try it

Run `make local-test-server-setup` in one terminal and `make local-test-desktop` in another. [Local end-to-end testing](local-testing.md) walks through every step, repeat runs, resetting and troubleshooting.

Uploaded files are browsable under `data/<username>`; metadata is in `config/serversend.sqlite`.

## Deliberate limits

| Area | Slice behaviour | Completed by |
| --- | --- | --- |
| Staying signed in | Rotating desktop credentials survive restart in the OS credential store when Remember sign-in is enabled; ten-minute access tokens stay in memory. Full login after 30 days without refresh; TOTP every 60 days of ongoing use. Browser admin cookies last seven days. | 07, 08 |
| Server trust | Private CA import or OS roots validate TLS; signed channel-bound proofs pin the stable server identity on every connection. Leaf renewal is silent; CA/identity replacement needs explicit repair. | 08 |
| Accounts | Browser admin onboarding and user creation are available. Admins cannot use file routes. User creation requires sign-in within five minutes. Device history/revocation and separate password/TOTP resets are exposed; mixed pending resets are blocked. Disablement still has no UI. | 07, 22 |
| Uploads | Eight workers with small-file batching; recursive folders including empty folders, selected destinations and same-name replacement. Replay-safe network operations retry briefly with stable identities; no queue persistence or upload resume after app exit. Individual/batch cancellation is available. The file is read twice (describe, then send). | 10, 12–14 |
| Storage | Chunks are verified and flushed in place before acknowledgement; files are published by journaled rename. Cross-filesystem staging is not implemented. | 11, 23 |
| Catalog | Folder navigation and creation; at most 1000 entries per folder; no paging, trash, change feed or events. | 10, 21 |
| Downloads | Verified chunks to a temporary file and rename; an existing file is replaced after the save dialog's confirmation. Brief interruptions retry automatically. Selecting the same version and destination again resumes verified local chunks; folder restores and persisted selections remain open. | 15 |
| Private mode | Rejected with `unsupported_format`. | 17–20 |
| Desktop UI | Tailwind components, persisted Light/Dark/System and bounded recent selection/skip summaries; complete queue controls and native lifecycle qualification remain. The scaffold `HelloWorld` component and greeting service remain because the existing smoke check uses them. | 09, 16 |
| Request timeouts | The server's 30-second read/write timeouts apply to each chunk, so links slower than roughly 2 Mbit/s can fail on 4 MiB chunks. | 12, 24 |

Schema version 6 (`006_directories.sql`) adds persistent folders and upload destinations without changing existing file ownership. Existing admin-owned files are retained but unavailable through user-only transfer routes. The manifest and chunk API are still not frozen: the format decision (Chunk 17) and the small-file overhead benchmark come first and may change these tables and routes.

## Verification

`apps/server/internal/server/transfer_test.go` runs the real server over TLS with production Argon2id and the real `shared/client`: fingerprint probe, rejection of an unapproved certificate before any request is sent, full onboarding, uploads of empty, small, exactly-one-chunk and multi-chunk files, byte-for-byte restores, on-disk object accounting, cross-account isolation, sign-out and revocation when an account is disabled. `apps/desktop/frontend/src/App.test.ts` covers the screen flow, failures, live progress, themes and skipped-item summaries against mocked bindings. Desktop manager/source tests cover mixed scans, hierarchy/empty folders, selected links, worker rechecks, policy skips and real failures. Native dialog walkthroughs remain unqualified.

The desktop displays acknowledged upload and verified download throughput with a rolling 60-second chart. Folder scans apply backpressure to the bounded transfer queue and retain completed work after errors. Existing folders merge by name. The server now has deployment instance locks, an initial [offline backup](backup-restore.md) and HTTP/3-first connections with HTTP/2 fallback; [local measurements](performance-baseline.md) record the performance baseline. Docker mappings and image-update instructions are in [Unraid testing](unraid-testing.md).

## Link-handling implementation and remaining gates

The implementation of [feature 1](implementation-tasks.md#1-graceful-cross-platform-symlink-handling) applies the approved skip-and-report policy to recursive scans, selected roots and direct files. Batch reports keep exact counts and at most 100 skipped paths/reasons, with distinct skipped-completion and all-skipped states. Genuine I/O, cancellation and server failures remain failures. No-follow source handles guard against link replacement; ordinary hard links are ordinary files. Link-bearing ancestors are refused, including macOS `/tmp` and `/var` aliases. The filesystem-library use is explicitly approved; Linux runtime/race and UI checks pass. Native picker resolution, Windows reparse/cloud fixtures and native Windows/macOS execution remain gates, so this is not full feature acceptance.

Single-file download checkpoints use scoped, version-bound `.serversend-*.partial` files in the destination directory. Each chunk is verified and flushed before progress advances; a retry rechecks and copies the valid prefix into a fresh exclusive temporary file before continuing. This needs local space for both the previous prefix and its replacement during resume. Cancelled/failed downloads retain checkpoints; abandoned checkpoints and crash-left `.serversend-*.tmp` files currently require manual cleanup. Full durable queue controls, collision policies, native filesystem/power-loss qualification and recursive restore safety remain roadmap work.
