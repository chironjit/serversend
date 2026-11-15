package api

import (
	"errors"
	"strings"

	"github.com/chironjit/serversend/shared/protocol"
)

const MaxEventBytes = 16 * 1024

type EventType string

const (
	CatalogChanged    EventType = "catalog_changed"
	SessionRevoked    EventType = "session_revoked"
	StatisticsChanged EventType = "statistics_changed"
)

// Event is an account-scoped hint. It never contains private names or keys.
// Unknown event types may be ignored; sequence gaps require reconciliation.
type Event struct {
	Sequence        protocol.Revision `json:"sequence"`
	Type            EventType         `json:"type"`
	CatalogRevision protocol.Revision `json:"catalog_revision,omitempty"`
}

func (e Event) Validate() error {
	if err := e.Sequence.Validate(); err != nil {
		return err
	}
	if e.Sequence == "0" || strings.TrimSpace(string(e.Type)) == "" {
		return errors.New("event sequence and type are required")
	}
	if e.Type == CatalogChanged && e.CatalogRevision == "" {
		return errors.New("catalog event requires a revision")
	}
	if e.CatalogRevision != "" {
		return e.CatalogRevision.Validate()
	}
	return nil
}
