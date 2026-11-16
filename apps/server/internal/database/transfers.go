package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

var (
	ErrUploadClosed   = errors.New("upload is no longer open")
	ErrUploadConflict = errors.New("upload conflicts with recorded state")
)

// Transfer is the immutable upload intent plus its lifecycle result. Every
// query is scoped by owner: foreign and absent uploads are indistinguishable.
type Transfer struct {
	AccountID, ID, FileID, VersionID, ParentID protocol.ID
	Mode                                       protocol.StorageMode
	IdempotencyKey                             string
	ExpectedHead                               uint64
	ChunkCount                                 int64
	TransmittedBytes                           int64
	ManifestBytes                              int64
	State                                      protocol.UploadState
	CreatedAt, ExpiresAt                       time.Time
	CatalogRevision, HeadRevision              uint64
}

type StoredChunk struct {
	Index    int64
	Bytes    int64
	Digest   protocol.Digest
	ObjectID protocol.ID
}

type FileEntry struct {
	ID, HeadVersionID, ParentID protocol.ID
	Directory                   bool
	Name                        string
	HeadRevision                uint64
}

const transferColumns = `parent_id,account_id,id,file_id,version_id,mode,idempotency_key,expected_head,chunk_count,expected_bytes,manifest_bytes,state,created_at,expires_at,coalesce(result_catalog_revision,0),coalesce(result_head_revision,0)`

func scanTransfer(row interface{ Scan(...any) error }) (Transfer, error) {
	var t Transfer
	var created, expires int64
	err := row.Scan(&t.ParentID, &t.AccountID, &t.ID, &t.FileID, &t.VersionID, &t.Mode, &t.IdempotencyKey, &t.ExpectedHead, &t.ChunkCount, &t.TransmittedBytes, &t.ManifestBytes, &t.State, &created, &expires, &t.CatalogRevision, &t.HeadRevision)
	t.CreatedAt, t.ExpiresAt = time.UnixMilli(created).UTC(), time.UnixMilli(expires).UTC()
	return t, err
}

