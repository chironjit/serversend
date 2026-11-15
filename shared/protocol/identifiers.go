package protocol

import (
	"encoding/hex"
	"errors"
	"strconv"
)

// ID is 128 random bits encoded as 32 lowercase hexadecimal characters.
// IDs identify resources; knowledge of an ID never grants access.
type ID string

func (id ID) Validate() error {
	if len(id) != 32 {
		return errors.New("identifier must contain 32 lowercase hexadecimal characters")
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return errors.New("identifier is not canonical lowercase hexadecimal")
		}
	}
	return nil
}

// Scope must accompany all locally cached remote identifiers and credentials.
type Scope struct {
	ServerID  ID `json:"server_id"`
	AccountID ID `json:"account_id"`
}

func (s Scope) Validate() error {
	if err := s.ServerID.Validate(); err != nil {
		return err
	}
	return s.AccountID.Validate()
}

type ResourceKey struct {
	Scope
	ID ID `json:"id"`
}

func (k ResourceKey) Validate() error {
	if err := k.Scope.Validate(); err != nil {
		return err
	}
	return k.ID.Validate()
}

// Revision is canonical unsigned decimal, serialized as a JSON string so JS
// consumers cannot silently round revisions. Zero means no existing head.
type Revision string

func (r Revision) Validate() error {
	n, err := strconv.ParseUint(string(r), 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != string(r) {
		return errors.New("revision must be a canonical unsigned decimal string")
	}
	return nil
}

func CompareRevisions(a, b Revision) (int, error) {
	if err := a.Validate(); err != nil {
		return 0, err
	}
	if err := b.Validate(); err != nil {
		return 0, err
	}
	x, _ := strconv.ParseUint(string(a), 10, 64)
	y, _ := strconv.ParseUint(string(b), 10, 64)
	if x < y {
		return -1, nil
	}
	if x > y {
		return 1, nil
	}
	return 0, nil
}

// Digest is SHA-256 of transmitted bytes, encoded as lowercase hexadecimal.
// A private object's digest describes ciphertext, never the original contents.
type Digest string

func (d Digest) Validate() error {
	decoded, err := hex.DecodeString(string(d))
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != string(d) {
		return errors.New("digest must be lowercase SHA-256 hexadecimal")
	}
	return nil
}
