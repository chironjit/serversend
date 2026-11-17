# ServerSend

ServerSend is a desktop client and private file-storage server for LAN and Tailscale access. The planned server runs in Docker on Unraid. The desktop targets Linux first, then Windows, with macOS support also required.

The end-to-end test slice supports TLS, persistent server identity/accounts/metadata, browser administrator onboarding and user creation, and desktop user sign-in, file and recursive folder uploads, server folders, verified downloads and live throughput. Administrators manage accounts at `/admin/`; file transfers require a user account. See [local testing](docs/local-testing.md), [Unraid Docker setup](docs/unraid-testing.md), [versioned GHCR releases](docs/releases.md), and [current limits](docs/vertical-slice.md). This remains a development build with sequential transfers and no persistent desktop queue or resume.

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

## Deploying on docker
### Settings
| Header | Setting |
| Name | ServerSend |
| Repository | ghcr.io/chironjit/serversend:latest |
| WebUI | https://[IP]:[PORT:58443]/admin/ |
| Extra Params | --read-only --cap-drop=ALL --security-opt=no-new-privileges --stop-timeout=15 |

### Ports
| Name | Container Port | Host Port |
| Port 1 | 58443 | 58443 | 

### Paths
| Name | Container Path | Host Path |
| Config | /config | /mnt/user/appdata/serversend/config/ |
| Data | /data | /mnt/user/serversend/data/ |
| Staging | /staging | /mnt/user/serversend/staging |

### Variables
| Name | Key | Value
| SQLITE Temp Dir | SQLITE_TMPDIR | /staging |
| Server IP Addr | SERVER_SEND_TLS_IPS | <server_addr> |

