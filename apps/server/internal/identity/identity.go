// Package identity manages the server identity independently of its TLS
// certificate, and signs TLS-channel-bound pairing proofs.
package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/chironjit/serversend/shared/protocol"
)

// Identity deliberately exposes only public material. The seed remains in the
// configuration database and is only read by the signing operation.
type Identity struct {
	ID        string
	PublicKey [ed25519.PublicKeySize]byte
}

// LoadOrCreate serializes the initial insert with an immediate transaction.
// Corrupt existing material is an error, never permission to rotate identity.
func LoadOrCreate(ctx context.Context, db *sql.DB) (Identity, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Identity{}, fmt.Errorf("begin identity initialization: %w", err)
	}
	defer tx.Rollback()
	var id string
	var seed []byte
	err = tx.QueryRowContext(ctx, "SELECT server_id, key_seed FROM server_identity WHERE singleton = 1").Scan(&id, &seed)
	if errors.Is(err, sql.ErrNoRows) {
		var randomID [16]byte
		if _, err := rand.Read(randomID[:]); err != nil {
			return Identity{}, errors.New("generate server identifier")
		}
		id = hex.EncodeToString(randomID[:])
		seed = make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return Identity{}, errors.New("generate server identity key")
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO server_identity(singleton, server_id, key_seed) VALUES(1, ?, ?)", id, seed); err != nil {
			return Identity{}, fmt.Errorf("persist server identity: %w", err)
		}
	} else if err != nil {
		return Identity{}, fmt.Errorf("read server identity: %w", err)
	}
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != id || len(seed) != ed25519.SeedSize {
		return Identity{}, errors.New("stored server identity is invalid; restore the configuration backup")
	}
	key := ed25519.NewKeyFromSeed(seed)
	result := Identity{ID: id}
	copy(result.PublicKey[:], key[ed25519.SeedSize:])
	if err := tx.Commit(); err != nil {
		return Identity{}, fmt.Errorf("commit server identity: %w", err)
	}
	return result, nil
}

// Sign proves possession without exporting the persisted seed to callers.
func Sign(ctx context.Context, db *sql.DB, proof *protocol.IdentityProof) error {
	var seed []byte
	var id string
	if err := db.QueryRowContext(ctx, `SELECT server_id,key_seed FROM server_identity WHERE singleton=1`).Scan(&id, &seed); err != nil {
		return err
	}
	if len(seed) != ed25519.SeedSize {
		return errors.New("invalid persisted identity")
	}
	key := ed25519.NewKeyFromSeed(seed)
	proof.TrustedIdentity = protocol.TrustedIdentity{ServerID: protocol.ID(id), PublicKey: base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey))}
	message, err := proof.SigningBytes()
	if err != nil {
		return err
	}
	proof.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, message))
	return nil
}
