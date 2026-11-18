# Stopped-server backup and restore

`serversend -backup /absolute/new/directory` creates a standard/private-data backup. Stop the serving process first and run the command with the same `SERVER_SEND_*` deployment settings and volume access. It refuses a deployment held by another process, an existing destination, a destination inside a source volume, an uninitialized database, and symbolic links or special files in the copied trees. The destination parent must exist and be trusted and private.

```sh
# With the normal deployment environment already loaded and the server stopped:
serversend -backup /protected-backups/serversend-before-upgrade
```

The command takes native locks on config, data and staging for the complete operation. It uses SQLite `VACUUM INTO` to snapshot committed WAL contents without copying a live main file. It streams all config files, data objects and staging files into private directories. TLS files configured outside config are included under `external-tls/`. It does not run schema migrations or initialize certificates. All files and directories are flushed before `backup.json` is written as the completion marker. An ordinary error removes the new destination; a process kill may leave an incomplete directory. Never restore a directory without a completed `backup.json` or mix files from different backups.

Backups contain password hashes, live session records, identity, private CA material, the TOTP protection key and standard plaintext files, including retained versions and recoverable trash. Private file contents, names and vault wrappers remain encrypted; client recovery packages and usable vault keys are not present and must be kept separately. Retention is suspended while the stopped-server barrier is held. They are unencrypted and require protected storage. Copy the completed backup to separate storage and restrict access. Preserve your deployment template/env settings and image version alongside it: the command does not capture arbitrary environment variables, host ACLs, UID/GID, Docker configuration or external infrastructure. `backup.json` records the format version, time, configured TLS IPs and whether external TLS was used. Copying all objects also includes harmless unreferenced objects; this is not an object compaction tool.

To back up an Unraid container, stop it and run a one-shot container with the same three source mounts, TLS settings, UID/GID and a fourth private mount at `/backups`. Use `--network none` and pass `-backup /backups/new-name`. Do not launch a second serving container. Test this on the actual pool/array layout before relying on it; local tests do not establish Unraid filesystem locking or power-loss durability.

For an isolated restore drill:

1. Keep the original server stopped. Preserve the backup unchanged and copy `config/`, `data/` and `staging/` into three new, separate writable directories. Preserve private permissions (directories 0700, files 0600) and provision the runtime UID/GID. Do not copy source `.serversend.lock` files; locks are recreated by startup.
2. Configure the restored instance with those three directories. For private-IP TLS, retain `config/tls` and the recorded IP settings. For external TLS, configure certificate/key paths pointing to copies of `external-tls/server.crt` and `server.key`. Keep its network isolated so it cannot be mistaken for the original.
3. Start the same image version, verify readiness, compare the server identity and trusted CA/certificate, sign in using the restored account/TOTP material, browse the catalog and download representative empty, small and multi-chunk files. Compare downloaded hashes with known originals.
4. Stop the drill instance before resuming the original. For an actual replacement, only start the replacement after the original is permanently stopped. Restored identity is for replacement; an independent clone needs a separate identity migration workflow, which is not implemented.

Take this stopped-server backup before upgrades. Unsupported newer schemas remain rejected. Downgrades require stopping the newer process and restoring the complete matching pre-upgrade backup with its original image. Do not run an older image against metadata already migrated by a newer image.

Local standard and private recovery drills verify identity, envelopes and restored bytes; private recovery uses an externally exported package. See [private storage](private-storage.md) for format and recovery limits. Live snapshots with GC suspension, independent format review, clone migration and actual Unraid qualification remain open roadmap gates. Native locks prevent cooperating processes sharing the same mounted volumes; they cannot detect copied volumes or an independent host running a copied identity. Storage directories must be protected against other local writers, and network filesystems are not supported by this procedure.
