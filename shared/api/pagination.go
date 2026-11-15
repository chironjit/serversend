package api

import (
	"encoding/base64"
	"errors"

	"github.com/chironjit/serversend/shared/protocol"
)

const (
	DefaultPageSize = 100
	MaxPageSize     = 1000
	MaxCursorBytes  = 2048
)

// Cursor is unpadded base64url, opaque to clients. Servers authenticate and
// scope it to the account, query and snapshot; syntax validation is not trust.
type Cursor string

func (c Cursor) Validate() error {
	if c == "" {
		return nil
	}
	if len(c) > MaxCursorBytes {
		return errors.New("pagination cursor exceeds size limit")
	}
	b, err := base64.RawURLEncoding.DecodeString(string(c))
	if err != nil || base64.RawURLEncoding.EncodeToString(b) != string(c) {
		return errors.New("invalid pagination cursor")
	}
	return nil
}

type PageRequest struct {
	Limit  int    `json:"limit"`
	Cursor Cursor `json:"cursor,omitempty"`
}

func (p PageRequest) Validate() error {
	if p.Limit < 1 || p.Limit > MaxPageSize {
		return errors.New("page limit outside supported bounds")
	}
	return p.Cursor.Validate()
}

type Page struct {
	Snapshot   protocol.ID `json:"snapshot"`
	NextCursor Cursor      `json:"next_cursor,omitempty"`
}

func (p Page) Validate() error {
	if err := p.Snapshot.Validate(); err != nil {
		return err
	}
	return p.NextCursor.Validate()
}

func (p Page) validateCount(count int) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if count > MaxPageSize || count == 0 && p.NextCursor != "" {
		return errors.New("invalid page size or empty continuation")
	}
	return nil
}
