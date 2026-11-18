package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/chironjit/serversend/apps/server/internal/layout"
	"github.com/chironjit/serversend/apps/server/internal/storage"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"strconv"
	"time"
)

// activeEntry follows opaque parents, refusing mutations anywhere inside trash.
func activeEntry(ctx context.Context, q querier, account, id protocol.ID) error {
	if id == "" {
		return nil
	}
	var count int
	err := q.QueryRowContext(ctx, `WITH RECURSIVE chain(id) AS (SELECT ?2 UNION SELECT parent_id FROM directories JOIN chain USING(id) WHERE account_id=?1 UNION SELECT parent_id FROM files JOIN chain USING(id) WHERE account_id=?1) SELECT count(*) FROM trash_entries WHERE account_id=?1 AND id IN chain`, account, id).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return api.Error{Code: api.Conflict, Message: "Restore this item from trash first"}
	}
	return nil
}
func lifecycleAudit(ctx context.Context, tx *sql.Tx, account protocol.ID, kind string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO storage_audit(account_id,kind,created_at) VALUES(?,?,?)`, account, kind, now.UnixMilli())
	return err
}
func (r *Repository) TrashEntry(ctx context.Context, account, id protocol.ID, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		var parent protocol.ID
		var name string
		var directory bool
		err := tx.QueryRowContext(ctx, `SELECT parent_id,name,0 FROM files WHERE account_id=?1 AND id=?2 AND hidden=0 UNION ALL SELECT parent_id,name,1 FROM directories WHERE account_id=?1 AND id=?2`, account, id).Scan(&parent, &name, &directory)
		if err != nil {
			return err
		}
		if err = activeEntry(ctx, tx, account, id); err != nil {
			return err
		}
		source, err := filePath(ctx, tx, account, parent, name)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO trash_entries VALUES(?,?,?,?,?,?)`, account, id, parent, name, directory, now.UnixMilli()); err != nil {
			return err
		}
		table := "files"
		if directory {
			table = "directories"
		}
		if _, err = tx.ExecContext(ctx, `UPDATE `+table+` SET parent_id=? WHERE account_id=? AND id=?`, id, account, id); err != nil {
			return err
		}
		target := ".serversend/recoverable/" + string(account) + "/" + string(id) + "/" + name
		if err = journal(ctx, tx, storage.Op{Kind: "move", Source: source, Target: target}); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE accounts SET catalog_revision=catalog_revision+1 WHERE id=?`, account); err != nil {
			return err
		}
		return lifecycleAudit(ctx, tx, account, "trash", now)
	})
}
func (r *Repository) RestoreTrash(ctx context.Context, account, id protocol.ID, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		var parent protocol.ID
		var name string
		var directory bool
		if err := tx.QueryRowContext(ctx, `SELECT parent_id,name,directory FROM trash_entries WHERE account_id=? AND id=?`, account, id).Scan(&parent, &name, &directory); err != nil {
			return err
		}
		if err := activeEntry(ctx, tx, account, parent); err != nil {
			return err
		}
		if parent != "" {
			var found int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM directories WHERE account_id=? AND id=?`, account, parent).Scan(&found); err != nil {
				return err
			}
			if found == 0 {
				parent = ""
			}
		}
		target, err := filePath(ctx, tx, account, parent, name)
		if err != nil {
			return err
		}
		var occupied int
		if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM files WHERE account_id=?1 AND parent_id=?2 AND name=?3 AND hidden=0)+(SELECT count(*) FROM directories WHERE account_id=?1 AND parent_id=?2 AND name=?3)`, account, parent, name).Scan(&occupied); err != nil {
			return err
		}
		if occupied > 0 {
			return api.Error{Code: api.Conflict, Message: "The original name is occupied; move or trash that item first"}
		}
		table := "files"
		if directory {
			table = "directories"
		}
		var rev uint64
		if err = tx.QueryRowContext(ctx, `UPDATE accounts SET catalog_revision=catalog_revision+1 WHERE id=? RETURNING catalog_revision`, account).Scan(&rev); err != nil {
			return err
		}
		column := "head_revision"
		if directory {
			column = "revision"
		}
		if _, err = tx.ExecContext(ctx, `UPDATE `+table+` SET parent_id=?,`+column+`=? WHERE account_id=? AND id=?`, parent, rev, account, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM trash_entries WHERE account_id=? AND id=?`, account, id); err != nil {
			return err
		}
		if err = journal(ctx, tx, storage.Op{Kind: "move", Source: ".serversend/recoverable/" + string(account) + "/" + string(id) + "/" + name, Target: target}); err != nil {
			return err
		}
		return lifecycleAudit(ctx, tx, account, "trash_restore", now)
	})
}
func (r *Repository) TrashPage(ctx context.Context, account, after protocol.ID, limit int) ([]api.CatalogEntry, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT t.id,t.parent_id,t.name,t.directory,coalesce(f.head_version_id,''),coalesce(f.head_revision,d.revision) FROM trash_entries t LEFT JOIN files f ON f.account_id=t.account_id AND f.id=t.id LEFT JOIN directories d ON d.account_id=t.account_id AND d.id=t.id WHERE t.account_id=? AND t.id>? ORDER BY t.id LIMIT ?`, account, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []api.CatalogEntry{}
	for rows.Next() {
		var e api.CatalogEntry
		var dir bool
		var name string
		var rev uint64
		if err = rows.Scan(&e.ID, &e.ParentID, &name, &dir, &e.HeadVersionID, &rev); err != nil {
			return nil, err
		}
		e.Kind = api.File
		if dir {
			e.Kind = api.Directory
		}
		e.Mode = protocol.Standard
		e.Trashed = true
		e.HeadRevision = protocol.Revision(strconv.FormatUint(rev, 10))
		e.Standard = &protocol.StandardMetadata{Name: name}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
func (r *Repository) History(ctx context.Context, account, file, after protocol.ID, limit int) ([]api.VersionInfo, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT v.id,v.total_bytes,v.manifest,v.created_at,coalesce(u.state='conflicted' AND v.resolved=0,0) FROM versions v LEFT JOIN uploads u ON u.account_id=v.account_id AND u.id=v.upload_id WHERE v.account_id=? AND v.file_id=? AND v.id>? ORDER BY v.id LIMIT ?`, account, file, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []api.VersionInfo{}
	for rows.Next() {
		v := api.VersionInfo{FileID: file, Mode: protocol.Standard}
		var manifest []byte
		var created int64
		if err = rows.Scan(&v.ID, &v.TransmittedBytes, &manifest, &created, &v.Conflicted); err != nil {
			return nil, err
		}
		d := sha256.Sum256(manifest)
		v.ManifestSHA256 = protocol.Digest(hex.EncodeToString(d[:]))
		v.CreatedAt = time.UnixMilli(created).UTC()
		result = append(result, v)
	}
	return result, rows.Err()
}
func (r *Repository) SelectVersion(ctx context.Context, account, file, version protocol.ID, expected uint64, now time.Time) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		if err := activeEntry(ctx, tx, account, file); err != nil {
			return err
		}
		var parent, head protocol.ID
		var name string
		var rev uint64
		var hidden bool
		if err := tx.QueryRowContext(ctx, `SELECT parent_id,name,head_version_id,head_revision,hidden FROM files WHERE account_id=? AND id=?`, account, file).Scan(&parent, &name, &head, &rev, &hidden); err != nil {
			return err
		}
		if rev != expected || hidden {
			return api.Error{Code: api.Conflict, Message: "The file changed; refresh history before selecting a version"}
		}
		var found int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM versions WHERE account_id=? AND file_id=? AND id=?`, account, file, version).Scan(&found); err != nil {
			return err
		}
		if found == 0 {
			return sql.ErrNoRows
		}
		if _, err := tx.ExecContext(ctx, `UPDATE versions SET resolved=1 WHERE account_id=? AND file_id=? AND id=?`, account, file, version); err != nil {
			return err
		}
		if head == version {
			return nil
		}
		target, err := filePath(ctx, tx, account, parent, name)
		if err != nil {
			return err
		}
		if err = tx.QueryRowContext(ctx, `UPDATE accounts SET catalog_revision=catalog_revision+1 WHERE id=? RETURNING catalog_revision`, account).Scan(&rev); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE files SET head_version_id=?,head_revision=? WHERE account_id=? AND id=?`, version, rev, account, file); err != nil {
			return err
		}
		if err = journal(ctx, tx, storage.Op{Kind: "move", Source: target, Target: layout.Version(account, file, head)}, storage.Op{Kind: "move", Source: layout.Version(account, file, version), Target: target}); err != nil {
			return err
		}
		return lifecycleAudit(ctx, tx, account, "version_restore", now)
	})
}
func (r *Repository) Retention(ctx context.Context) (p api.RetentionPolicy, err error) {
	err = r.db.QueryRowContext(ctx, `SELECT trash_days,version_days FROM retention_policy WHERE id=1`).Scan(&p.TrashDays, &p.VersionDays)
	return
}
func (r *Repository) SetRetention(ctx context.Context, p api.RetentionPolicy) error {
	if p.Validate() != nil {
		return api.Error{Code: api.InvalidRequest, Message: "Retention days are out of range"}
	}
	_, err := r.db.ExecContext(ctx, `UPDATE retention_policy SET trash_days=?,version_days=? WHERE id=1`, p.TrashDays, p.VersionDays)
	return err
}
func (r *Repository) Collect(ctx context.Context, now time.Time) error {
	p, err := r.Retention(ctx)
	if err != nil {
		return err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT account_id,id FROM trash_entries WHERE deleted_at<=? ORDER BY deleted_at LIMIT 100`, now.Add(-time.Duration(p.TrashDays)*24*time.Hour).UnixMilli())
	if err != nil {
		return err
	}
	type key struct{ a, f, v protocol.ID }
	var keys []key
	for rows.Next() {
		var k key
		if err = rows.Scan(&k.a, &k.f); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, k)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err = r.PurgeTrash(ctx, k.a, k.f, now); err != nil {
			return err
		}
	}
	if p.VersionDays == 0 {
		return nil
	}
	return r.write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT v.account_id,v.file_id,v.id FROM versions v JOIN files f ON f.account_id=v.account_id AND f.id=v.file_id WHERE v.id<>f.head_version_id AND v.created_at<=? AND NOT EXISTS(SELECT 1 FROM uploads u WHERE u.account_id=v.account_id AND u.id=v.upload_id AND u.state='conflicted' AND v.resolved=0) LIMIT 100`, now.Add(-time.Duration(p.VersionDays)*24*time.Hour).UnixMilli())
		if err != nil {
			return err
		}
		keys = nil
		for rows.Next() {
			var k key
			if err = rows.Scan(&k.a, &k.f, &k.v); err != nil {
				rows.Close()
				return err
			}
			keys = append(keys, k)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, k := range keys {
			if err = journal(ctx, tx, storage.Op{Kind: "remove", Target: layout.Version(k.a, k.f, k.v)}); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM chunks WHERE account_id=?1 AND upload_id=(SELECT upload_id FROM versions WHERE account_id=?1 AND file_id=?2 AND id=?3) AND NOT EXISTS(SELECT 1 FROM versions v WHERE v.account_id=?1 AND v.upload_id=chunks.upload_id AND (v.file_id<>?2 OR v.id<>?3))`, k.a, k.f, k.v); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM versions WHERE account_id=? AND file_id=? AND id=?`, k.a, k.f, k.v); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE accounts SET catalog_revision=catalog_revision+1 WHERE id=?`, k.a); err != nil {
				return err
			}
			if err = lifecycleAudit(ctx, tx, k.a, "version_expired", now); err != nil {
				return err
			}
		}
		return nil
	})
}
func (r *Repository) PurgeTrash(ctx context.Context, account, id protocol.ID, now time.Time) error {
	return r.DeleteEntry(ctx, account, id, now)
}

func (r *Repository) UploadTarget(ctx context.Context, account, parent protocol.ID, name string) (*api.CatalogEntry, error) {
	if err := r.CheckDirectory(ctx, account, parent); err != nil {
		return nil, err
	}
	if err := activeEntry(ctx, r.db, account, parent); err != nil {
		return nil, err
	}
	e := api.CatalogEntry{ParentID: parent, Kind: api.File, Mode: protocol.Standard, Standard: &protocol.StandardMetadata{Name: name}}
	var rev uint64
	err := r.db.QueryRowContext(ctx, `SELECT id,head_version_id,head_revision FROM files WHERE account_id=? AND parent_id=? AND name=? AND hidden=0`, account, parent, name).Scan(&e.ID, &e.HeadVersionID, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	e.HeadRevision = protocol.Revision(strconv.FormatUint(rev, 10))
	return &e, err
}
