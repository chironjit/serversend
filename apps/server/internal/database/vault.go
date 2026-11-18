package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/vault"
)

func (r *Repository) Vault(ctx context.Context, account protocol.ID) (vault.Envelope, error) {
	var body []byte
	var e vault.Envelope
	err := r.db.QueryRowContext(ctx, `SELECT envelope FROM vault_envelopes WHERE account_id=?`, account).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return e, api.Error{Code: api.NotFound, Message: "No vault exists for this account"}
	}
	if err == nil {
		err = json.Unmarshal(body, &e)
	}
	return e, err
}
func (r *Repository) PutVault(ctx context.Context, account protocol.ID, e vault.Envelope) error {
	if e.Validate() != nil || e.AccountID != account {
		return api.Error{Code: api.InvalidRequest, Message: "Invalid vault envelope"}
	}
	body, _ := json.Marshal(e)
	return r.write(ctx, func(tx *sql.Tx) error {
		var prior []byte
		err := tx.QueryRowContext(ctx, `SELECT envelope FROM vault_envelopes WHERE account_id=?`, account).Scan(&prior)
		if errors.Is(err, sql.ErrNoRows) && e.Revision == 1 {
			_, err = tx.ExecContext(ctx, `INSERT INTO vault_envelopes VALUES(?,?,?)`, account, e.Revision, body)
			return err
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var old vault.Envelope
		_ = json.Unmarshal(prior, &old)
		if old.Revision+1 != e.Revision || old.VaultID != e.VaultID || old.Scope != e.Scope || string(old.Recovery) != string(e.Recovery) || string(old.Check) != string(e.Check) {
			return api.Error{Code: api.Conflict, Message: "Vault changed; refresh before rewrapping"}
		}
		_, err = tx.ExecContext(ctx, `UPDATE vault_envelopes SET revision=?,envelope=? WHERE account_id=?`, e.Revision, body, account)
		return err
	})
}

// PrivateCatalog enriches opaque tree entries with their encrypted metadata.
func (r *Repository) PrivateCatalog(ctx context.Context, account protocol.ID, e *api.CatalogEntry) error {
	var body []byte
	if e.Kind == api.Directory {
		err := r.db.QueryRowContext(ctx, `SELECT envelope FROM private_directories WHERE account_id=? AND id=?`, account, e.ID).Scan(&body)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var p protocol.PrivateEnvelope
		if err = json.Unmarshal(body, &p); err != nil {
			return err
		}
		e.Private = &p
	} else {
		raw, _, err := r.Version(ctx, account, e.ID, e.HeadVersionID)
		if err != nil {
			return err
		}
		var m protocol.Manifest
		if err = json.Unmarshal(raw, &m); err != nil {
			return err
		}
		e.Private = m.Private
	}
	if e.Private != nil {
		e.Mode = protocol.Private
		e.Standard = nil
	}
	return nil
}
