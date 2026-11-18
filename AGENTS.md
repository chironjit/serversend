# Repository instructions

## Approval for new modules

Before adding any new module or third-party dependency, ask the user for approval. State the proposed module and its purpose, and allow the user to choose a preferred alternative. Wait for explicit approval before installing it or adding it to the project, including dependency manifests or lockfiles. This applies to application, development, build, and test dependencies, as well as new Go workspace modules.

## Recorded approvals

- `github.com/mattn/go-sqlite3`: SQLite persistence (2026-11-14).
- `github.com/pquerna/otp`: server-side TOTP enrollment and verification, including its required barcode dependency; replaces `rsc.io/2fa` (2026-11-14).
- Tailwind CSS: both interfaces, with light and dark modes required.
- Vitest, `@vue/test-utils`, `happy-dom`: Chunk 01 frontend tests.
- Oxc (`oxlint`, `oxlint-tsgolint`, `oxfmt`): linting, checking and formatting. Do not add ESLint, Prettier or Playwright.
- CI activation: approved 2026-11-16; active at `.github/workflows/check.yml`.
- Unused iOS/Android scaffold removal: approved 2026-11-16; mobile clients remain deferred.
- `@tailwindcss/vite`: admin shell Tailwind build, pinned to `4.3.5` to match `tailwindcss` (2026-11-16).
- `golang.org/x/crypto/argon2`: production Argon2id password hashing in `apps/server` only, including its `golang.org/x/sys` requirement (2026-11-16). Other `x/crypto` uses require approval.
- `github.com/quic-go/quic-go`: HTTP/3 with verified HTTP/2 fallback, including required transitive dependencies (2026-11-17). Authentication and mutations must not use 0-RTT.
- `golang.org/x/mod`: indirect dependency for the Wails GitHub updater’s semantic version comparison in `apps/desktop` (2026-11-17).
- `golang.org/x/sys`: race-resistant no-follow upload source opening, link/reparse-point classification on Linux, macOS and Windows, and native server storage locks (2026-11-17).

Do not ask again for these approved uses; other new dependencies still require approval.

- `github.com/zalando/go-keyring` and required transitive dependencies: desktop session storage in the native OS credential store (2026-11-17).

## Verbosity
When planning, thinking, building, coding or implementing - ensure that you aim for simplicity and robustness. Be mindful of token usage on your side, and manage documentation generation to keep it minimal to what is needed for us to track the progress of the project.

## Documentation
Documentation is important, but also, too much does not serve the purpose. Humans don't read it, and it seems agents also ignore it unless specifically prompted. Please read the documentation before starting and before ending your task, review the documentation and see if it needs to be updated. Delete any documentation that will not serve to help agents or humans in the future. 


- `golang.org/x/crypto/argon2`: desktop/shared vault key derivation (2026-11-18).
