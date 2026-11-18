# ServerSend

ServerSend is a desktop client and private file-storage server for LAN and Tailscale access. The planned server runs in Docker on Unraid. The desktop targets Linux first, then Windows, with macOS support also required.

The end-to-end test slice supports TLS, persistent server identity/accounts/metadata, browser administrator onboarding and user creation, and desktop user sign-in, file and recursive folder uploads, server folders, verified downloads and live throughput. Administrators manage accounts at `/admin/`; file transfers require a user account. See [local testing](docs/local-testing.md), [Unraid Docker setup](docs/unraid-testing.md), [versioned GHCR releases](docs/releases.md), and [current limits](docs/vertical-slice.md). This remains a development build with persistent resumable uploads and file/folder restores, paginated browsing and durable queue controls. Native platform and deployment qualification remain open.

Experimental [private encrypted storage and recovery](docs/private-storage.md) is offered when a server is first opened in the desktop library. Private uploads currently require files below the 16 GiB ciphertext staging limit (larger files can be skipped or uploaded unencrypted); independent crypto review and native/deployment qualification remain open.

The next implementation priorities are tracked in the [feature roadmap](docs/implementation-tasks.md), with the full product specification in the [implementation plan](docs/implementation-plan.md).

The first roadmap delivery adds skip-and-report source handling and desktop summaries, deployment instance locks, [offline backup/restore](docs/backup-restore.md), and [HTTP/3 over QUIC with HTTP/2 fallback](docs/performance-baseline.md). Linux source/runtime checks pass; native Windows/macOS and deployment qualification remain open in the [verification map](docs/requirements-verification.md#feature-roadmap-execution-2026-11-17).

## Repository layout

| Path | Responsibility |
| --- | --- |
| `apps/desktop` | Desktop application, frontend and packaging |
| `apps/server` | Server implementation, admin UI and deployment tooling |
| `shared` | Reusable code, protocol contracts and tests |
| `docs` | Project plans and development documentation |

Read [AGENTS.md](AGENTS.md) before adding modules or dependencies. Keep the existing Go module paths and workspace layout.

## Development checks

Use the Go version declared in `go.work`, and the desktop prerequisites described in [desktop development](docs/desktop-development.md). Restore frontend dependencies with `npm ci` in both `apps/desktop/frontend` and `apps/server/admin`. From the repository root:

```sh
make check
```

Release both components from a clean, pushed default branch with `make release` (preview with `make release DRY_RUN=1`; choose a version with `VERSION=0.2.0`). See [release instructions](docs/releases.md) for desktop downloads and server updates.

Checks can also run individually:

```sh
make check-shared
make check-server
make check-admin
make check-desktop
make check-persistence
```

Each target delegates to the owning component's `scripts/check.sh`, which can also be invoked with Bash when Make is unavailable. Checks do not install dependencies. A missing desktop build prerequisite is a failed check, not a successful validation of the application.

See [server development](docs/server-development.md) to run the process or container, [protocol foundation](docs/protocol-foundation.md) for completed Chunk 02 contracts, [SQLite foundation](docs/sqlite-foundation.md) for schemas, scoped repositories, snapshots, fault fixtures and platform qualification, and [TOTP foundation](docs/twofactor-foundation.md) for enrollment and verification helpers. [Requirement verification](docs/requirements-verification.md) distinguishes existing checks from planned acceptance coverage. To run the server and desktop together and upload a file, run `make local-test-server-setup` and `make local-test-desktop` and follow [local end-to-end testing](docs/local-testing.md); the [vertical slice](docs/vertical-slice.md) page lists what is implemented. It is an early end-to-end path with deliberate limits, not yet a usable file-storage product.

## Deploying on Docker (Unraid)

Full details: [Unraid setup](docs/unraid-testing.md) and [releases](docs/releases.md).

### 1. Prepare host folders (Unraid terminal)

```sh
mkdir -p /mnt/user/appdata/serversend/config /mnt/user/serversend/data /mnt/user/serversend/staging /mnt/user/serversend-backups
chown 10001:10001 /mnt/user/appdata/serversend/config /mnt/user/serversend/data /mnt/user/serversend/staging /mnt/user/serversend-backups
chmod 700 /mnt/user/appdata/serversend/config /mnt/user/serversend/data /mnt/user/serversend/staging /mnt/user/serversend-backups
```

Keep config on a local pool (not SMB/NFS). The container runs as UID/GID 10001.

### 2. Add the container (Docker tab → Add Container, Advanced View)

Or copy the versioned template `apps/server/deploy/unraid/serversend.xml` to `/boot/config/plugins/dockerMan/templates-user/my-serversend.xml` and pick it under **Add Container**; it contains the settings below.

| Field | Value |
| --- | --- |
| Name | `serversend` |
| Repository | `ghcr.io/chironjit/serversend:latest` (or `:0.4` to stay on the 0.4 series) |
| Network Type | Bridge |
| WebUI | `https://[IP]:[PORT:58443]/admin/` |
| Extra Parameters | `--read-only --cap-drop=ALL --security-opt=no-new-privileges --stop-timeout=15` |
| Privileged | Off |

| Port | Container | Host | Protocol |
| --- | --- | --- | --- |
| HTTPS (HTTP/2) | 58443 | 58443 | TCP |
| HTTP/3 (QUIC) | 58443 | 58443 | UDP |

| Path | Container | Host | Access |
| --- | --- | --- | --- |
| Config | `/config` | `/mnt/user/appdata/serversend/config` | Read/Write |
| Data | `/data` | `/mnt/user/serversend/data` | Read/Write |
| Staging | `/staging` | `/mnt/user/serversend/staging` | Read/Write |
| Backups | `/backups` | `/mnt/user/serversend-backups` | Read/Write |

| Variable | Key | Value |
| --- | --- | --- |
| Server IPs | `SERVER_SEND_TLS_IPS` | Unraid LAN IPv4, e.g. `192.168.1.10`; add the Tailscale IP comma-separated if used (e.g `192.168.1.10,172.100.1.10`) |
| Upgrade backups | `SERVER_SEND_UPGRADE_BACKUP_DIR` | `/backups` (or `none` if you take your own stopped-server backups) |
| User quota (optional) | `SERVER_SEND_STORAGE_USER_BYTES` | Bytes per account; `0` = unlimited (default) |
| Total quota (optional) | `SERVER_SEND_STORAGE_GLOBAL_BYTES` | Bytes for all accounts; `0` = unlimited (default) |
| Free-space floor (optional) | `SERVER_SEND_STORAGE_MIN_FREE_BYTES` | Default `67108864` (64 MiB) |

`SQLITE_TMPDIR=/staging` is set by the image; add it manually only if you changed the staging container path. UDP is optional: clients fall back to HTTP/2 over TCP.

### 3. Create the first administrator (once)

```sh
docker stop serversend
docker run --rm --network none --read-only \
  -e SERVER_SEND_TLS_IPS=192.168.1.10 \
  -v /mnt/user/appdata/serversend/config:/config \
  -v /mnt/user/serversend/data:/data \
  -v /mnt/user/serversend/staging:/staging \
  ghcr.io/chironjit/serversend:latest -bootstrap-admin admin
docker start serversend
```

Save the printed temporary password (valid one hour), open `https://UNRAID_IP:58443/admin/`, accept the private certificate after checking it is your server, then set a password, enrol TOTP and keep the recovery codes. Create user accounts there; in the desktop app connect to `UNRAID_IP` and compare the fingerprint with `docker logs serversend`.

### Updating

Use **Check for Updates → Apply Update**; keep paths and variables, and do not bootstrap again. When an update migrates the database, the server first writes a full backup into `/backups`; without `SERVER_SEND_UPGRADE_BACKUP_DIR` it refuses to migrate. See [backup/restore](docs/backup-restore.md) for restores and downgrades.

**Breaking change (per-user file tree):** files uploaded by older images are not migrated and the server refuses to start with them. Accounts survive, but all uploaded files are deleted by:

```sh
docker stop serversend
docker run --rm --network none --read-only \
  -e SERVER_SEND_TLS_IPS=192.168.1.10 \
  -v /mnt/user/appdata/serversend/config:/config \
  -v /mnt/user/serversend/data:/data \
  -v /mnt/user/serversend/staging:/staging \
  ghcr.io/chironjit/serversend:latest -reset-files
docker start serversend
```

Files are then browsable on the host at `/mnt/user/serversend/data/<username>/…`; leave `data/.serversend` alone. Keep the share unexported or read-only: a file changed there no longer matches its upload checksums, and downloads of it complete with a warning.
