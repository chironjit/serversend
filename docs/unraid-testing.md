# Unraid network testing

Use TCP **58443** permanently, with a DHCP reservation for the Unraid LAN address. This is the ServerSend default, selected to avoid the commonly used 8443 port. Check that another container is not already using it. The desktop accepts a bare IP and supplies HTTPS and port 58443. An explicit port is still supported. The server also listens on UDP 58443 for HTTP/3 evaluation; the desktop continues to prefer HTTP/2. See [transport measurements](performance-baseline.md). Actual Unraid/Tailscale UDP reachability remains unverified.

This image uses Debian Bookworm slim and runs as UID/GID 10001.

For versioned images from GHCR and update subscriptions, start with [releases and GHCR deployment](releases.md). The manual image transfer below remains available for local development builds.

## Build and transfer the image

On your development computer, from the repository root (Docker must be running):

```sh
docker build -f apps/server/deploy/Dockerfile -t serversend:dev .
docker save -o /tmp/serversend-dev.tar serversend:dev
scp /tmp/serversend-dev.tar root@UNRAID_IP:/mnt/user/appdata/serversend-dev.tar
```

Build for the architecture of the Unraid host (normally linux/amd64). On Unraid:

```sh
docker load -i /mnt/user/appdata/serversend-dev.tar
mkdir -p /mnt/user/appdata/serversend/config /mnt/user/serversend/data /mnt/user/serversend/staging
chown 10001:10001 /mnt/user/appdata/serversend/config /mnt/user/serversend/data /mnt/user/serversend/staging
chmod 700 /mnt/user/appdata/serversend/config /mnt/user/serversend/data /mnt/user/serversend/staging
```

Use local storage for the SQLite configuration directory, never an SMB/NFS mount. Set the appdata share's storage pool in Unraid before setup; use a pool-backed appdata share for metadata performance. Choose the data share's pool/array policy deliberately: measured throughput includes its disk speed. Keep these directories private and do not let another process alter the database or objects.

## Configure once in Unraid's Docker UI

Add a container (enable Advanced View where needed):

| Field | Value |
| --- | --- |
| Name | `serversend` |
| Repository | `serversend:dev` (the locally loaded image) |
| Network type | Bridge |
| WebUI | `https://[IP]:[PORT:58443]/admin/` |
| Port | Host `58443`, container `58443`, TCP |
| Port for HTTP/3 evaluation | Host `58443`, container `58443`, UDP |
| Path | `/mnt/user/appdata/serversend/config` → `/config`, read/write |
| Path | `/mnt/user/serversend/data` → `/data`, read/write |
| Path | `/mnt/user/serversend/staging` → `/staging`, read/write |
| Variable | `SERVER_SEND_TLS_IPS` = your Unraid LAN IPv4 address |
| Extra parameters | `--read-only --cap-drop=ALL --security-opt=no-new-privileges --stop-timeout=15` |

Do not enable privileged mode. Enable Autostart after setup. Unraid saves this template, including mappings and settings, for later recreations. If using Tailscale too, supply LAN and Tailscale IPv4 addresses comma-separated in `SERVER_SEND_TLS_IPS`.

Alternatively, if Docker Compose is already available, copy `apps/server/deploy/compose.unraid.yaml` and `unraid.env.example` to a persistent directory. Rename the env example `serversend.env`, edit the IP once, and use `SERVER_SEND_IMAGE=serversend:dev` for the locally loaded image (the default is the published GHCR `0.1` channel), then run:

```sh
docker compose --env-file serversend.env -f compose.unraid.yaml up -d
```

The Compose example publishes on the configured LAN address. To expose it on the tailnet too, adjust the host binding explicitly; certificate IPs and listener bindings are separate settings. No router port forwarding is needed for LAN tests.

## Bootstrap the administrator once

The bootstrap command opens the same storage, so stop the serving container before running it:

```sh
docker stop serversend
docker run --rm --network none --read-only \
  -e SERVER_SEND_TLS_IPS=192.168.1.10 \
  -v /mnt/user/appdata/serversend/config:/config \
  -v /mnt/user/serversend/data:/data \
  -v /mnt/user/serversend/staging:/staging \
  serversend:dev -bootstrap-admin admin
docker start serversend
```

