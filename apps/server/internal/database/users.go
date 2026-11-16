package database

import (
	"context"
	"github.com/chironjit/serversend/shared/protocol"
	"time"
)

type UserSummary struct {
	ID                protocol.ID `json:"id"`
	Username          string      `json:"username"`
	State             string      `json:"state"`
	Disabled          bool        `json:"disabled"`
	ExpiresAt         *time.Time  `json:"expires_at,omitempty"`
	TemporaryPassword string      `json:"temporary_password,omitempty"`
	Protected         []byte      `json:"-"`
}

func (r *Repository) Users(ctx context.Context, now time.Time) ([]UserSummary, error) {
	// Expired credentials are irreversibly removed, including from later reads.
	if _, err := r.db.ExecContext(ctx, `UPDATE accounts SET temporary_password=NULL WHERE temporary_expires_at<=? AND temporary_password IS NOT NULL`, now.UnixMilli()); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,username,state,disabled,coalesce(temporary_expires_at,0),temporary_password FROM accounts WHERE role='user' ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []UserSummary{}
	for rows.Next() {
		var u UserSummary
		var expires int64
		if err := rows.Scan(&u.ID, &u.Username, &u.State, &u.Disabled, &expires, &u.Protected); err != nil {
			return nil, err
		}
		if expires > 0 {
			t := time.UnixMilli(expires).UTC()
			u.ExpiresAt = &t
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
