# Repository instructions

## Approval for new modules

Before adding any new module or third-party dependency, ask the user for approval. State the proposed module and its purpose, and allow the user to choose a preferred alternative. Wait for explicit approval before installing it or adding it to the project, including dependency manifests or lockfiles. This applies to application, development, build, and test dependencies, as well as new Go workspace modules.

## Recorded approvals

The user approved `github.com/mattn/go-sqlite3` for SQLite persistence and `github.com/pquerna/otp` for server-side TOTP enrollment and verification on 2026-11-14. The OTP choice replaces the earlier `rsc.io/2fa` preference. Its required barcode dependency is part of that integration. Tailwind CSS was also explicitly selected for both interfaces, with light and dark modes required. Do not ask again for these approved choices; other new dependencies still require approval.
