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

An existing version greater than the supplied history returns an error wrapping `sqlite.ErrFutureSchema`. Negative schema markers are rejected. No downgrade is attempted. Backups, restore procedures and production upgrade coordination are separate application work.

## Verification

The tests use real temporary SQLite files. They cover connection settings, persistent contents after close/reopen, foreign keys and not-null constraints, missing parents, literal special-character filenames, invalid paths, corruption, Unix read-only files, cancelled contexts, concurrent handles, concurrent migrations, version guards, invalid histories, failed-batch rollback and retry after failure.

Run `bash shared/scripts/check.sh` for formatting, vet and module tests. The approved module must already be available in the local module cache; checks do not download dependencies or edit manifests. Run `go test -mod=readonly -race ./...` from `shared` for race checks on a supported native platform. Linux tests do not replace Windows/macOS packaging, credential-storage or filesystem qualification.
