package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/config"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/apps/server/internal/identity"
	"github.com/chironjit/serversend/apps/server/internal/instancelock"
	"github.com/chironjit/serversend/apps/server/internal/localtls"
	"github.com/chironjit/serversend/shared/sqlite"
)

// RestoreMode must be chosen explicitly: a replacement keeps the server
// identity and must only run once the original is permanently stopped; a
// clone receives a new identity and TLS material so both can run.
type RestoreMode string

const (
	Replace RestoreMode = "replace"
	Clone   RestoreMode = "clone"
)

// Restore copies a completed backup into the configured, empty config, data
// and staging directories while holding their deployment locks, and returns
// the resulting server ID. A replacement leaves the schema untouched so the
// matching (possibly older) image can serve it. A clone is migrated to this
// server's schema, gets a new identity, revokes every device and browser
// session, and discards the private CA. Clones refuse private-mode data,
// whose ciphertext is bound to the original server ID. On error the restored
// contents are removed again.
func Restore(ctx context.Context, c config.Config, source string, mode RestoreMode) (id string, err error) {
	if mode != Replace && mode != Clone {
		return "", errors.New("restore mode must be replace or clone")
	}
	if !filepath.IsAbs(source) {
		return "", errors.New("backup source must be absolute")
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return "", err
	}
	manifest, err := readManifest(source)
	if err != nil {
		return "", err
	}
	if err := c.CheckStorage(); err != nil {
		return "", err
	}
	targets := []struct{ name, path string }{
		{"config", c.ConfigDir}, {"data", c.DataDir}, {"staging", c.StagingDir},
	}
	for _, target := range targets {
		info, err := os.Lstat(filepath.Join(source, target.name))
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("backup is missing its %s directory", target.name)
		}
		forward, err := within(target.path, source)
		if err != nil {
			return "", err
		}
		backward, err := within(source, target.path)
		if err != nil || forward || backward {
			return "", errors.Join(err, errors.New("restore targets must be separate from the backup"))
		}
	}
	if mode == Clone && c.TLSCertFile != "" && manifest.ExternalTLS {
		original, _ := os.ReadFile(filepath.Join(source, "external-tls", "server.crt"))
		configured, err := os.ReadFile(c.TLSCertFile)
		if err != nil || bytes.Equal(original, configured) {
			return "", errors.Join(err, errors.New("a clone needs its own TLS certificate, not the original server's"))
		}
	}
	locks, err := instancelock.Acquire(c.ConfigDir, c.DataDir, c.StagingDir)
	if err != nil {
		return "", fmt.Errorf("stop the server before restoring: %w", err)
	}
	defer locks.Close()
	for _, target := range targets {
		entries, err := os.ReadDir(target.path)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			if entry.Name() != instancelock.Filename {
				return "", fmt.Errorf("restore target %s must be empty", target.path)
			}
		}
	}
	defer func() {
		if err != nil {
			for _, target := range targets {
				err = errors.Join(err, empty(target.path))
			}
		}
	}()
	for _, target := range targets {
		if err := copyVolume(ctx, filepath.Join(source, target.name), target.path, false); err != nil {
			return "", fmt.Errorf("restore %s: %w", target.name, err)
		}
	}
	if mode == Clone {
		id, err = clone(ctx, c.ConfigDir)
	} else {
		id, err = replace(ctx, c.ConfigDir)
	}
	if err != nil {
		return "", err
	}
	if manifest.ServerID != "" && mode == Replace && id != manifest.ServerID {
		return "", errors.New("restored server identity does not match the backup manifest")
	}
	return id, syncDirectory(c.ConfigDir)
}

func readManifest(source string) (Manifest, error) {
	var manifest Manifest
	filename := filepath.Join(source, "backup.json")
	if info, err := os.Lstat(filename); err != nil || !info.Mode().IsRegular() {
		return manifest, errors.New("backup is incomplete: backup.json is missing; never restore it")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Version < 1 || manifest.Version > manifestVersion {
		return manifest, errors.New("unsupported or damaged backup.json")
	}
	if manifest.SchemaVersion > database.LatestVersion() {
		return manifest, fmt.Errorf("backup schema %d is newer than this server supports (%d); restore it with its matching image", manifest.SchemaVersion, database.LatestVersion())
	}
	return manifest, nil
}

// replace only verifies the restored metadata; startup migrates it later.
func replace(ctx context.Context, configDir string) (string, error) {
	db, err := sqlite.OpenPrivate(ctx, filepath.Join(configDir, database.Filename))
	if err != nil {
		return "", err
	}
	defer db.Close()
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return "", err
	}
	if version < 1 || version > database.LatestVersion() {
		return "", fmt.Errorf("restored metadata schema %d is not restorable by this server", version)
	}
	id, err := serverID(ctx, db)
	if err == nil && id == "" {
		err = errors.New("restored metadata has no server identity")
	}
	return id, err
}

func clone(ctx context.Context, configDir string) (string, error) {
	db, err := database.Open(ctx, configDir)
	if err != nil {
		return "", fmt.Errorf("migrate restored metadata: %w", err)
	}
	defer db.Close()
	err = sqlite.Write(ctx, db, func(tx *sql.Tx) error {
		var private int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM vault_envelopes) OR EXISTS(SELECT 1 FROM uploads WHERE mode='private') OR EXISTS(SELECT 1 FROM private_directories)`).Scan(&private); err != nil {
			return err
		}
		if private != 0 {
			return errors.New("private-mode data is bound to the original server identity; restore it only as a replacement")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_sessions SET revoked_at=coalesce(revoked_at,?)`, time.Now().UnixMilli()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM auth_challenges; DELETE FROM server_identity`)
		return err
	})
	if err != nil {
		return "", err
	}
	created, err := identity.LoadOrCreate(ctx, db)
	if err != nil {
		return "", err
	}
	// Startup provisions a fresh private CA; the original's is discarded.
	if err := os.RemoveAll(filepath.Join(configDir, localtls.Directory)); err != nil {
		return "", err
	}
	return created.ID, nil
}

// empty removes a failed restore's contents but keeps the lock file.
func empty(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != instancelock.Filename {
			err = errors.Join(err, os.RemoveAll(filepath.Join(directory, entry.Name())))
		}
	}
	return err
}
