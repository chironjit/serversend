package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/chironjit/serversend/shared/protocol"
)

type Profile struct {
	Scope       protocol.Scope
	DisplayName string
	IdentityKey []byte
	Endpoints   []string
}

// CreateProfile stores a previously verified identity and approved endpoints.
// Pairing must authenticate these values before calling this persistence layer.
// An existing trust pin cannot be replaced through this insertion operation.
func (r *Repository) CreateProfile(ctx context.Context, profile Profile) error {
	if err := profile.Scope.Validate(); err != nil {
		return err
	}
	if len(profile.Endpoints) == 0 || len(profile.Endpoints) > 16 {
		return errors.New("profiles require between 1 and 16 approved endpoints")
	}
	for _, origin := range profile.Endpoints {
		if err := protocol.ValidateOrigin(origin); err != nil {
			return err
		}
	}
	return r.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO profiles(server_id,account_id,display_name,identity_key) VALUES(?,?,?,?)`, profile.Scope.ServerID, profile.Scope.AccountID, profile.DisplayName, profile.IdentityKey)
		if err != nil {
			return err
		}
		for _, origin := range profile.Endpoints {
			if _, err := tx.ExecContext(ctx, `INSERT INTO endpoints(server_id,account_id,origin) VALUES(?,?,?)`, profile.Scope.ServerID, profile.Scope.AccountID, origin); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repository) GetProfile(ctx context.Context, scope protocol.Scope) (Profile, error) {
	if err := scope.Validate(); err != nil {
		return Profile{}, err
	}
	profile := Profile{Scope: scope}
	// Read pin and routes from one snapshot, even if another handle edits a profile.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Profile{}, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT display_name,identity_key FROM profiles WHERE server_id=? AND account_id=?`, scope.ServerID, scope.AccountID).Scan(&profile.DisplayName, &profile.IdentityKey); err != nil {
		return Profile{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT origin FROM endpoints WHERE server_id=? AND account_id=? ORDER BY origin`, scope.ServerID, scope.AccountID)
	if err != nil {
		return Profile{}, err
	}
	for rows.Next() {
		var origin string
		if err := rows.Scan(&origin); err != nil {
			rows.Close()
			return Profile{}, err
		}
		profile.Endpoints = append(profile.Endpoints, origin)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return Profile{}, err
	}
	if err := tx.Commit(); err != nil {
		return Profile{}, err
	}
	return profile, nil
}
