# Changelog

## Unreleased

### Usability iteration

- Persistent admin user list with encrypted pending passwords until replacement or one-hour expiry; refreshes every five seconds. Passwords issued before this upgrade cannot be recovered from their hashes.
- Clickable desktop folder names and breadcrumb navigation; one Upload menu for native file and folder pickers.
- Confirmed permanent file and recursive folder deletion, scoped to the signed-in user. Active uploads block deletion; stored-object cleanup survives restarts.
- Bounded, independently scrollable transfer queue and file list.
- Vue-native workspace layouts, compact navigation, violet accents, and light/dark themes inspired by the Artifact template; no new dependencies.

### Build and release tooling

- Independent server and desktop checks and component-prefixed version tags.
- Portable desktop formatter invocation on Windows.
- Removed the unsupported npm minimum-release-age setting (npm did not enforce it).

### Server and desktop initial feature baseline

First planned version: **0.1.0** (experimental network-testing release).

- Browser administrator onboarding and user creation, separated from desktop file transfers.
- Recursive folder uploads, server folders, verified downloads and transfer speed chart.
- Persistent Docker configuration, accounts, certificates and storage; TCP port 58443.
- Browser-compatible ECDSA TLS certificates and explicit migration for older development certificates.
- Versioned GHCR server releases, OCI version/commit labels and Unraid update channels.

Known limits: sequential transfers, no resume or persistent desktop queue, at most 1000 catalog entries per directory, and no desktop installer publishing in the server release workflow.
