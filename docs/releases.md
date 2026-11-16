# Releases and GHCR deployment

Git release tags are the version source of truth for this repository. The first planned release is `v0.1.0`; it has not been published by adding this workflow. Server images include OCI version, source repository and commit labels. This workflow publishes the server and its embedded browser admin, not desktop installers. Rebuild the desktop separately when client changes are released.

## Version policy

- `v0.1.0`: initial versioned network-testing release; still experimental.
- `v0.1.1`: compatible fixes within the same series.
- `v0.2.0`: new feature series or breaking changes while pre-1.0.
- `v1.0.0`: a future stable milestone, not the current maturity level.

Record changes under Unreleased in `CHANGELOG.md`, then move them under the version/date when preparing a release. Never move or reuse a published Git tag or overwrite an exact image version. Publish versions in increasing order; backport channels and prerelease tags are not supported by this initial workflow. Patch releases must preserve client compatibility and avoid migrations requiring manual intervention. A database schema version remains separate from the application version.

| Image tag | Meaning |
| --- | --- |
| `ghcr.io/chironjit/serversend:0.1.0` | Exact release, retained by policy |
| `ghcr.io/chironjit/serversend:0.1` | Moves with patch releases in the 0.1 series; recommended for Unraid testing |
| `ghcr.io/chironjit/serversend:latest` | Moves with every release, including new feature series |

The workflow does not enforce registry immutability; use an image digest if you need a cryptographically fixed deployment. Protect release Git tags against deletion/force updates in GitHub repository rulesets. Only maintainers should be able to push release tags.

## First publish from your development computer

1. Commit the release workflow and accompanying changes, and push your branch. Merge to the normal release branch before tagging. Check GitHub Actions is enabled for the repository and that package writes are allowed by any organization policy.
2. Update `CHANGELOG.md` with the release version/date and commit that change. Ensure all intended source changes are committed.
3. From that committed release revision:

   ```sh
   git tag -a v0.1.0 -m "ServerSend 0.1.0"
   git push origin HEAD
   git push origin v0.1.0
   ```

4. Watch **Actions → Release server**. It runs the existing Linux/Windows/macOS checks, builds a Linux amd64 server image, checks container TLS/persistence, then pushes the three image tags. It uses GitHub's built-in `GITHUB_TOKEN` with `packages: write`; no personal access token is needed for the workflow.
5. In your GitHub account's **Packages → serversend → Package settings**, choose visibility deliberately. New packages are private by default. Public permits anonymous pulls on Unraid, but exposes the image contents to everyone. A public source repository does not automatically make the package public.

For a private package, authenticate on Unraid before pulling. Use a GitHub personal access token (classic) with `read:packages` and access to the package; authorize organizational SSO if required. Enter the token at the password prompt, never in a committed env file:

```sh
docker login ghcr.io -u chironjit
```

Confirm authentication still works for Unraid's scheduled updater after reboot; credential persistence depends on the host configuration. Public images avoid this credential requirement.

## First Unraid installation

Use the Docker tab's saved container template for the simplest integration with Unraid updates. Follow [Unraid network testing](unraid-testing.md) for directories, ownership and mappings, with these changes:

```sh
docker pull ghcr.io/chironjit/serversend:0.1
```

Set **Repository** to `ghcr.io/chironjit/serversend:0.1`. Keep TCP host/container ports both `58443`, the three persistent paths, `SERVER_SEND_TLS_IPS`, and the extra parameters from that guide. No image tarball transfer is needed. For initial administrator bootstrap, replace `serversend:dev` in the guide's `docker run` command with `ghcr.io/chironjit/serversend:0.1`.

An existing Unraid installation only needs its Repository changed; preserve its paths and settings and do not bootstrap again. An existing local-computer Docker install is a separate server unless you deliberately migrate its complete persistent storage while stopped.

The Compose example now defaults to the `0.1` GHCR channel. Set `SERVER_SEND_IMAGE=serversend:dev` in its env file to keep using a locally loaded development image. Choose either Unraid's template or Compose to manage the container, not both.

## Updates

Unraid's **Docker → Check for Updates → Apply Update** pulls a changed image and recreates the container with its saved settings. A moving tag enables detection; restarting a container alone does not fetch an update. Autostart means start at boot, not auto-update.

For unattended updates, an optional third-party Unraid plugin is **CA Auto Update Applications** by Squid. If you choose to install it through Community Applications, enable scheduled Docker updates only for `serversend`, select a quiet time and enable notifications. This repository does not install that plugin or change your Unraid settings. A `0.1` subscription stays on that series; change it deliberately to `0.2` for a new feature series. `latest` follows all releases.

Updates stop active transfers, and the current desktop cannot resume them. Back up all three persistent directories and the deployment settings with the container stopped before upgrades; preserve matching metadata, keys and objects. The auto-update plugin alone does not provide this backup policy. Use manual updates until your backup schedule is in place. After a database migration, rollback may require restoring the matching backup as well as the previous image.

For a Compose-managed deployment, manually update with:

```sh
docker compose --env-file serversend.env -f compose.unraid.yaml pull
docker compose --env-file serversend.env -f compose.unraid.yaml up -d
```

Do not delete volumes or host directories. Accounts, TLS identity and uploaded data persist in the host paths.

## Subsequent releases and version inspection

Update the changelog, commit and push, then tag `v0.1.1` (and later patch versions) using the same process. The workflow moves `0.1` and `latest` after checks pass. You do not rebuild on Unraid. Create an optional GitHub Release from the tag and copy its changelog entry for human-readable release notes.

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