// CreateTransfer is idempotent by (account, key). A replay returns the
// recorded upload so the caller can compare intent; created reports a new row.
func (r *Repository) CreateTransfer(ctx context.Context, t Transfer) (Transfer, bool, error) {
	var recorded Transfer
	created := false
	err := r.write(ctx, func(tx *sql.Tx) error {
		existing, err := scanTransfer(tx.QueryRowContext(ctx, `SELECT `+transferColumns+` FROM uploads WHERE account_id=? AND idempotency_key=? AND file_id IS NOT NULL`, t.AccountID, t.IdempotencyKey))
		if err == nil {
			recorded = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var directory int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM directories WHERE account_id=? AND id=?`, t.AccountID, t.FileID).Scan(&directory); err != nil {
			return err
		}
		if directory != 0 {
			return api.Error{Code: api.Conflict, Message: "This ID is already used by a folder"}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO uploads(account_id,id,state,mode,idempotency_key,expected_bytes,created_at,file_id,version_id,chunk_count,manifest_bytes,expected_head,expires_at,parent_id) VALUES(?,?,'open',?,?,?,?,?,?,?,?,?,?,?)`,
			t.AccountID, t.ID, t.Mode, t.IdempotencyKey, t.TransmittedBytes, t.CreatedAt.UnixMilli(), t.FileID, t.VersionID, t.ChunkCount, t.ManifestBytes, int64(t.ExpectedHead), t.ExpiresAt.UnixMilli(), t.ParentID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO reservations(account_id,upload_id,bytes) VALUES(?,?,?)`, t.AccountID, t.ID, t.TransmittedBytes); err != nil {
			return err
		}
		recorded, created = t, true
		recorded.State = protocol.UploadOpen
		return nil
	})
	return recorded, created, err
}

func (r *Repository) Transfer(ctx context.Context, account, id protocol.ID) (Transfer, error) {
	return scanTransfer(r.db.QueryRowContext(ctx, `SELECT `+transferColumns+` FROM uploads WHERE account_id=? AND id=? AND file_id IS NOT NULL`, account, id))
}

func (r *Repository) StoredChunks(ctx context.Context, account, upload protocol.ID) ([]StoredChunk, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT chunk_index,byte_count,digest,object_id FROM chunks WHERE account_id=? AND upload_id=? ORDER BY chunk_index`, account, upload)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chunks []StoredChunk
	for rows.Next() {
		var c StoredChunk
		if err := rows.Scan(&c.Index, &c.Bytes, &c.Digest, &c.ObjectID); err != nil {
			return nil, err
		}
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}

func (r *Repository) StoredChunk(ctx context.Context, account, upload protocol.ID, index int64) (StoredChunk, error) {
	c := StoredChunk{Index: index}
	err := r.db.QueryRowContext(ctx, `SELECT byte_count,digest,object_id FROM chunks WHERE account_id=? AND upload_id=? AND chunk_index=?`, account, upload, index).Scan(&c.Bytes, &c.Digest, &c.ObjectID)
	return c, err
}

// RecordChunk acknowledges a durable object for an open upload. If the index
// was already acknowledged, the recorded chunk is returned with created false
// and the caller decides between an identical retry and a conflict.
func (r *Repository) RecordChunk(ctx context.Context, account, upload protocol.ID, chunk StoredChunk, now time.Time) (StoredChunk, bool, error) {
	recorded, created := chunk, false
	err := r.write(ctx, func(tx *sql.Tx) error {
		var open int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM uploads WHERE account_id=? AND id=? AND state='open' AND expires_at>? AND chunk_count>?`, account, upload, now.UnixMilli(), chunk.Index).Scan(&open); err != nil {
			return err
		}
		if open != 1 {
			return ErrUploadClosed
		}
		err := tx.QueryRowContext(ctx, `SELECT byte_count,digest,object_id FROM chunks WHERE account_id=? AND upload_id=? AND chunk_index=?`, account, upload, chunk.Index).Scan(&recorded.Bytes, &recorded.Digest, &recorded.ObjectID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		recorded, created = chunk, true
		_, err = tx.ExecContext(ctx, `INSERT INTO chunks(account_id,upload_id,chunk_index,byte_count,digest,object_id) VALUES(?,?,?,?,?,?)`, account, upload, chunk.Index, chunk.Bytes, chunk.Digest, chunk.ObjectID)
		return err
	})
	return recorded, created, err
}

// CommitUpload publishes a complete upload as an immutable version in one
// transaction. If the file's head moved since the client read it, the version
// is retained and the result is conflicted rather than replacing the head.
// A finished upload returns its recorded result unchanged.
func (r *Repository) CommitUpload(ctx context.Context, account, upload protocol.ID, name string, manifest []byte, now time.Time) (Transfer, error) {
	var result Transfer
	err := r.write(ctx, func(tx *sql.Tx) error {
		t, err := scanTransfer(tx.QueryRowContext(ctx, `SELECT `+transferColumns+` FROM uploads WHERE account_id=? AND id=? AND file_id IS NOT NULL`, account, upload))
		if err != nil {
			return err
		}
		if t.State == protocol.UploadCommitted || t.State == protocol.UploadConflicted {
			var recorded []byte
			if err := tx.QueryRowContext(ctx, `SELECT manifest FROM versions WHERE account_id=? AND file_id=? AND id=?`, account, t.FileID, t.VersionID).Scan(&recorded); err != nil {
				return err
			}
			if string(recorded) != string(manifest) {
				return ErrUploadConflict
			}
			result = t
			return nil
		}
		if t.State != protocol.UploadOpen || !t.ExpiresAt.After(now) {
			return ErrUploadClosed
		}
		var stored int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM chunks WHERE account_id=? AND upload_id=?`, account, upload).Scan(&stored); err != nil {
			return err
		}
		if stored != t.ChunkCount {
			return ErrUploadConflict
		}
		var revision, head uint64
		if err := tx.QueryRowContext(ctx, `UPDATE accounts SET catalog_revision=catalog_revision+1 WHERE id=? RETURNING catalog_revision`, account).Scan(&revision); err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `SELECT head_revision FROM files WHERE account_id=? AND id=?`, account, t.FileID).Scan(&head)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		t.State, t.CatalogRevision, t.HeadRevision = protocol.UploadConflicted, revision, head
		if head == t.ExpectedHead {
			t.State, t.HeadRevision = protocol.UploadCommitted, revision
			if _, err := tx.ExecContext(ctx, `INSERT INTO files(account_id,id,name,head_version_id,head_revision,parent_id) VALUES(?,?,?,?,?,?)
				ON CONFLICT(account_id,id) DO UPDATE SET name=excluded.name,head_version_id=excluded.head_version_id,head_revision=excluded.head_revision,parent_id=excluded.parent_id`, account, t.FileID, name, t.VersionID, revision, t.ParentID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO versions(account_id,file_id,id,upload_id,manifest,total_bytes,created_at) VALUES(?,?,?,?,?,?,?)`, account, t.FileID, t.VersionID, upload, manifest, t.TransmittedBytes, now.UnixMilli()); err != nil {
			return ErrUploadConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE uploads SET state=?,result_catalog_revision=?,result_head_revision=? WHERE account_id=? AND id=?`, t.State, int64(t.CatalogRevision), int64(t.HeadRevision), account, upload); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM reservations WHERE account_id=? AND upload_id=?`, account, upload)
		result = t
		return err
	})
	return result, err
}

// Catalog returns the account's files and revision from one snapshot. The
// slice has a flat namespace; directories and paging arrive with Chunk 10.
func (r *Repository) Catalog(ctx context.Context, account protocol.ID, parent protocol.ID, limit int) (uint64, []FileEntry, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	var revision uint64
	if err := tx.QueryRowContext(ctx, `SELECT catalog_revision FROM accounts WHERE id=?`, account).Scan(&revision); err != nil {
		return 0, nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,head_version_id,name,head_revision,parent_id,0 FROM files WHERE account_id=? AND parent_id=? UNION ALL SELECT id,'',name,revision,parent_id,1 FROM directories WHERE account_id=? AND parent_id=? ORDER BY id LIMIT ?`, account, parent, account, parent, limit)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	entries := []FileEntry{}
	for rows.Next() {
		var e FileEntry
		if err := rows.Scan(&e.ID, &e.HeadVersionID, &e.Name, &e.HeadRevision, &e.ParentID, &e.Directory); err != nil {
			return 0, nil, err
		}
		entries = append(entries, e)
	}
	return revision, entries, rows.Err()
}

// Version returns the exact stored manifest and the upload that owns its chunks.
func (r *Repository) Version(ctx context.Context, account, file, version protocol.ID) ([]byte, protocol.ID, error) {
	var manifest []byte
	var upload protocol.ID
	err := r.db.QueryRowContext(ctx, `SELECT manifest,upload_id FROM versions WHERE account_id=? AND file_id=? AND id=?`, account, file, version).Scan(&manifest, &upload)
	return manifest, upload, err
}
