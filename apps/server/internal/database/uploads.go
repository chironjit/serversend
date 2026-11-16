package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/sqlite"
)

// Upload is only the durable upload/reservation foundation. Later upload logic
// authorizes requests, enforces capacity, and owns idempotent lifecycle replay.
type Upload struct {
	AccountID      protocol.ID
	ID             protocol.ID
	State          protocol.UploadState
	Mode           string
	IdempotencyKey string
	ExpectedBytes  int64
	CreatedAt      time.Time
}

// CreateUpload records both the initial upload and its reservation atomically.
// The caller must enforce account authorization and available capacity.
func CreateUpload(ctx context.Context, db *sql.DB, upload Upload) error {
	if err := upload.AccountID.Validate(); err != nil {
		return err
	}
	if err := upload.ID.Validate(); err != nil {
		return err
	}
	if upload.State != protocol.UploadOpen || upload.CreatedAt.IsZero() || upload.CreatedAt.UnixMilli() < 0 {
		return errors.New("new uploads must be open with a creation time")
	}
	return sqlite.Write(ctx, db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO uploads(account_id,id,state,mode,idempotency_key,expected_bytes,created_at) VALUES(?,?,?,?,?,?,?)`, upload.AccountID, upload.ID, upload.State, upload.Mode, upload.IdempotencyKey, upload.ExpectedBytes, upload.CreatedAt.UnixMilli())
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO reservations(account_id,upload_id,bytes) VALUES(?,?,?)`, upload.AccountID, upload.ID, upload.ExpectedBytes)
		return err
	})
}

// GetUpload always scopes by owner; absent and foreign uploads both return
// sql.ErrNoRows. Knowledge of an ID does not grant access to another owner.
func GetUpload(ctx context.Context, db *sql.DB, accountID, id protocol.ID) (Upload, error) {
	if err := accountID.Validate(); err != nil {
		return Upload{}, err
	}
	if err := id.Validate(); err != nil {
		return Upload{}, err
	}
	var upload Upload
	var millis int64
	err := db.QueryRowContext(ctx, `SELECT account_id,id,state,mode,idempotency_key,expected_bytes,created_at FROM uploads WHERE account_id=? AND id=?`, accountID, id).Scan(&upload.AccountID, &upload.ID, &upload.State, &upload.Mode, &upload.IdempotencyKey, &upload.ExpectedBytes, &millis)
	if err != nil {
		return Upload{}, err
	}
	upload.CreatedAt = time.UnixMilli(millis).UTC()
	return upload, nil
}
