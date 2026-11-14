# ServerSend

ServerSend is a desktop client and private file-storage server for LAN and Tailscale access. The planned server runs in Docker on Unraid. The desktop targets Linux first, then Windows, with macOS support also required.

Implementation is at the foundation stage. The desktop is still a Wails/Vue scaffold; the server does not yet serve files or authenticate users. See the [implementation plan](docs/implementation-plan.md) and [task breakdown](docs/implementation-tasks.md) for scope and dependencies.

## Repository layout

| Path | Responsibility |
| --- | --- |
| `apps/desktop` | Desktop application, frontend and packaging |
| `apps/server` | Server implementation, admin UI and deployment tooling |
| `shared` | Reusable code, protocol contracts and tests |
| `docs` | Project plans and development documentation |

Read [AGENTS.md](AGENTS.md) before adding modules or dependencies. Keep the existing Go module paths and workspace layout.

## Development checks

Use the Go version declared in `go.work`, and the desktop prerequisites described in [desktop development](docs/desktop-development.md). From the repository root:

```sh
make check
```

Checks can also run individually:

```sh
make check-shared
make check-server
make check-desktop
```

Each target delegates to the owning component's `scripts/check.sh`, which can also be invoked with Bash when Make is unavailable. Checks do not install dependencies. A missing desktop build prerequisite is a failed check, not a successful validation of the application.

See [server development](docs/server-development.md) for configuration validation, [protocol foundation](docs/protocol-foundation.md) for shared contracts, [SQLite foundation](docs/sqlite-foundation.md) for database opening/migrations, and [TOTP foundation](docs/twofactor-foundation.md) for enrollment and verification helpers. These foundations do not yet constitute a deployable server.
