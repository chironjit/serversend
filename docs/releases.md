# Releases and GHCR deployment

Server and desktop share one version and Git tag: `vX.Y.Z`. The **Release** workflow checks both components, builds native desktop downloads, publishes the Linux amd64 server image with its embedded admin, and publishes one GitHub Release with desktop archives and SHA-256 checksums. Historical `server/v*` and `desktop/v*` tags remain intact but no longer trigger releases.

## Create a release

Merge your changes into the remote default branch, update `CHANGELOG.md`, commit, and push. From a clean checkout of that branch:

```sh
make release DRY_RUN=1  # fetch tags and preview, without creating/pushing a tag
make release            # choose and push the next patch version
# Or choose a new feature series explicitly:
make release VERSION=0.2.0
```

The command needs Git, Node.js 26 and permission to push tags to `origin`. It requires HEAD to match the remote default branch, including no uncommitted or untracked changes. It never commits or pushes source changes. It selects one patch above the highest historical unified/server/desktop version; with existing `server/v0.1.4`, that is `v0.1.5`. Explicit versions must also exceed all historical versions. Prerelease tags are unsupported. The command returns once the tag is pushed; follow **GitHub → Actions → Release** for the result. The workflow must already be committed and pushed before invoking the command.

Checks run for shared/server code and native desktop builds on Linux, Windows and macOS. Both must pass before packaging starts. Desktop downloads are a Linux AppImage, a Windows executable in a zip, and a macOS app bundle in a zip. Filenames include the native runner architecture; this does not build every architecture. Linux targets Ubuntu 24.04 or compatible and requires host GTK4/WebKitGTK 6.0 runtime libraries. The AppImage intentionally uses host libraries and WebKit subprocesses; it is not a fully self-contained runtime. Install FUSE 2 to launch it, or use `--appimage-extract-and-run`. Windows requires WebView2. Downloads use the tag's version in Windows/macOS metadata. Windows builds are unsigned; macOS bundles are ad-hoc signed without notarization. Release builds check GitHub's latest stable release automatically after the application starts. A newer release opens a popup with the versions and release notes. **Later**, Escape, or closing the popup postpones the update without downloading anything; it will be offered again on the next launch. **Update and restart** downloads and verifies the offered release, replaces the executable, AppImage or app bundle, and relaunches automatically. Finish transfers before accepting. Progress and errors appear in the popup; download and helper-handoff failures keep the old app running and allow retry. An offline or up-to-date startup stays quiet. **ServerSend → Check for Updates…** remains available for manual checks. Keep the installation in a user-writable directory (extract macOS bundles from their download first). Development builds (`currentVersion=dev`) disable updating. There is no periodic timer or installer.

The build embeds the tag version and GitHub repository with `-ldflags`. The updater accepts only the matching OS/architecture desktop payload and requires a 32-byte SHA-256 digest from `SHA256SUMS`; missing entries and digest mismatches fail closed. This provides integrity against the published checksum, not independent release authenticity: Ed25519 signatures, Authenticode signing and macOS notarization are not configured. Older manual-update builds must be replaced once to gain the update menu.

A draft GitHub Release holds downloads until all checks, packaging and container checks pass and the image is pushed. It uses GitHub's built-in `GITHUB_TOKEN`; no PAT is needed. Release notes are generated from GitHub changes and include the image/update channels. Maintain `CHANGELOG.md` for human-curated changes. This product remains experimental. Patch versions should preserve compatibility; use a new minor version for a new feature series or breaking pre-1.0 changes. Database schema versions remain separate.

Never move/reuse a published tag or overwrite an exact image version; the publish step refuses an exact image version that already exists. Protect release tags in repository rulesets. Failed runs can leave a draft and, if publishing partially succeeds, image tags. Inspect the failed step first. A rerun uses the original tagged commit. Use a fresh version if the fix requires a new commit or if the failed run pushed any image tag. A failed local tag push retains the local tag; inspect remote state before retrying. Publishing to GHCR and GitHub Releases is not atomic.

| Image tag | Meaning |
| --- | --- |
| `ghcr.io/chironjit/serversend:0.4.0` | Exact release, retained by policy |
| `ghcr.io/chironjit/serversend:0.4` | Moves with patch releases in the 0.4 series; recommended for Unraid testing |
| `ghcr.io/chironjit/serversend:latest` | Moves with every release, including new feature series |

The workflow does not enforce registry immutability; use an image digest if you need a cryptographically fixed deployment. Protect release Git tags against deletion/force updates in GitHub repository rulesets. Only maintainers should be able to push release tags.

## Desktop packaging and qualification

After restoring pinned dependencies and building `apps/desktop/frontend`, run `bash scripts/package-desktop.sh X.Y.Z` on each native OS. Outputs go to root `dist/`; `GITHUB_REPOSITORY` defaults to `chironjit/serversend` locally and is embedded from Actions for forks. The existing Release workflow builds all three platforms and publishes them with `SHA256SUMS` only after checks succeed. Do not publish a tag until these changes are merged and pushed.

