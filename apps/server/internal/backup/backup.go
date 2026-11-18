// Package backup creates stopped-server snapshots. Holding all deployment
// locks is the maintenance barrier: no cooperating server, cleanup worker or
// operator command can mutate the source while it is copied.
package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/config"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/apps/server/internal/instancelock"
	"github.com/chironjit/serversend/shared/sqlite"
)

// Manifest is written last, after every file is flushed. Its absence means the
// directory is incomplete and must never be restored as a successful backup.
// Version 2 adds the schema version and server ID; version 1 remains restorable.
type Manifest struct {
	Version       int       `json:"version"`
	CreatedAt     time.Time `json:"createdAt"`
	SchemaVersion int       `json:"schemaVersion,omitempty"`
	ServerID      string    `json:"serverId,omitempty"`
	TLSIPs        string    `json:"tlsIps,omitempty"`
	ExternalTLS   bool      `json:"externalTls"`
}

const manifestVersion = 2

// Create requires a new absolute destination under an existing trusted parent,
// outside all source volumes. It never overwrites an existing destination and
// never starts the server or runs schema migrations. Backups contain secrets
// and are private, unencrypted directories requiring protected offline storage.
func Create(ctx context.Context, c config.Config, destination string) error {
	if err := c.CheckStorage(); err != nil {
		return err
	}
	locks, err := instancelock.Acquire(c.ConfigDir, c.DataDir, c.StagingDir)
	if err != nil {
		return fmt.Errorf("stop the server before taking a backup: %w", err)
	}
	defer locks.Close()
	return CreateLocked(ctx, c, destination)
}

// CreateLocked is Create for a caller that already holds the deployment locks,
// such as startup taking a pre-upgrade backup before migrating.
func CreateLocked(ctx context.Context, c config.Config, destination string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(destination) {
		return errors.New("backup destination must be absolute")
	}
	destination = filepath.Clean(destination)
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return fmt.Errorf("backup parent must exist: %w", err)
	}
	destination = filepath.Join(parent, filepath.Base(destination))
	volumes := []struct{ name, path string }{
		{"config", c.ConfigDir}, {"data", c.DataDir}, {"staging", c.StagingDir},
	}
	for _, volume := range volumes {
		if inside, err := within(parent, volume.path); err != nil || inside {
			return errors.Join(err, errors.New("backup destination must be outside source volumes"))
		}
	}
	// Refuse an uninitialized deployment instead of creating an empty database.
	info, err := os.Lstat(filepath.Join(c.ConfigDir, database.Filename))
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("backup requires an existing regular metadata database")
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return fmt.Errorf("create new backup destination: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(destination)
		}
	}()
	for _, volume := range volumes {
		target := filepath.Join(destination, volume.name)
		if err := os.Mkdir(target, 0o700); err != nil {
			return err
		}
		if err := copyVolume(ctx, volume.path, target, volume.name == "config"); err != nil {
			return fmt.Errorf("backup %s: %w", volume.name, err)
		}
	}
	db, err := sqlite.OpenPrivate(ctx, filepath.Join(c.ConfigDir, database.Filename))
	if err != nil {
		return err
	}
	manifest := Manifest{Version: manifestVersion, CreatedAt: time.Now().UTC(), TLSIPs: c.TLSIPs, ExternalTLS: c.TLSCertFile != ""}
	err = db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&manifest.SchemaVersion)
	if err == nil {
		manifest.ServerID, err = serverID(ctx, db)
	}
	if err == nil {
		err = sqlite.Backup(ctx, db, filepath.Join(destination, "config", database.Filename))
	}
	closeErr := db.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return err
	}
	if manifest.ExternalTLS {
		directory := filepath.Join(destination, "external-tls")
		if err := os.Mkdir(directory, 0o700); err != nil {
			return err
		}
		for _, pair := range [][2]string{{c.TLSCertFile, "server.crt"}, {c.TLSKeyFile, "server.key"}} {
			if err := copyFile(ctx, pair[0], filepath.Join(directory, pair[1])); err != nil {
				return fmt.Errorf("backup external TLS: %w", err)
			}
		}
		if err := syncDirectory(directory); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := syncDirectory(destination); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	marker, err := os.OpenFile(filepath.Join(destination, "backup.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := marker.Write(append(data, '\n'))
	if err := errors.Join(writeErr, marker.Sync(), marker.Close()); err != nil {
		return err
	}
	return errors.Join(syncDirectory(filepath.Join(destination, "config")), syncDirectory(destination), syncDirectory(parent))
}

// copyVolume copies source's contents into the existing destination directory.
func copyVolume(ctx context.Context, source, destination string, metadata bool) error {
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == instancelock.Filename || metadata && (relative == database.Filename || relative == database.Filename+"-wal" || relative == database.Filename+"-shm" || relative == database.Filename+"-journal") {
			return nil
		}
		if relative == "." {
			return nil
		}
		if entry.IsDir() {
			return os.Mkdir(filepath.Join(destination, relative), 0o700)
		}
		if err := copyFile(ctx, path, filepath.Join(destination, relative)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Flush empty directories as well as directories containing files before
	// writing the completion marker. No per-file paths accumulate in memory.
	return filepath.WalkDir(destination, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return syncDirectory(path)
		}
		return nil
	})
}

func copyFile(ctx context.Context, source, destination string) (err error) {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup refuses links and special files: %s", source)
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("backup source changed while opening")
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	buffer := make([]byte, 128<<10)
	for {
		if err = ctx.Err(); err != nil {
			break
		}
		n, readErr := input.Read(buffer)
		if n > 0 {
			if _, err = output.Write(buffer[:n]); err != nil {
				break
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				err = readErr
			}
			break
		}
	}
	return errors.Join(err, output.Sync(), output.Close())
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

// within reports whether path is volume or lies beneath it. Comparing inode
// identity too means aliases and bind mounts cannot hide nesting.
func within(path, volume string) (bool, error) {
	info, err := os.Stat(volume)
	if err != nil {
		return false, err
	}
	for ancestor := path; ; ancestor = filepath.Dir(ancestor) {
		current, err := os.Stat(ancestor)
		if err != nil {
			return false, err
		}
		if os.SameFile(info, current) {
			return true, nil
		}
		if filepath.Dir(ancestor) == ancestor {
			return false, nil
		}
	}
}

func serverID(ctx context.Context, db *sql.DB) (id string, err error) {
	err = db.QueryRowContext(ctx, `SELECT server_id FROM server_identity WHERE singleton=1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}
