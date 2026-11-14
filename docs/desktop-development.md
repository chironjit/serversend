# Desktop development

The desktop application lives in `apps/desktop`. It currently contains the Wails/Vue scaffold; transfer, account and vault features are not implemented yet. Keep reusable code in root `shared` and project documentation in root `docs`.

## Validation

Run from the repository root:

```bash
bash apps/desktop/scripts/check.sh
```

The script also works from another working directory when invoked by its full path. It checks Go formatting, runs the existing frontend type check and production build, runs Go vet and race-enabled tests, and compiles the native production application into a temporary directory. It reports missing prerequisites as failures and does not silently skip checks. It does not install packages, download Go modules or toolchains, or run `go mod tidy`.

Use the Go version required by root `go.work`, Node.js/npm compatible with the existing frontend dependencies, and the dependencies already approved in the committed manifests and lockfile. Restore those dependencies separately before validation. New modules or dependency changes require approval under root `AGENTS.md`.

Linux is the primary development target. Native validation requires CGO, a C compiler, `pkg-config`, and the GTK4 and WebKitGTK 6.0 development libraries. Windows and macOS require their native Wails build prerequisites and must be validated on those platforms before release. The Bash entry point is usable on Linux/macOS and in Git Bash on Windows; it does not replace native platform testing.

For a restricted environment, `GOCACHE` may be set to a writable cache directory before running checks. If the C compiler uses ccache, set `CCACHE_DIR` to a writable cache directory as well. Missing cached Go dependencies produce an error because validation deliberately runs offline. The existing frontend build combines `vue-tsc` and Vite; frontend behavioural tests will be added when their test-tool dependency is approved. A successful scaffold build does not establish product-level UI coverage.

Run `bash apps/desktop/scripts/check_test.sh` to test the validation entry point itself. These dependency-free fixtures verify command execution, failure propagation, missing prerequisites, read-only/offline Go settings and paths containing spaces. They do not replace native application builds.

## Interactive development and packaging

Wails CLI operations run from `apps/desktop`. The configuration is `build/config.yml`, and platform build tasks are under `build`. The generated Taskfiles place application output in `bin` by default. They can install dependencies and run `go mod tidy`; review dependency changes before invoking them and follow the repository approval rule.

Wails binding generation requires the matching Wails CLI. Regenerate bindings when Go services change; do not manually edit `frontend/bindings`. The validation script builds the checked-in bindings and does not regenerate them.

The scaffold's `build:server` and Docker tasks package Wails' browser/server mode for the desktop scaffold. They are not the ServerSend backup server, which belongs in `apps/server`. Mobile scaffold assets remain unused; this project targets Linux, Windows and macOS desktops.