macOS builds target 12.0 (Monterey), using the [shared desktop build setting](desktop-development.md#validation). Packaging verifies the executable's deployment minimum before signing and sets matching bundle metadata. Native CI on a newer macOS version does not replace testing the packaged app on macOS 12.

Linux packaging downloads the scaffold's existing linuxdeploy tool and verifies its pinned SHA-256 before execution. Its upstream `continuous` URL can change: a changed artifact deliberately fails the build until its digest is reviewed and updated in `apps/desktop/build/linux/appimage/build.sh`.

Desktop checks include consent, postponement, retries, failure handling and prompt rendering tests, plus a native fixture that uses the actual Wails helper to replace the installed binary and relaunch into the new version. The existing Windows/macOS/Linux CI matrix runs that fixture using a Windows executable zip, a signed macOS app bundle zip, or a Linux executable. Linux CI additionally packages and updates two real AppImages using extract-and-run mode. Local Linux executable and AppImage swap/relaunch tests pass; native Windows/macOS results require the hosted matrix to run.

Before release qualification, also exercise the actual GUI on each OS: startup offer, Later, Update and restart, progress/error display and the second launch finding no newer version. Check read-only installations and OS trust prompts, and test Linux FUSE launching. The fixture tests do not establish Gatekeeper/SmartScreen acceptance or native GUI behaviour.

## Registry setup

Ensure Actions is enabled and organization policy permits package writes. In **Packages → serversend → Package settings**, deliberately choose visibility. Public images allow anonymous Unraid pulls; a public source repository does not automatically make the package public.

For a private package, authenticate on Unraid before pulling. Use a GitHub personal access token (classic) with `read:packages` and access to the package; authorize organizational SSO if required. Enter the token at the password prompt, never in a committed env file:

```sh
docker login ghcr.io -u chironjit
```

Confirm authentication still works for Unraid's scheduled updater after reboot; credential persistence depends on the host configuration. Public images avoid this credential requirement.

## First Unraid installation

Use the versioned template `apps/server/deploy/unraid/serversend.xml` (or the Docker tab's saved container template) for the simplest integration with Unraid updates. Follow [Unraid network testing](unraid-testing.md) for directories, ownership and mappings, with these changes:

```sh
docker pull ghcr.io/chironjit/serversend:0.4
```

Set **Repository** to `ghcr.io/chironjit/serversend:0.4`. Keep TCP/UDP host/container ports both `58443`, the four paths, `SERVER_SEND_TLS_IPS`, and the extra parameters from that guide. No image tarball transfer is needed. For initial administrator bootstrap, replace `serversend:dev` in the guide's `docker run` command with `ghcr.io/chironjit/serversend:0.4`.

An existing Unraid installation only needs its Repository changed; preserve its paths and settings and do not bootstrap again. An existing local-computer Docker install is a separate server unless you deliberately migrate its complete persistent storage while stopped.

The Compose example now defaults to the `0.4` GHCR channel. Set `SERVER_SEND_IMAGE=serversend:dev` in its env file to keep using a locally loaded development image. Choose either Unraid's template or Compose to manage the container, not both.

## Updates

Unraid's **Docker → Check for Updates → Apply Update** pulls a changed image and recreates the container with its saved settings. A moving tag enables detection; restarting a container alone does not fetch an update. Autostart means start at boot, not auto-update.

For unattended updates, an optional third-party Unraid plugin is **CA Auto Update Applications** by Squid. If you choose to install it through Community Applications, enable scheduled Docker updates only for `serversend`, select a quiet time and enable notifications. This repository does not install that plugin or change your Unraid settings. A `0.4` subscription stays on that series; change it deliberately to `0.5` for a new feature series. `latest` follows all releases.

Updates interrupt active transfers; the desktop resumes them after reconnecting. If the new image migrates the database schema, startup first writes a full stopped-server backup to `SERVER_SEND_UPGRADE_BACKUP_DIR` (`/backups` in the template and Compose files) and refuses to migrate without that setting. The backup volume needs room for a complete copy. Rolling back after a migration means restoring that backup with the previous image; see [upgrades and downgrades](backup-restore.md#upgrades-and-downgrades).

For a Compose-managed deployment, manually update with:

```sh
docker compose --env-file serversend.env -f compose.unraid.yaml pull
docker compose --env-file serversend.env -f compose.unraid.yaml up -d
```

Do not delete volumes or host directories. Accounts, TLS identity and uploaded data persist in the host paths.

## Subsequent releases and version inspection

Use `make release` for each subsequent patch. The workflow moves the series channel (for example `0.4`) and `latest` after checks pass. You do not rebuild on Unraid. Moving to a new minor version publishes a new series channel; a `0.4` deployment stays on `0.4` until you deliberately change it.

Inspect the image actually used by the running container, even if a newer image has been pulled:

```sh
docker inspect --format '{{index .Config.Labels "org.opencontainers.image.version"}}' serversend
docker inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' serversend
```

## References

- [GitHub: publishing Docker images](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images)
- [GitHub: container registry authentication and visibility](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
- [Unraid: container management](https://docs.unraid.net/unraid-os/using-unraid-to/run-docker-containers/managing-and-customizing-containers/)
- [CA Auto Update Applications source](https://github.com/Squidly271/ca.update.applications)

## Build checks without releasing

Branch pushes and pull requests run the combined **Check** workflow, which builds the Linux amd64 server image using the release Dockerfile. Tagged releases build and smoke-test the image in the **publish** job. Use **Actions → Check → Run workflow** to check both components on demand. The reusable component workflows remain available for troubleshooting; component-prefixed tags no longer trigger them. A shared release version identifies a tested source revision, but CI does not test compatibility against historical desktop/server binaries. Keep API additions compatible with deployed clients; breaking protocols need an explicit migration window.

## Compatibility

Desktop and server from the same release tag are the only tested combination: CI checks both from one source revision. Historical desktop/server binaries are not tested against each other, so update both together. Server schemas only move forward; a newer schema is refused by older servers, and backups record their schema version.

| Release | Server schema | Desktop schema | Identity protocol | Tested with |
| --- | --- | --- | --- | --- |
| Unreleased (after v0.4.7) | 14 | 3 | 1 | Same-release desktop and server |

Add a row for each release. Upgrades from every earlier server schema are covered by `TestServerUpgradeEveryPriorSchema`; the pre-upgrade backup and downgrade restore by `TestUpgradeRequiresBackupAndDowngradeRestoresPriorSchema`.
