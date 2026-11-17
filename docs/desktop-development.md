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

## Saved connections

The connection screen lists saved servers and provides removal. **Switch server / add server** returns to that list without signing out. One server is active at a time; transfers must finish before switching. Each origin pins a stable server ID/public key and uses OS CA trust or an operator-supplied public CA certificate. For private servers, use **Choose server CA certificate**, compare the imported CA fingerprint with the operator over a trusted channel, then approve. TLS hostname, chain and expiry checks and a fresh channel-bound signed identity proof gate every replacement HTTP/3 or HTTP/2 connection. Leaf renewal under the same CA and identity needs no new approval; CA/identity changes require explicit trust repair. The server’s private keys are never copied to the desktop.

Select **Remember sign-in on this device** before signing in to save a rotating refresh credential in macOS Keychain, Windows Credential Manager, or Linux Secret Service. Linux needs an unlocked Secret Service provider in the desktop session. Missing/locked stores produce an error and never fall back to plaintext. Saved addresses, public CA/identity pins, installation IDs and usernames live in `ServerSend/servers.json` under the OS user configuration directory; passwords and TOTP secrets are never saved there. Access tokens last ten minutes and remain in Go memory. Refresh happens before expiry while the app is active; a 30-second heartbeat records recent connectivity. The native app uses Wails single-instance handling, and refresh/account switching is serialized so two workers cannot rotate the same credential independently.

A device requires full password/TOTP login after 30 days without a fresh successful rotation. Active devices require TOTP (or a single-use recovery code) every 60 days, without reentering their password. Ordinary refresh never extends that second deadline. Restart retries a securely persisted pending rotation after a lost response; failed keychain promotion preserves the retry tuple. Heartbeats do not independently extend either deadline. File requests and heartbeat updates reject revoked devices. Transfers interrupted by expiry, verification or revocation need retrying; durable upload queues remain separate roadmap work. Brief network interruptions retry with the same transfer identities. Single-file downloads retain verified chunk checkpoints; choose the same version and destination again to resume. See [current limits](vertical-slice.md) for checkpoint storage and cleanup.

The **Devices** panel lists current and offline authorizations in pages of 100, with confirmed individual revocation and **Sign out other devices**. Use **Sign out** for this device. Sign-out disables local restoration before attempting keychain deletion, then revokes the server authorization; removing a profile deletes local trust/preferences and the saved credential. Offline sign-out cannot guarantee server revocation. Certificate-only legacy profiles require explicit CA/trust repair and one full login; their old origin-only keychain entries are deleted during repair. New credentials are keyed by approved origin, server ID/public key and account ID, and restore rejects mismatched scopes. Full login reuses the server/account installation record and invalidates its old access and refresh grants; refresh and periodic TOTP retain the same device ID. Installation IDs are public deduplication labels, not authentication secrets. Removing a profile loses that label; do not clone the profile file across devices. Native single-instance and complete OS restart/locked-store behavior still need platform qualification.

### Native credential-store qualification

From the repository root, run `go run ./apps/desktop/cmd/credentialcheck -mode=roundtrip` as the desktop user. This uses an isolated `ServerSendQualification` entry, verifies a real child process can read it, deletes it and verifies loss. It never reads application credentials. For locked-store checks, run `-mode=seed`, lock the store manually, then run `-mode=expect-unavailable` and cancel any OS unlock prompt. Unlock afterwards and run `-mode=cleanup`. On Linux an unavailable-service check can use `DBUS_SESSION_BUS_ADDRESS=unix:path=/tmp/serversend-absent-secret-service` with `-mode=expect-unavailable`. Use the provider’s actual lock/denial mechanism; locking the desktop does not necessarily lock credential APIs on every OS. Record unsupported lock modes explicitly. Never lock the user's store automatically.

Linux Secret Service roundtrip/process restart, missing-item and unavailable-service checks passed on 2026-11-17. Locked-store application behavior is covered by fault tests; actual locked-store/OS restart checks and native Windows/macOS runs remain open. Record results in the verification map; cross-compilation is not a native-store result.
