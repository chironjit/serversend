# Desktop development

The desktop application lives in `apps/desktop`. It contains the Wails/Vue client, account connection and file transfer services. Keep reusable code in root `shared` and project documentation in root `docs`.

## Validation

Run from the repository root:

```bash
bash apps/desktop/scripts/check.sh
```

The script also works from another working directory when invoked by its full path. It checks Go and Oxfmt formatting, runs Oxc lint/type checks and Vue template checks, runs Vitest and the production frontend build, runs Go vet and race-enabled tests, and compiles the native production application into a temporary directory. It reports missing prerequisites as failures and does not silently skip checks. It does not install packages, download Go modules or toolchains, or run `go mod tidy`.

Use the Go version required by root `go.work` and Node.js 26 with npm (the version used by the CI workflow and local verification). Restore the dependencies already approved in the committed manifests and lockfile separately before validation. New modules or dependency changes require approval under root `AGENTS.md`.

Linux is the primary development target. Native validation requires CGO, a C compiler, `pkg-config`, and the GTK4 and WebKitGTK 6.0 development libraries. Windows and macOS require their native Wails build prerequisites and must be validated on those platforms before release. The Bash entry point is usable on Linux/macOS and in Git Bash on Windows; it does not replace native platform testing.

For a restricted environment, `GOCACHE` may be set to a writable cache directory before running checks. If the C compiler uses ccache, set `CCACHE_DIR` to a writable cache directory as well. Missing cached Go dependencies produce an error because validation deliberately runs offline. Integration/smoke checks need temporary loopback TCP listeners. A successful scaffold check does not establish product-level UI coverage.

## Frontend checks

Run these commands from `apps/desktop/frontend`:

| Command | Purpose |
| --- | --- |
| `npm run lint` | Oxlint with type-aware rules and warnings treated as failures |
| `npm run check` | Oxlint/tsgolint TypeScript diagnostics, then `vue-tsc` for actual Vue components/templates |
| `npm run format:check` | Oxfmt checks frontend source/configuration and component-owned JavaScript check scripts |
| `npm run format` | Apply the same formatting |
| `npm test` | Vitest using Vue Test Utils and happy-dom; no browser installation |
| `npm run build` | Vue type check and Vite production bundle |

Oxc supplies `oxlint`, `oxlint-tsgolint` and `oxfmt`; ESLint, Prettier and Playwright are not added. Oxfmt excludes generated bindings, lockfiles and build output. Oxlint checks Vue script sections but currently has no template linting; its TypeScript checker uses `checks/vue.d.ts` for SFC import boundaries. `vue-tsc` separately resolves the actual Vue files and checks templates. A negative check confirmed that an invalid template property is rejected with that shim present. See [Oxc compatibility](https://oxc.rs/compatibility) and [type-aware checking](https://oxc.rs/docs/guide/usage/linter/type-aware.html).

The eight scaffold component tests cover bound-service results, empty/Unicode input, safe text rendering, failure/retry, responsive time events and event/timer cleanup. These tests mock the bridge; the separate live smoke check exercises the real Go service. Tests exposed missing event/timer cleanup in the scaffold, which is now fixed, and service failures now offer a safe retry message.

Run `bash apps/desktop/scripts/check_test.sh` to test the validation entry point itself. These dependency-free fixtures verify command execution, failure propagation, missing prerequisites, read-only/offline Go settings and paths containing spaces. They do not replace native application builds.

## Interactive development and packaging

Release downloads and startup self-update checks are described in [release instructions](releases.md). Release packaging embeds `main.currentVersion` and `main.updateRepository`; ordinary development builds use `dev` and disable the updater.

Wails CLI operations run from `apps/desktop`. The configuration is `build/config.yml`, and platform build tasks are under `build`. The generated Taskfiles place application output in `bin` by default. They can install dependencies and run `go mod tidy`; review dependency changes before invoking them and follow the repository approval rule.

Wails binding generation requires the matching Wails CLI. Regenerate bindings when Go services change; do not manually edit `frontend/bindings`. The component validation script builds the checked-in bindings; root `make check` also regenerates into a temporary directory and compares them.

The scaffold's `build:server` and Docker tasks package Wails' browser/server mode for the desktop scaffold. They are not the ServerSend backup server, which belongs in `apps/server`. The unused iOS/Android scaffold assets and their Taskfile includes were removed on 2026-11-16; this project targets Linux, Windows and macOS desktops, and mobile clients remain deferred.

## Workspace and binding verification

`make check-layout` verifies module/workspace names, scaffold assets, Taskfile includes, and desktop packaging references using Node's built-in assertions. It checks packaging inputs; native installer qualification still belongs to the release work.

`make check-bindings` runs the matching Wails v3.0.0 generator into a temporary directory and compares the result with the checked-in bindings. It does not invoke Taskfile prerequisites, install dependencies, or run `go mod tidy`. Build the frontend first so its embedded `dist` directory exists. The script looks for the CLI in this order: an explicit `WAILS3` override, `wails3` on PATH, then Go's `GOBIN` (or the first `GOPATH` entry's `bin` directory when `GOBIN` is unset). A selected executable with the wrong version fails rather than silently switching installations. An explicit override is also supported:

