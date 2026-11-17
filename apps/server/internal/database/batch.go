package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

// BatchFile is one single-chunk (or empty) file whose object is already durable.
type BatchFile struct {
	Transfer
	Name     string
	Manifest []byte
	Chunk    *StoredChunk
}

// BatchOutcome reports one file. Inserted is false when the object was not
// referenced (a replay or a refused file), so the caller may remove it.
type BatchOutcome struct {
	Result   Transfer
	Err      error
	Inserted bool
}

// CommitBatch publishes files in one transaction, each as a committed upload
// with its version and catalog entry, exactly as Create, RecordChunk and
// CommitUpload would in sequence. A replayed key returns its recorded result.
// Refusals are per file; any other error rolls back the whole batch.
func (r *Repository) CommitBatch(ctx context.Context, account protocol.ID, files []BatchFile, capacity Capacity, now time.Time) ([]BatchOutcome, error) {
	var outcomes []BatchOutcome
	err := r.write(ctx, func(tx *sql.Tx) error {
		outcomes = make([]BatchOutcome, len(files))
		var total int64
		for _, file := range files {
			total += file.TransmittedBytes
		}
		if err := checkCapacity(ctx, tx, Transfer{AccountID: account, TransmittedBytes: total}, capacity); err != nil {
			return err
		}
		for i, file := range files {
			if _, err := tx.ExecContext(ctx, `SAVEPOINT batch_file`); err != nil {
				return err
			}
			result, inserted, err := commitBatchFile(ctx, tx, account, file, now)
			var refused api.Error
			if err != nil && !errors.As(err, &refused) {
				return err
			}
			if err != nil {
				if _, rollbackErr := tx.ExecContext(ctx, `ROLLBACK TO batch_file`); rollbackErr != nil {
					return rollbackErr
				}
			}
			if _, releaseErr := tx.ExecContext(ctx, `RELEASE batch_file`); releaseErr != nil {
				return releaseErr
			}
			outcomes[i] = BatchOutcome{Result: result, Err: err, Inserted: inserted}
		}
		return nil
	})
	return outcomes, err
}

func commitBatchFile(ctx context.Context, tx *sql.Tx, account protocol.ID, file BatchFile, now time.Time) (Transfer, bool, error) {
	existing, err := scanTransfer(tx.QueryRowContext(ctx, `SELECT `+transferColumns+` FROM uploads WHERE account_id=? AND idempotency_key=? AND file_id IS NOT NULL`, account, file.IdempotencyKey))
	if err == nil {
		var recorded []byte
		if existing.State == protocol.UploadCommitted || existing.State == protocol.UploadConflicted {
			err = tx.QueryRowContext(ctx, `SELECT manifest FROM versions WHERE account_id=? AND file_id=? AND id=?`, account, existing.FileID, existing.VersionID).Scan(&recorded)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return Transfer{}, false, err
			}
		}
		if string(recorded) != string(file.Manifest) || existing.FileID != file.FileID || existing.VersionID != file.VersionID {
			return Transfer{}, false, api.Error{Code: api.Conflict, Message: "This idempotency key was already used for a different upload"}
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Transfer{}, false, err
	}
	var found int
	if file.ParentID != "" {
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM directories WHERE account_id=? AND id=?`, account, file.ParentID).Scan(&found); err != nil {
			return Transfer{}, false, err
		}
		if found != 1 {
			return Transfer{}, false, api.Error{Code: api.NotFound, Message: "Destination folder no longer exists"}
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM directories WHERE account_id=?1 AND id=?2)+(SELECT count(*) FROM versions WHERE account_id=?1 AND file_id=?2 AND id=?3)`, account, file.FileID, file.VersionID).Scan(&found); err != nil {
		return Transfer{}, false, err
	}
	if found != 0 {
		return Transfer{}, false, api.Error{Code: api.Conflict, Message: "This file or version ID is already in use"}
	}
	t := file.Transfer
	t.AccountID, t.CreatedAt, t.ExpiresAt = account, now, now
	if err := tx.QueryRowContext(ctx, `UPDATE accounts SET catalog_revision=catalog_revision+1 WHERE id=? RETURNING catalog_revision`, account).Scan(&t.CatalogRevision); err != nil {
		return Transfer{}, false, err
	}
	var head uint64
	err = tx.QueryRowContext(ctx, `SELECT head_revision FROM files WHERE account_id=? AND id=?`, account, t.FileID).Scan(&head)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Transfer{}, false, err
	}
	t.State, t.HeadRevision = protocol.UploadConflicted, head
	if head == t.ExpectedHead {
		t.State, t.HeadRevision = protocol.UploadCommitted, t.CatalogRevision
		if _, err := tx.ExecContext(ctx, `INSERT INTO files(account_id,id,name,head_version_id,head_revision,parent_id) VALUES(?,?,?,?,?,?)
			ON CONFLICT(account_id,id) DO UPDATE SET name=excluded.name,head_version_id=excluded.head_version_id,head_revision=excluded.head_revision,parent_id=excluded.parent_id`, account, t.FileID, file.Name, t.VersionID, t.HeadRevision, t.ParentID); err != nil {
			return Transfer{}, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO uploads(account_id,id,state,mode,idempotency_key,expected_bytes,created_at,file_id,version_id,chunk_count,manifest_bytes,expected_head,expires_at,parent_id,result_catalog_revision,result_head_revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		account, t.ID, t.State, t.Mode, t.IdempotencyKey, t.TransmittedBytes, now.UnixMilli(), t.FileID, t.VersionID, t.ChunkCount, t.ManifestBytes, int64(t.ExpectedHead), now.UnixMilli(), t.ParentID, int64(t.CatalogRevision), int64(t.HeadRevision)); err != nil {
		return Transfer{}, false, err
	}
	if file.Chunk != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO chunks(account_id,upload_id,chunk_index,byte_count,digest,object_id) VALUES(?,?,0,?,?,?)`, account, t.ID, file.Chunk.Bytes, file.Chunk.Digest, file.Chunk.ObjectID); err != nil {
			return Transfer{}, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO versions(account_id,file_id,id,upload_id,manifest,total_bytes,created_at) VALUES(?,?,?,?,?,?,?)`, account, t.FileID, t.VersionID, t.ID, file.Manifest, t.TransmittedBytes, now.UnixMilli()); err != nil {
		return Transfer{}, false, err
	}
	return t, true, nil
}
