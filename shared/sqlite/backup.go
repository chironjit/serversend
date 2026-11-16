package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// Backup writes a consistent metadata-only snapshot, including committed WAL
// contents, to a new absolute filename in an existing private directory. It never
// overwrites a backup. The destination must not be used until this call succeeds.
// Callers coordinate referenced blobs/keys and pre-upgrade retention separately.
func Backup(ctx context.Context, db *sql.DB, filename string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if db == nil || !filepath.IsAbs(filename) {
		return errors.New("database and absolute backup filename are required")
	}
	// Reserve the name exclusively. VACUUM INTO accepts an existing empty file.
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(filename)
		}
	}()
	if err = file.Close(); err != nil {
		return err
	}
	// SQLite produces a consistent snapshot without copying a live main file.
	if _, err = db.ExecContext(ctx, "VACUUM main INTO ?", filename); err != nil {
		return err
	}
	file, err = os.OpenFile(filename, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err = errors.Join(file.Sync(), file.Close()); err != nil {
		return err
	}
	// Windows directory flushing requires separate native qualification.
	if runtime.GOOS == "windows" {
		return nil
	}
	parent, err := os.Open(filepath.Dir(filename))
	if err != nil {
		return err
	}
	return errors.Join(parent.Sync(), parent.Close())
}
