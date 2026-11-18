# SQLite foundation

`shared/sqlite` supplies the connection and migration policy for both the server metadata database and desktop queue database. It uses `database/sql` and the approved `github.com/mattn/go-sqlite3` driver. Product tables and migrations remain in their owning application.

## Opening databases

Call `sqlite.Open(ctx, absoluteFilename)` and close the returned `*sql.DB` when finished. The path must be an absolute filesystem filename, not a DSN, URI, relative path or in-memory database. The parent directory must already exist; the package never creates it. Filenames are URL-escaped so characters such as `?`, `#`, `%` and `&` cannot supply SQLite connection parameters on platforms permitting those characters.

New database files are created with mode 0600, subject to the operating system's permission semantics. Existing permissions are not changed. The application owns protection of the parent directory, Windows ACL configuration, mount validation and local-filesystem selection. This helper is not a symlink sandbox and cannot prove that a filesystem provides safe locking or durable writes. A failed initialization can leave an empty new database file; cleanup must not remove a file another opener may own.

Each connection uses WAL journaling, FULL synchronous mode, foreign-key enforcement, a five-second busy timeout and a suggested 2 MiB SQLite page cache. The `database/sql` pool permits one open connection and one idle connection. Cache size is a database cache target, not a hard cap on total process memory. The DSN carries these settings so replacement connections receive the same configuration. New transactions begin immediately, acquiring the writer reservation before reading migration state.

`Open` checks a pre-cancelled context before filesystem work and pings the database to discover initialization errors before returning it. Busy locks may delay initialization or transaction acquisition up to the driver's busy timeout; context support does not promise instantaneous cancellation of every native operation. Multiple handles may access the same local database, but callers must handle timeout/busy errors under prolonged contention. This is not support for multiple server processes sharing a deployment.