```sh
WAILS3="$(go env GOPATH)/bin/wails3" make check-bindings
```

When Go bindings intentionally change, run `bash apps/desktop/scripts/check_bindings.sh --write` with the same environment and review the regenerated files. Do not edit generated files manually.

The desktop Go tests validate the actual embedded frontend: the application mount, compiled JavaScript bundle, and every root-relative asset referenced by the production index must exist. This catches missing build output at the boundary shipped in the native executable.

## Live scaffold smoke check

After building the frontend, `make check-smoke` builds the desktop scaffold in Wails `server,production` mode into a temporary directory. A Node built-in HTTP/WebSocket client starts it on loopback and verifies embedded assets, a real generated-method-ID call, malformed arguments, the origin guard and a live Go time event. The child process and binary are cleaned up. No Playwright, browser driver or external GUI service is used.

This smoke test covers the bridge and assets, not native WebView rendering. Native installer/signing and visual qualification remain release work. The Wails server-mode scaffold tested here is separate from `apps/server`.

## Fresh checkout and CI review

After installing the native platform prerequisites, Go and Node, restore pinned dependencies from a fresh checkout:

```sh
(cd shared && GOWORK=off go mod download)
(cd apps/server && GOWORK=off go mod download)
(cd apps/desktop && go mod download)
(cd apps/desktop/frontend && npm ci)
GOWORK=off go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0
```

Run `make check`; the bindings script can discover the CLI in Go's bin directory even when that directory is not on PATH. Add the directory to PATH if you also want to invoke `wails3` directly, or use `WAILS3` for a custom location. Dependency restoration is separate from the offline checks; no Taskfile `tidy`/installation prerequisites run implicitly.

The reviewed CI workflow is active at `.github/workflows/check.yml` (approved 2026-11-16). It checks native desktop builds on Ubuntu 24.04, Windows and macOS, with shared/server checks and the live smoke test on Linux. It restores dependencies, invokes component-owned commands, and verifies no tracked files changed. It has read-only repository permissions and no publishing/release steps. Hosted runs execute on push; until the first hosted Windows/macOS runs pass, cross-platform results are not claimed as verified.

The workflow follows the layered approach in [Wails v3's CI guide](https://v3.wails.io/contributing/testing-ci/) and checks the [current upstream workflow](https://github.com/wailsapp/wails/blob/master/.github/workflows/build-and-test-v3.yml) for GTK4/WebKitGTK 6.0 prerequisites. Older guide examples still mention Go 1.25/GTK3; this repository's `go.work` and pinned Wails module govern versions. Upstream uses D-Bus/Xvfb for GUI tests. Our current tests compile the native executable and exercise the bridge in server mode, so they do not need a virtual display. Future native GUI tests must add the corresponding display/session setup.

Local verification passed both in the working tree and in a disposable Git checkout of the current source snapshot: locked `npm ci`, then `make check`, then `git diff --exit-code`. The clean checkout reused the installed native prerequisites and Go module cache; it did not reuse `node_modules` or `frontend/dist`. The snapshot commit was created only in `/tmp`; the repository's branch/index were not changed.
