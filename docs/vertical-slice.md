# End-to-end vertical slice

This slice exists so the server and desktop can be exercised together early. It was pulled ahead of the chunk order on 2026-11-16 at the user's request. It follows the published v1 routes and contracts, so it can grow into the full implementation, but it deliberately omits most of each chunk's acceptance scope. Nothing here closes a chunk.

## What works

Run a server, bootstrap an administrator, then in the desktop app: approve the server's certificate, replace the temporary password, enroll an authenticator, sign in, upload files of any size, see them listed, and download them again with every chunk verified.

## Try it

Run `make local-test-server-setup` in one terminal and `make local-test-desktop` in another. [Local end-to-end testing](local-testing.md) walks through every step, repeat runs, resetting and troubleshooting.

Uploaded bytes are stored under `data/objects`; metadata is in `config/serversend.sqlite`.

## Deliberate limits

| Area | Slice behaviour | Completed by |
| --- | --- | --- |
| Staying signed in | The session lives in desktop memory for 12 hours with no refresh. Restarting the app means signing in again with a new code. Nothing is written to disk because the OS credential store is not integrated and plaintext storage is not acceptable. | 07, 08 |
| Server trust | The user approves the certificate's SHA-256 fingerprint and the app pins exactly that certificate. When the private certificate renews the fingerprint changes and must be approved again. The signed identity proof and key pin are not used yet. | 08 |
| Accounts | Only the bootstrap administrator can be created. Creating users, resets and disablement are implemented in the service but have no caller. No device list. | 07, 22 |
| Uploads | One file at a time, each as a new file with a new ID; no folders, no replacing an existing file, no queue persistence, no resume after an interruption, no cancel. The file is read twice (describe, then send). | 10, 12–14 |
| Storage | Chunks are verified, flushed and renamed into place before acknowledgement. Capacity checks, cross-filesystem staging, orphan reconciliation after a crash and cleanup of abandoned or expired uploads are not implemented, so interrupted uploads leave objects behind. | 11, 23 |
| Catalog | One flat page of at most 1000 files; no paging, directories, trash, change feed or events. | 10, 21 |
| Downloads | Whole file to a chosen path via a temporary file and rename; an existing file at that path is replaced after the save dialog's own confirmation. No resume. | 15 |
| Private mode | Rejected with `unsupported_format`. | 17–20 |
| Desktop UI | A single screen with plain CSS and a persisted Light/Dark/System choice. Tailwind and the accessible component styling planned for the desktop are not applied; the scaffold `HelloWorld` component and greeting service remain only because the existing smoke check uses them. | 09, 16 |
| Request timeouts | The server's 30-second read/write timeouts apply to each chunk, so links slower than roughly 2 Mbit/s can fail on 4 MiB chunks. | 12, 24 |

Schema version 5 (`005_sessions_transfers.sql`) was added for the slice. The manifest and chunk API are still not frozen: the format decision (Chunk 17) and the small-file overhead benchmark come first and may change these tables and routes.

## Verification

`apps/server/internal/server/transfer_test.go` runs the real server over TLS with production Argon2id and the real `shared/client`: fingerprint probe, rejection of an unapproved certificate before any request is sent, full onboarding, uploads of empty, small, exactly-one-chunk and multi-chunk files, byte-for-byte restores, on-disk object accounting, cross-account isolation, sign-out and revocation when an account is disabled. `apps/desktop/frontend/src/App.test.ts` covers the screen flow, failures, live progress and theme persistence against mocked bindings. The desktop Go manager and the native dialogs have no automated test; the assembled application was started but not clicked through.
