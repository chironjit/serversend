# Changelog

## Unreleased

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
