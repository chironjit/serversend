// Package sqlite provides the shared SQLite connection and migration policy.
// Product schemas remain in their owning server or desktop application.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// Open opens or creates an on-disk database without creating parent directories.
// Callers own Close and must place the database on a local filesystem supporting
// SQLite locking. The filename is a path, not a SQLite URI or DSN.
func Open(ctx context.Context, filename string) (*sql.DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(filename) || strings.ContainsRune(filename, '\x00') {
		return nil, errors.New("SQLite filename must be an absolute filesystem path")
	}
	filename = filepath.Clean(filename)
	parent, err := os.Stat(filepath.Dir(filename))
	if err != nil {
		return nil, fmt.Errorf("SQLite parent directory: %w", err)
	}
	if !parent.IsDir() {
		return nil, errors.New("SQLite parent path must be a directory")
	}
	if info, err := os.Stat(filename); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("SQLite filename must refer to a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("SQLite file: %w", err)
	}
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create SQLite file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close SQLite file: %w", err)
	}

	path := filepath.ToSlash(filename)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path // Windows drive paths use file:///C:/... URI form.
	}
	uri := url.URL{Scheme: "file", Path: path}
	settings := url.Values{
		"mode":          {"rwc"},
		"_busy_timeout": {"5000"},
		"_foreign_keys": {"on"},
		"_journal_mode": {"WAL"},
		"_synchronous":  {"FULL"},
		"_cache_size":   {"-2048"},
		"_txlock":       {"immediate"},
	}
	uri.RawQuery = settings.Encode()
	db, err := sql.Open("sqlite3", uri.String())
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	// Settings are in the DSN so replacement connections get the same policy.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize SQLite: %w", err)
	}
	return db, nil
}
