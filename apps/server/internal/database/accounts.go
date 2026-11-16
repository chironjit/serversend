package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/sqlite"
)

// Account contains no password hash or TOTP material. Authentication repositories
// in later chunks own access to those fields and authorized state transitions.
type Account struct {
	ID        protocol.ID
	Username  string
	Role      string
	Status    protocol.AccountStatus
	CreatedAt time.Time
}

// CreateAccount persists a newly issued temporary-password account. The caller
// supplies an encoded hash, never a plaintext password; hashing belongs to auth.
func CreateAccount(ctx context.Context, db *sql.DB, account Account, passwordHash string) error {
	if err := account.ID.Validate(); err != nil {
		return err
	}
	if account.CreatedAt.IsZero() || account.CreatedAt.UnixMilli() < 0 || passwordHash == "" {
		return errors.New("account creation time and password hash are required")
	}
	if account.Status != (protocol.AccountStatus{State: protocol.PasswordRequired}) {
		return errors.New("new accounts require password replacement and TOTP enrollment")
	}
	return sqlite.Write(ctx, db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO accounts(id,username,role,state,password_hash,created_at) VALUES(?,?,?,?,?,?)`, account.ID, account.Username, account.Role, account.Status.State, passwordHash, account.CreatedAt.UnixMilli())
		return err
	})
}

func GetAccount(ctx context.Context, db *sql.DB, id protocol.ID) (Account, error) {
	if err := id.Validate(); err != nil {
		return Account{}, err
	}
	var account Account
	var millis int64
	err := db.QueryRowContext(ctx, `SELECT id,username,role,state,totp_enrolled,disabled,created_at FROM accounts WHERE id=?`, id).Scan(&account.ID, &account.Username, &account.Role, &account.Status.State, &account.Status.TOTPEnrolled, &account.Status.Disabled, &millis)
	if err != nil {
		return Account{}, err
	}
	account.CreatedAt = time.UnixMilli(millis).UTC()
	return account, nil
}
