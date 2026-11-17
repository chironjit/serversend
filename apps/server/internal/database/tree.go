package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/chironjit/serversend/apps/server/internal/layout"
	"github.com/chironjit/serversend/apps/server/internal/storage"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

// ErrLegacyFiles refuses metadata written for the pre-release object store.
var ErrLegacyFiles = errors.New("the metadata database contains files stored in the pre-release object layout; stop the server and run serversend -reset-files to remove all uploaded files (accounts are kept), or restore a matching backup")

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// dirPath returns the visible path of a folder; "" is the user's root.
func dirPath(ctx context.Context, q querier, account, dir protocol.ID) (string, error) {
	var username string
	if err := q.QueryRowContext(ctx, `SELECT username FROM accounts WHERE id=?`, account).Scan(&username); err != nil {
		return "", err
	}
	if dir == "" {
		return layout.Tree(username), nil
	}
	rows, err := q.QueryContext(ctx, `WITH RECURSIVE chain(id,parent_id,name,depth) AS (
		SELECT id,parent_id,name,0 FROM directories WHERE account_id=?1 AND id=?2
		UNION ALL SELECT d.id,d.parent_id,d.name,c.depth+1 FROM directories d JOIN chain c ON d.account_id=?1 AND d.id=c.parent_id WHERE c.depth<4096)
		SELECT name,parent_id FROM chain ORDER BY depth DESC`, account, dir)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	names := []string{}
	first := true
	for rows.Next() {
		var name, parent string
		if err := rows.Scan(&name, &parent); err != nil {
			return "", err
		}
		if first && parent != "" {
			return "", fmt.Errorf("folder %s has a missing ancestor", dir)
		}
		first = false
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", api.Error{Code: api.NotFound, Message: "Destination folder no longer exists"}
	}
	return layout.Tree(username, names...), nil
}

func filePath(ctx context.Context, q querier, account, parent protocol.ID, name string) (string, error) {
	dir, err := dirPath(ctx, q, account, parent)
	if err != nil {
		return "", err
	}
	return dir + "/" + name, nil
}

func journal(ctx context.Context, tx *sql.Tx, ops ...storage.Op) error {
	for _, op := range ops {
		if _, err := tx.ExecContext(ctx, `INSERT INTO storage_journal(kind,source,target) VALUES(?,?,?)`, op.Kind, op.Source, op.Target); err != nil {
			return err
		}
	}
	return nil
}

// Journal returns filesystem changes not yet applied, in order, with the last ID.
func (r *Repository) Journal(ctx context.Context) ([]storage.Op, int64, error) {
	r.journalMu.Lock()
	applied := r.applied
	r.journalMu.Unlock()
	rows, err := r.db.QueryContext(ctx, `SELECT id,kind,source,target FROM storage_journal WHERE id>? ORDER BY id`, applied)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var ops []storage.Op
	var last int64
	for rows.Next() {
		var op storage.Op
		if err := rows.Scan(&last, &op.Kind, &op.Source, &op.Target); err != nil {
			return nil, 0, err
		}
		ops = append(ops, op)
	}
	return ops, last, rows.Err()
}

// Applied records that changes through last are applied and flushed; the
// next write forgets them.
func (r *Repository) Applied(last int64) {
	r.journalMu.Lock()
	r.applied = max(r.applied, last)
	r.journalMu.Unlock()
}

// ClearJournal forgets every applied change now, as startup does.
func (r *Repository) ClearJournal(ctx context.Context) error {
	return r.write(ctx, func(*sql.Tx) error { return nil })
}