Replace the sample IP with the same configured address. Save the printed temporary password (valid for one hour). Visit `https://UNRAID_IP:58443/admin/`. The private certificate initially causes a browser warning; verify that you are connecting to your own server. Replace the temporary password, enroll an authenticator, and save the recovery codes. Use a fresh authenticator code or a recovery code to finish sign-in.

Create a user account in the admin page. Creation requires a full admin sign-in within five minutes; sign out and in again if that window expires. Give the user the displayed temporary password. In the desktop, connect to `UNRAID_IP`, compare its certificate fingerprint against `docker logs serversend`, and sign in as the user. Replace the user's temporary password and enroll their authenticator. Administrators cannot browse, upload, or download files; existing administrator-owned test files remain stored but are no longer accessible through transfer routes.

## Changing an existing installation from port 8443

Change both the host and container port in the saved Unraid template to **58443**, and update its WebUI URL. If you explicitly set `SERVER_SEND_TCP_ADDRESS`, change it to `:58443` (otherwise the new image supplies that default). Recreate the container with the new image and unchanged storage mappings. Use `https://UNRAID_IP:58443/admin/` and reconnect the desktop at port 58443. Certificates and accounts do not need to be recreated for a port change.

For the local test script, edit `LOCAL_PORT=58443` in the existing `settings.env` file before restarting; saved custom ports are deliberately preserved.

## Updating without repeating setup

Build and load the new image, stop the old container, and recreate it from the saved Unraid template with the same three host paths and environment values. For Compose:

```sh
docker compose --env-file serversend.env -f compose.unraid.yaml up -d --force-recreate
```

Do not bootstrap again, delete the host folders, or change the mappings. `/config` holds SQLite accounts/settings/metadata, server identity, TLS certificates and the TOTP protection key; `/data` holds uploaded objects; `/staging` is reserved for staging. The deployment template or env file persists listener/certificate-IP settings outside the image. Schema migrations apply automatically; make a stopped-container backup of all three paths and the deployment settings before upgrading. Preserve the complete config directory, especially its keys. Restore matching metadata and objects together. Downgrading an image after a schema migration is not supported without restoring the matching backup.

The initial `serversend -backup DIR` command captures a consistent stopped-server snapshot, including committed SQLite WAL data, config keys, objects and staging. See [backup/restore instructions](backup-restore.md). Native locks now refuse a second process sharing any deployment volume; stop the serving process before bootstrap, recovery, `-check` or backup. Do not delete live `.serversend.lock` files. These local-lock and backup tests still require actual Unraid pool/array qualification.

## Measure actual throughput

Upload a stable multi-gigabyte file, then download it to a different local directory. The desktop shows upload/download rates and a rolling 60-second chart. These are useful acknowledged/verified bytes per second, including network, disk and verification costs; chunk acknowledgements cause bursts. Source hashing before upload is preparation and does not count as transferred bytes. Compare the restored file's hash with the original.

Then upload a folder containing nested folders, empty folders and many small files. Choose the destination by opening a server folder first. The selected source folder is created beneath it. Duplicate folder names return an error; choose another destination or rename the source. The skip-and-report implementation is described below; Linux backend tests pass, while native picker/Windows/macOS qualification remains pending. Transfers are sequential, not resumable, and not persisted across desktop restarts. The catalog currently shows at most 1000 entries per folder; large-scale pagination and queue persistence remain follow-up work. Desktop sign-in must be repeated after an app restart, but server setup and user enrollment persist.

Older development images used Ed25519 HTTPS certificates that Firefox cannot negotiate. New images use ECDSA P-256. Existing deployments retain their original certificates until explicitly migrated; see [Firefox certificate compatibility](server-development.md#firefox-certificate-compatibility) before moving TLS files. This is separate from the normal private-CA trust warning.

## Link-handling implementation and remaining gates

The user approved skip-and-report and the `golang.org/x/sys` filesystem integration on 2026-11-17 as [feature 1](implementation-tasks.md#1-graceful-cross-platform-symlink-handling). The implementation skips links, directory redirections and unsupported special files and reports bounded path/reason details with exact counts. It distinguishes completion with skips, an entirely skipped selection and genuine failure. Source opening refuses link-bearing ancestors too, including `/tmp` and `/var` aliases on macOS; explicitly select the real path if needed. A native picker may resolve a link before returning it, so the application can enforce its policy only on the supplied path. Linux runtime/race and UI checks pass. Actual picker behavior, Windows reparse/cloud fixtures and native Windows/macOS execution remain open gates. Following targets is outside the approved fix.
