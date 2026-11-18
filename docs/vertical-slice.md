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
| Uploads | SQLite-persisted selections and bounded scans; eight workers, per-file checkpoints/missing-chunk recovery, empty folders and same-name replacement. Pause/resume/cancel/retry and persisted bandwidth limits survive restart. Durable workers use per-file requests; small-file batching remains an API/legacy path. | 10, 12–14 |
| Storage | Chunks are verified and flushed in place before acknowledgement; files are published by journaled rename. Cross-filesystem staging is not implemented. | 11, 23 |
| Catalog | Folder navigation/creation and 100-entry signed cursor pages. Catalog mutation/expiry/restart invalidates continuation and triggers full refresh. Unchanged revisions return an empty change interval; no retained delta history, user trash or catalog mutation event stream yet. Periodic statistics events and account/server storage dashboards are available. | 10, 21 |
| Downloads | Persisted file/folder selections and verified chunk checkpoints. Folder restores choose fail/skip/replace for existing files; directories merge. Single-file Save retains its explicit replacement confirmation. Unsafe names, aliases, links and path escapes fail explicitly. Completed publication is reconciled after lost local acknowledgement. | 15 |
| Private mode | Rejected with `unsupported_format`. | 17–20 |
| Desktop UI | Tailwind components, persisted Light/Dark/System and bounded recent selection/skip summaries; durable queue controls are available; native lifecycle qualification remains. The scaffold `HelloWorld` component and greeting service remain because the existing smoke check uses them. | 09, 16 |
| Request timeouts | The server's 30-second read/write timeouts apply to each chunk, so links slower than roughly 2 Mbit/s can fail on 4 MiB chunks. | 12, 24 |

Schema version 6 (`006_directories.sql`) adds persistent folders and upload destinations without changing existing file ownership. Existing admin-owned files are retained but unavailable through user-only transfer routes. The manifest and chunk API are still not frozen: the format decision (Chunk 17) and the small-file overhead benchmark come first and may change these tables and routes.

## Verification

`apps/server/internal/server/transfer_test.go` runs the real server over TLS with production Argon2id and the real `shared/client`: fingerprint probe, rejection of an unapproved certificate before any request is sent, full onboarding, uploads of empty, small, exactly-one-chunk and multi-chunk files, byte-for-byte restores, on-disk object accounting, cross-account isolation, sign-out and revocation when an account is disabled. `apps/desktop/frontend/src/App.test.ts` covers the screen flow, failures, live progress, themes and skipped-item summaries against mocked bindings. Desktop manager/source tests cover mixed scans, hierarchy/empty folders, selected links, worker rechecks, policy skips and real failures. Native dialog walkthroughs remain unqualified.

The desktop displays acknowledged upload and verified download throughput with a rolling 60-second chart. Folder scans apply backpressure to the bounded transfer queue and retain completed work after errors. Existing folders merge by name. The server now has deployment instance locks, an initial [offline backup](backup-restore.md) and HTTP/3-first connections with HTTP/2 fallback; [local measurements](performance-baseline.md) record the performance baseline. Docker mappings and image-update instructions are in [Unraid testing](unraid-testing.md).

## Link-handling implementation and remaining gates

The implementation of [feature 1](implementation-tasks.md#1-graceful-cross-platform-symlink-handling) applies the approved skip-and-report policy to recursive scans, selected roots and direct files. Batch reports keep exact counts and at most 100 skipped paths/reasons, with distinct skipped-completion and all-skipped states. Genuine I/O, cancellation and server failures remain failures. No-follow source handles guard against link replacement; ordinary hard links are ordinary files. Link-bearing ancestors are refused, including macOS `/tmp` and `/var` aliases. The filesystem-library use is explicitly approved; Linux runtime/race and UI checks pass. Native picker resolution, Windows reparse/cloud fixtures and native Windows/macOS execution remain gates, so this is not full feature acceptance.

Download checkpoints use scoped/version-bound `.serversend-*.partial` files beside the destination. Resume verifies and copies the contiguous prefix into a fresh exclusive file, temporarily requiring space for both prefixes. Explicit cancellation removes its own partial and preserves any already published destination. Abandoned checkpoints and crash-left `.serversend-*.tmp` files still require manual cleanup. Unsafe remote names fail visibly rather than being silently renamed; a changed path/version encountered during a discovery rescan also fails visibly, preserving completed work. Native Windows/macOS and actual filesystem/power-loss qualification remain open.