// publish makes a version whose bytes are at source the current version of
// its file at parent/name, or retains it as a conflicted version when the
// head moved. The previous head and any other visible file of that name are
// retained under the versions area; the other file leaves the catalog.
func publish(ctx context.Context, tx *sql.Tx, t *Transfer, name, source string) error {
	account := t.AccountID
	var oldParent protocol.ID
	var oldName string
	var oldHead protocol.ID
	var head uint64
	var hidden bool
	err := tx.QueryRowContext(ctx, `SELECT parent_id,name,head_version_id,head_revision,hidden FROM files WHERE account_id=? AND id=?`, account, t.FileID).Scan(&oldParent, &oldName, &oldHead, &head, &hidden)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := tx.QueryRowContext(ctx, `UPDATE accounts SET catalog_revision=catalog_revision+1 WHERE id=? RETURNING catalog_revision`, account).Scan(&t.CatalogRevision); err != nil {
		return err
	}
	t.State, t.HeadRevision = protocol.UploadConflicted, head
	if head != t.ExpectedHead {
		return journal(ctx, tx, storage.Op{Kind: "move", Source: source, Target: layout.Version(account, t.FileID, t.VersionID)})
	}
	var folders int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM directories WHERE account_id=? AND parent_id=? AND name=?`, account, t.ParentID, name).Scan(&folders); err != nil {
		return err
	}
	if folders != 0 {
		return api.Error{Code: api.Conflict, Message: "A folder with this name already exists here"}
	}
	target, err := filePath(ctx, tx, account, t.ParentID, name)
	if err != nil {
		return err
	}
	ops := []storage.Op{}
	if exists && !hidden {
		previous, err := filePath(ctx, tx, account, oldParent, oldName)
		if err != nil {
			return err
		}
		ops = append(ops, storage.Op{Kind: "move", Source: previous, Target: layout.Version(account, t.FileID, oldHead)})
	}
	var displaced, displacedHead protocol.ID
	err = tx.QueryRowContext(ctx, `SELECT id,head_version_id FROM files WHERE account_id=? AND parent_id=? AND name=? AND hidden=0 AND id<>?`, account, t.ParentID, name, t.FileID).Scan(&displaced, &displacedHead)
	if err == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE files SET hidden=1 WHERE account_id=? AND id=?`, account, displaced); err != nil {
			return err
		}
		ops = append(ops, storage.Op{Kind: "move", Source: target, Target: layout.Version(account, displaced, displacedHead)})
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	ops = append(ops, storage.Op{Kind: "move", Source: source, Target: target})
	t.State, t.HeadRevision = protocol.UploadCommitted, t.CatalogRevision
	if _, err := tx.ExecContext(ctx, `INSERT INTO files(account_id,id,name,head_version_id,head_revision,parent_id,hidden) VALUES(?,?,?,?,?,?,0)
		ON CONFLICT(account_id,id) DO UPDATE SET name=excluded.name,head_version_id=excluded.head_version_id,head_revision=excluded.head_revision,parent_id=excluded.parent_id,hidden=0`,
		account, t.FileID, name, t.VersionID, int64(t.HeadRevision), t.ParentID); err != nil {
		return err
	}
	return journal(ctx, tx, ops...)
}

// VersionLocation is where a retained version's bytes are: the visible tree
// for each visible file's head, and the versions area for everything else.
type VersionLocation struct {
	Path   string
	Bytes  int64
	Upload protocol.ID
}

func versionLocation(ctx context.Context, q querier, account, file, version protocol.ID) (VersionLocation, error) {
	var location VersionLocation
	var parent protocol.ID
	var name string
	var head protocol.ID
	var hidden bool
	err := q.QueryRowContext(ctx, `SELECT v.total_bytes,v.upload_id,f.parent_id,f.name,f.head_version_id,f.hidden FROM versions v JOIN files f ON f.account_id=v.account_id AND f.id=v.file_id
		WHERE v.account_id=? AND v.file_id=? AND v.id=?`, account, file, version).Scan(&location.Bytes, &location.Upload, &parent, &name, &head, &hidden)
	if err != nil {
		return location, err
	}
	if !hidden && head == version {
		location.Path, err = filePath(ctx, q, account, parent, name)
	} else {
		location.Path = layout.Version(account, file, version)
	}
	return location, err
}

func (r *Repository) VersionLocation(ctx context.Context, account, file, version protocol.ID) (VersionLocation, error) {
	return versionLocation(ctx, r.db, account, file, version)
}

// EachVersion visits every retained version's location, for startup checks.
func (r *Repository) EachVersion(ctx context.Context, visit func(VersionLocation) error) error {
	rows, err := r.db.QueryContext(ctx, `SELECT account_id,file_id,id FROM versions`)
	if err != nil {
		return err
	}
	type key struct{ account, file, version protocol.ID }
	var keys []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.account, &k.file, &k.version); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range keys {
		location, err := r.VersionLocation(ctx, k.account, k.file, k.version)
		if err != nil {
			return err
		}
		if err := visit(location); err != nil {
			return err
		}
	}
	return nil
}

// OpenUploadEnd reports whether an upload is open and the end of its
// furthest acknowledged chunk, for startup checks and cleanup.
func (r *Repository) OpenUploadEnd(ctx context.Context, upload protocol.ID) (bool, int64, error) {
	var open bool
	var end int64
	err := r.db.QueryRowContext(ctx, `SELECT u.state='open',coalesce((SELECT max(byte_offset+byte_count) FROM chunks c WHERE c.account_id=u.account_id AND c.upload_id=u.id),0) FROM uploads u WHERE u.id=?`, upload).Scan(&open, &end)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, nil
	}
	return open, end, err
}
