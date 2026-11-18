# Stopped-server backup and restore

`serversend -backup /absolute/new/directory` creates a standard/private-data backup. Stop the serving process first and run the command with the same `SERVER_SEND_*` deployment settings and volume access. It refuses a deployment held by another process, an existing destination, a destination inside a source volume, an uninitialized database, and symbolic links or special files in the copied trees. The destination parent must exist and be trusted and private.

```sh
# With the normal deployment environment already loaded and the server stopped:
serversend -backup /protected-backups/serversend-before-upgrade
```

The command takes native locks on config, data and staging for the complete operation. It uses SQLite `VACUUM INTO` to snapshot committed WAL contents without copying a live main file. It streams all config files, data objects and staging files into private directories. TLS files configured outside config are included under `external-tls/`. It does not run schema migrations or initialize certificates. All files and directories are flushed before `backup.json` is written as the completion marker. An ordinary error removes the new destination; a process kill may leave an incomplete directory. Never restore a directory without a completed `backup.json` or mix files from different backups.

Backups contain password hashes, live session records, identity, private CA material, the TOTP protection key and standard plaintext files, including retained versions and recoverable trash. Private file contents, names and vault wrappers remain encrypted; client recovery packages and usable vault keys are not present and must be kept separately. Retention is suspended while the stopped-server barrier is held. They are unencrypted and require protected storage. Copy the completed backup to separate storage and restrict access. Preserve your deployment template/env settings and image version alongside it: the command does not capture arbitrary environment variables, host ACLs, UID/GID, Docker configuration or external infrastructure. `backup.json` records the format version, time, schema version, server ID, configured TLS IPs and whether external TLS was used. Copying all objects also includes harmless unreferenced objects; this is not an object compaction tool.

To back up an Unraid container, stop it and run a one-shot container with the same three source mounts, TLS settings, UID/GID and the private `/backups` mount. Use `--network none` and pass `-backup /backups/new-name`. Do not launch a second serving container. Test this on the actual pool/array layout before relying on it; local tests do not establish Unraid filesystem locking or power-loss durability.

## Restore

`serversend -restore /absolute/backup -restore-as replace|clone` copies a completed backup into the configured config, data and staging directories. They must exist, be private and be empty (except lock files). It holds their locks, refuses incomplete backups (no `backup.json`), backups from a newer schema, and targets overlapping the backup. On error it empties the targets again. It never modifies the backup. Run it like `-backup`: container stopped, `--network none`, the backup mounted read-only.

- **replace** keeps the server identity, sessions and private CA, and leaves the schema unchanged so the matching image can serve it. Only start it once the original is permanently stopped: two running copies of one identity are indistinguishable to clients. Devices whose refresh credential rotated after the backup must sign in again.
- **clone** migrates to this image's schema and creates a new server identity. It revokes all device and browser sessions and discards the private CA (startup issues a new one). With external TLS it requires a different certificate than the backup's. Desktops pair with a clone as a new server. Clones refuse private-mode data: its ciphertext is bound to the original server ID, so restore it only as a replacement.

On Unraid, with the container stopped and the three host directories empty (owned by 10001, mode 0700):

```sh
docker run --rm --network none --read-only -e SERVER_SEND_TLS_IPS=192.168.1.10 \
  -v /mnt/user/appdata/serversend/config:/config -v /mnt/user/serversend/data:/data \
  -v /mnt/user/serversend/staging:/staging -v /mnt/user/serversend-backups:/backups:ro \
  ghcr.io/chironjit/serversend:0.4 -restore /backups/NAME -restore-as replace
```

Restore drill: restore into three new directories, start the same image version on an isolated network, compare the server identity and certificate, sign in with the restored account/TOTP material, browse the catalog and download representative empty, small and multi-chunk files, comparing hashes with the originals. Stop the drill instance before resuming the original.

## Upgrades and downgrades

When a new image needs a schema migration, startup first takes this stopped-server backup into `SERVER_SEND_UPGRADE_BACKUP_DIR`, named `serversend-<server ID>-schema-<old>-to-<new>`, then migrates. The directory must be outside the three volumes and have room for a complete copy. Without the setting, the server refuses to migrate and stops; `none` skips the backup deliberately, for operators with their own stopped-server backups. An existing backup with the same name is never overwritten: if an upgrade failed or was rolled back, check the old backup, move it aside, and restart. Startups without a migration take no backup. Unsupported newer schemas remain rejected.

To downgrade: stop the newer server, move the live config/data/staging contents aside, run the **older** image with `-restore <pre-upgrade backup> -restore-as replace`, then start the older image. Never run an older image against metadata already migrated by a newer one. Work uploaded after the upgrade is not in the backup.

Local standard and private recovery drills verify identity, envelopes and restored bytes; private recovery uses an externally exported package. See [private storage](private-storage.md) for format and recovery limits. Live snapshots with GC suspension, independent format review, cloning private data and actual Unraid qualification remain open roadmap gates. Native locks prevent cooperating processes sharing the same mounted volumes; they cannot detect copied volumes or an independent host running a copied identity. Storage directories must be protected against other local writers, and network filesystems are not supported by this procedure.
