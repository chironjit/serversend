// Package backup creates stopped-server snapshots. Holding all deployment
// locks is the maintenance barrier: no cooperating server, cleanup worker or
// operator command can mutate the source while it is copied.
package backup

import (
	"context"
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
type Manifest struct {
	Version     int       `json:"version"`
	CreatedAt   time.Time `json:"createdAt"`
	TLSIPs      string    `json:"tlsIps,omitempty"`
	ExternalTLS bool      `json:"externalTls"`
}

// Create requires a new absolute destination under an existing trusted parent,
// outside all source volumes. It never overwrites an existing destination and
// never starts the server or runs schema migrations. Backups contain secrets
// and are private, unencrypted directories requiring protected offline storage.
func Create(ctx context.Context, c config.Config, destination string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.CheckStorage(); err != nil {
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
		info, err := os.Stat(volume.path)
		if err != nil {
			return err
		}
		// Compare inode identity too, so aliases/bind mounts cannot hide nesting.
		for ancestor := parent; ; ancestor = filepath.Dir(ancestor) {
			current, err := os.Stat(ancestor)
			if err != nil {
				return err
			}
			if os.SameFile(info, current) {
				return errors.New("backup destination must be outside source volumes")
			}
			if filepath.Dir(ancestor) == ancestor {
				break
			}
		}
	}
	locks, err := instancelock.Acquire(c.ConfigDir, c.DataDir, c.StagingDir)
	if err != nil {
		return fmt.Errorf("stop the server before taking a backup: %w", err)
	}
	defer locks.Close()
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
		if err := copyVolume(ctx, volume.path, filepath.Join(destination, volume.name), volume.name == "config"); err != nil {
			return fmt.Errorf("backup %s: %w", volume.name, err)
		}
	}
	db, err := sqlite.OpenPrivate(ctx, filepath.Join(c.ConfigDir, database.Filename))
	if err != nil {
		return err
	}
	err = sqlite.Backup(ctx, db, filepath.Join(destination, "config", database.Filename))
	closeErr := db.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return err
	}
	manifest := Manifest{Version: 1, CreatedAt: time.Now().UTC(), TLSIPs: c.TLSIPs, ExternalTLS: c.TLSCertFile != ""}
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
