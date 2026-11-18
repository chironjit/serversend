package database

import (
	"context"
	"database/sql"
	"github.com/chironjit/serversend/shared/protocol"
	"time"
)

type UserSummary struct {
	RecoveryKind string      `json:"recovery_kind"`
	StorageBytes int64       `json:"storage_bytes"`
	ID           protocol.ID `json:"id"`
	Username     string      `json:"username"`
	State        string      `json:"state"`
	Disabled     bool        `json:"disabled"`
	ExpiresAt    *time.Time  `json:"expires_at,omitempty"`
}

func (r *Repository) Users(ctx context.Context) ([]UserSummary, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT recovery_kind,id,username,state,disabled,temporary_expires_at,(SELECT coalesce(sum(total_bytes),0) FROM versions WHERE account_id=accounts.id) FROM accounts WHERE role='user' ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []UserSummary{}
	for rows.Next() {
		var u UserSummary
		var expires sql.NullInt64
		if err := rows.Scan(&u.RecoveryKind, &u.ID, &u.Username, &u.State, &u.Disabled, &expires, &u.StorageBytes); err != nil {
			return nil, err
		}
		if expires.Valid {
			t := time.UnixMilli(expires.Int64).UTC()
			u.ExpiresAt = &t
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
