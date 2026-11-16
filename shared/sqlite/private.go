package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// OpenPrivate requires an existing protected application directory and refuses
// symlinks at the database filename. On Windows the caller must provision the
// directory ACL; POSIX mode bits cannot establish Windows privacy. Trusted parent
// directories are required; this check is not a sandbox against local races.
func OpenPrivate(ctx context.Context, filename string) (*sql.DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(filename) {
		return nil, errors.New("database filename must be absolute")
	}
	info, err := os.Stat(filepath.Dir(filename))
	if err != nil || !info.IsDir() {
		return nil, errors.New("database directory must already exist")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("database directory must be private (mode 0700)")
	}
	info, err = os.Lstat(filename)
	if err == nil && (!info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return nil, errors.New("database must be a private regular file (mode 0600)")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect database: %w", err)
	}
	return Open(ctx, filename)
}
