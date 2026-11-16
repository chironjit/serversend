# Repository instructions

## Approval for new modules

Before adding any new module or third-party dependency, ask the user for approval. State the proposed module and its purpose, and allow the user to choose a preferred alternative. Wait for explicit approval before installing it or adding it to the project, including dependency manifests or lockfiles. This applies to application, development, build, and test dependencies, as well as new Go workspace modules.

## Recorded approvals

The user approved `github.com/mattn/go-sqlite3` for SQLite persistence and `github.com/pquerna/otp` for server-side TOTP enrollment and verification on 2026-11-14. The OTP choice replaces the earlier `rsc.io/2fa` preference. Its required barcode dependency is part of that integration. Tailwind CSS was also explicitly selected for both interfaces, with light and dark modes required. Do not ask again for these approved choices; other new dependencies still require approval.

For Chunk 01, the user accepted Vitest, `@vue/test-utils` and `happy-dom` for frontend tests, and selected Oxc (`oxlint`, `oxlint-tsgolint`, `oxfmt`) for linting, checking and formatting. Do not add ESLint, Prettier or Playwright. The CI draft was reviewed and its activation approved on 2026-11-16; it is active at `.github/workflows/check.yml`. The user also approved removing the unused iOS/Android scaffold assets on 2026-11-16; mobile clients remain deferred.