The driver uses CGO and requires a C compiler for builds. Its DSN options are documented in the [go-sqlite3 README](https://github.com/mattn/go-sqlite3). No separate system SQLite installation is required for the driver's normal bundled build.

## Applying migrations

Supply the complete ordered application history to `sqlite.Migrate(ctx, db, migrations)` on a database opened by `sqlite.Open`:

```go
migrations := []sqlite.Migration{
    {Version: 1, SQL: "CREATE TABLE example (id INTEGER PRIMARY KEY)"},
    {Version: 2, SQL: "ALTER TABLE example ADD COLUMN label TEXT"},
}
err := sqlite.Migrate(ctx, db, migrations)
```

Versions must start at 1 and be consecutive, with nonempty SQL. Invalid histories are rejected before database mutation. Migration SQL is trusted application code: it must not include transaction control, alter `PRAGMA user_version`, attach databases or change connection settings. It must not be supplied by users. Existing migration text must remain immutable; this initial helper tracks only the version marker, not historical SQL checksums.

The helper begins one immediate transaction, reads `PRAGMA user_version`, and applies all pending migrations in that transaction. Schema changes, data changes and the version marker commit together; any failure rolls the entire pending batch back. The version is read after acquiring the writer reservation, so concurrent migrators using this connection policy serialize rather than both applying the same migration. Calling again with the same history is safe.

An existing version greater than the supplied history returns an error wrapping `sqlite.ErrFutureSchema`. Negative schema markers are rejected. No downgrade is attempted. The metadata snapshot primitive is described below. Full restore procedures, automatic pre-upgrade backups and production upgrade coordination remain Chunk 26 work.

## Application schemas and repositories

The server now opens schema **12** in `/config/serversend.sqlite`. Version 1 remains the shipped singleton identity schema; version 2 adds accounts, uploads, reservations and committed-chunk metadata. Identity material survives the upgrade unchanged. An independent frozen `apps/server/internal/database/testdata/v1.sql` fixture tests compatibility with the shipped schema rather than rebuilding the prior database from current migration code. The frozen `testdata/v2.sql` fixture also preserves existing accounts, uploads and reservations through upgrade. Version 3 adds temporary-credential expiry, credential revisions, revocation epochs, persistent failed-attempt backoff, hashed restricted challenges and audit events. A version-0 empty database is also supported. The pending migration batch rolls back on failure; errors do not authorize schema repair or identity replacement.

Versions 4–6 add protected two-factor secrets, device sessions, immutable file versions and directories. Version 7 adds an encrypted pending-password envelope and a durable deleted-object cleanup queue. Pending passwords use AES-GCM with a separate authenticated purpose under the existing protection key; permanent passwords remain hash-only. Password replacement clears the envelope in the same transaction. Listing users clears expired envelopes. Deleted catalog trees and their versions are removed atomically, and object cleanup is retried on startup. Version 8 adds object-reference and upload-expiry indexes for bounded startup reconciliation and maintenance. Frozen fixtures cover every prior schema through version 7.

The desktop opens schema **2** through `apps/desktop/internal/database.Open(ctx, absoluteDirectory)` in `desktop.sqlite`. It requires an existing private application directory. Tables hold paired profiles and public trust pins, approved endpoints, jobs, file tasks, source fingerprints, chunk acknowledgements and catalog revision checkpoints. Every remote key and foreign key includes server ID and account ID. Catalog revisions use canonical decimal text to preserve the protocol's full unsigned 64-bit range. No passwords, refresh credentials or vault keys belong in this database. Local source paths are local queue metadata; encrypted remote metadata caches are added by Chunk 20. Migration 2 preserves the v1 foundation and adds operational transfer batches/tasks/preferences. Task payloads contain pinned file/version metadata, source fingerprints, upload identities/acknowledgements and scanner cursors; parent ownership scopes each task to a server/account. Active work is requeued at startup under the native queue lock; pause/cancel and terminal decisions remain intact.

New tables use SQLite STRICT typing plus constraints for canonical IDs, enums, lengths, nonnegative counts and ownership. The original v1 identity table remains unchanged. Composite foreign keys prevent a chunk/reservation/task from attaching to another owner's resource. File bytes remain outside SQLite. Later chunks add their own migrations for TOTP, sessions, full catalogs, vault envelopes and additional lifecycle-specific fields as those implementations need them; these initial tables do not implement those workflows.

Both application packages use concrete `Repository` receivers created with `NewRepository(db)`. Their private database handle and `write` callback make transaction ownership explicit; callbacks use only their supplied transaction. No repository interfaces have been invented ahead of a consumer:

- Server `CreateAccount` inserts only the initial password-required, unenrolled state with an opaque encoded password hash. `GetAccount` excludes hashes and secret material. The new authentication repository adds expiring temporary credentials, first-admin issuance/local recovery, scoped challenge creation/consumption, live administrator checks, backoff and audited reset/disable transactions. The auth service owns password verification/policy and recent reauthentication; production hashing and executable integration still await dependency approval.
- Server `CreateUpload` inserts the upload and reservation together; `GetUpload` requires an owner and returns `sql.ErrNoRows` for absent/foreign IDs. The standard-transfer service uses `CreateTransfer` with transactional capacity checks, idempotent replay and full-upload reservations; chunk acknowledgement and finalization publish durable object references and versions. The older foundation adapter remains test scaffolding.
- Desktop `CreateProfile` inserts a previously verified pin and approved HTTPS origins atomically. A duplicate cannot overwrite an existing pin; pairing and explicit trust repair belong to Chunk 08. Profiles have a maximum of sixteen configured endpoints, independently of batch size.
- Desktop `CreateJob`, bounded keyset `ListJobs` (1–1000 rows), and `TransitionJob` provide persistence for later scheduling. Transitions validate the shared state machine and atomically compare the prior state. Missing/foreign/stale transitions return the same conflict error. Running jobs are persisted as running; Chunk 13 owns restart reconciliation before scheduling any work.

Times in these repositories are stored as Unix milliseconds and read as UTC. The desktop now references the existing shared workspace module and the already approved SQLite driver; its Go minimum matches shared's `1.26.3`. No new third-party selection or workspace module was added.

## Transactions and private paths

`sqlite.Write(ctx, db, callback)` commits a short immediate transaction only when its callback and context succeed. Errors and panics roll back, with the original error/panic preserved. Callbacks must use the supplied `*sql.Tx`, never the one-connection pool, and must not hold the writer lock during filesystem/network work. There is no automatic retry of callback side effects. Existing single-connection limits, five-second busy timeout, foreign keys and WAL/FULL policy apply equally to both applications; multiple local handles serialize writes through SQLite.

`sqlite.OpenPrivate` centralizes application directory/database checks and refuses symlinks at the database filename. Unix directories/files must be private (0700/0600). Existing access is never silently changed. Windows callers must provision user-only ACLs: POSIX mode bits are not evidence of Windows privacy. Parent paths must be trusted against local replacement races. Database directories must be on local storage with working SQLite locking, not SMB/NFS.

## Metadata snapshots

`sqlite.Backup(ctx, db, newAbsoluteFilename)` uses SQLite `VACUUM INTO` to capture a consistent standalone metadata database, including committed WAL data. It exclusively reserves the destination so an existing backup, source file or symlink is never overwritten; a failed call removes only its newly reserved destination. The destination must be in a trusted existing directory and must not be used until the call succeeds. It flushes the finished file and, on Unix, the containing directory. Windows directory flushing and restore semantics still require native qualification.

This primitive is tested by reopening the snapshot, checking integrity and verifying that later source commits do not appear in it. It is **not** a complete ServerSend backup: configuration keys, identity handling, referenced objects, garbage-collection suspension, maintenance coordination, retention and automatic pre-migration backup remain Chunk 26. Until that workflow exists, stop the process and preserve the complete deployment before upgrading; retain the pre-upgrade backup for a downgrade. Never copy only a live main SQLite file.

## Deterministic failure fixtures

`shared/testutil` supplies a mutex-protected manual clock (`Now`/`Advance`), seeded `io.Reader` random source, named one-shot faults, real-file wrappers for open/read/write/sync/close/rename/remove failures, a partial-write byte budget, and `net.Conn` wrappers that interrupt reads/writes after independent byte budgets. A negative connection budget disables that direction's injection. Closing/reconnecting remains the test's responsibility. The clock does not simulate timers; finite entropy failures use the standard library's `bytes.Reader`.

These adapters are test-only choices, especially the noncryptographic random source. They do not alter production randomness or add retry policy. Tests use real temporary files and `net.Pipe`, so failures do not depend on physical disk exhaustion or wall-clock sleeps. A reservation-trigger failure also proves the repository's two-statement write rolls back, and a two-handle state race proves only one job transition wins.

## Verification

The tests use real temporary SQLite files. They cover connection settings, persistent contents after close/reopen, foreign keys and not-null constraints, missing parents, literal special-character filenames, invalid paths, corruption, Unix read-only files, cancelled contexts, concurrent handles, concurrent migrations, version guards, invalid histories, failed-batch rollback and retry after failure.

Run `bash shared/scripts/check.sh` for formatting, vet and module tests. The approved module must already be available in the local module cache; checks do not download dependencies or edit manifests. Run `go test -mod=readonly -race ./...` from `shared` for race checks on a supported native platform. Linux tests do not replace Windows/macOS packaging, credential-storage or filesystem qualification.


## Chunk 03 platform qualification

Run `make check-persistence` natively on each supported target. It requires CGO and a real C compiler, runs vet/race tests against shared persistence/fault fixtures and both application database packages, and explicitly links their test binaries. It refuses non-CGO stubs and foreign targets. It uses offline, read-only module settings. The activated CI workflow at `.github/workflows/check.yml` runs this command on Linux, Windows and macOS; the platform qualification gate closes when those hosted runs pass.

| Platform | Recorded result |
| --- | --- |
| Linux amd64 | Passed locally on 2026-11-16: native CGO builds, vet, race tests, migrations, constraints, ownership isolation, concurrent writes, rollback and restart persistence. Full `make check` also passed, including frontend/native desktop build, regenerated bindings and live smoke checks. Loopback tests required sandbox permission. |
| Windows | Not run: target C compiler/runtime unavailable on this Linux host. Native build/test and user-only ACL checks remain acceptance gates. |
| macOS | Not run: target SDK/compiler/runtime unavailable on this Linux host. Native build/test and filesystem checks remain acceptance gates. |

All Chunk 03 implementation checkboxes are addressed; the chunk is not marked fully accepted until the Windows/macOS driver qualification passes. A Linux build or a non-CGO cross build is not evidence for those platforms. The CI workflow is active; hosted qualification results remain unrecorded.

Schema 12 adds the standard version/trash retention lifecycle; see [lifecycle and migration semantics](protocol-transfers.md#standard-versions-trash-and-retention). Frozen schema 11 and real SQLite/filesystem lifecycle tests cover upgrade preservation and restart replay.
