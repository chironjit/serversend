package uploads

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

const catalogSnapshotLifetime = 5 * time.Minute

type catalogCursor struct {
	Kind     string      `json:"kind,omitempty"`
	Instance protocol.ID `json:"instance"`
	Account  protocol.ID `json:"account"`
	Parent   protocol.ID `json:"parent"`
	After    protocol.ID `json:"after"`
	Snapshot protocol.ID `json:"snapshot"`
	Revision uint64      `json:"revision"`
	Limit    int         `json:"limit"`
	Expires  int64       `json:"expires"`
}

func (s *Service) sealCatalogCursor(cursor catalogCursor) api.Cursor {
	body, _ := json.Marshal(cursor)
	mac := hmac.New(sha256.New, s.catalogKey[:])
	mac.Write(body)
	return api.Cursor(base64.RawURLEncoding.EncodeToString(append(body, mac.Sum(nil)...)))
}

func (s *Service) openCatalogCursor(value api.Cursor, account, parent protocol.ID, limit int) (catalogCursor, error) {
	invalid := api.Error{Code: api.InvalidRequest, Message: "Invalid catalog continuation for this account, folder or page size"}
	expired := api.Error{Code: api.SnapshotExpired, Message: "The catalog snapshot expired; refresh this folder"}
	var cursor catalogCursor
	if value == "" || value.Validate() != nil {
		return cursor, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(string(value))
	if err != nil || len(raw) <= sha256.Size {
		return cursor, invalid
	}
	body, signature := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	if json.Unmarshal(body, &cursor) != nil || cursor.Instance.Validate() != nil {
		return cursor, invalid
	}
	// Restart retires every previous instance's cursor. No stale data is exposed
	// and no retained cursor or row snapshot consumes server memory.
	if cursor.Instance != s.catalogInstance {
		return cursor, expired
	}
	mac := hmac.New(sha256.New, s.catalogKey[:])
	mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) || cursor.Account != account || cursor.Parent != parent || cursor.Limit != limit || cursor.After.Validate() != nil || cursor.Snapshot.Validate() != nil {
		return cursor, invalid
	}
	if cursor.Expires <= s.now().Unix() {
		return cursor, expired
	}
	return cursor, nil
}

func (s *Service) Catalog(ctx context.Context, account, parent protocol.ID) (api.CatalogPage, error) {
	return s.CatalogPage(ctx, account, parent, api.PageRequest{Limit: api.MaxPageSize})
}

// CatalogPage uses keyset pagination at one account catalog revision. Any
// concurrent catalog mutation explicitly expires the continuation; callers
// discard their partial listing and start a fresh snapshot.
func (s *Service) CatalogPage(ctx context.Context, account, parent protocol.ID, request api.PageRequest) (api.CatalogPage, error) {
	if request.Validate() != nil || account.Validate() != nil || parent != "" && parent.Validate() != nil {
		return api.CatalogPage{}, api.Error{Code: api.InvalidRequest, Message: "Invalid catalog query"}
	}
	cursor := catalogCursor{Instance: s.catalogInstance, Account: account, Parent: parent, Limit: request.Limit, Snapshot: newID(), Expires: s.now().Add(catalogSnapshotLifetime).Unix()}
	var expected *uint64
	if request.Cursor != "" {
		var err error
		cursor, err = s.openCatalogCursor(request.Cursor, account, parent, request.Limit)
		if err != nil {
			return api.CatalogPage{}, err
		}
		if cursor.Kind != "" {
			return api.CatalogPage{}, api.Error{Code: api.InvalidRequest, Message: "Wrong continuation type"}
		}
		expected = &cursor.Revision
	}
	current, files, err := s.repo.CatalogWindow(ctx, account, parent, cursor.After, expected, request.Limit+1)
	if err != nil {
		return api.CatalogPage{}, err
	}
	page := api.CatalogPage{Page: api.Page{Snapshot: cursor.Snapshot}, CatalogRevision: revision(current), Entries: []api.CatalogEntry{}}
	more := len(files) > request.Limit
	if more {
		files = files[:request.Limit]
	}
	for _, file := range files {
		kind := api.File
		if file.Directory {
			kind = api.Directory
		}
		page.Entries = append(page.Entries, api.CatalogEntry{ID: file.ID, ParentID: file.ParentID, Kind: kind, Mode: protocol.Standard, HeadVersionID: file.HeadVersionID, HeadRevision: revision(file.HeadRevision), Standard: &protocol.StandardMetadata{Name: file.Name}})
	}
	for i := range page.Entries {
		if err := s.repo.PrivateCatalog(ctx, account, &page.Entries[i]); err != nil {
			return api.CatalogPage{}, err
		}
	}
	if more {
		cursor.After = files[len(files)-1].ID
		cursor.Revision = current
		page.NextCursor = s.sealCatalogCursor(cursor)
	}
	return page, nil
}

// CatalogChanges deliberately retains no delta history. An unchanged revision
// returns an empty stable interval; any older revision expires and requires a
// paginated full refresh. This also handles server restarts without event gaps.
func (s *Service) CatalogChanges(ctx context.Context, account protocol.ID, after protocol.Revision) (api.CatalogChanges, error) {
	if after.Validate() != nil {
		return api.CatalogChanges{}, api.Error{Code: api.InvalidRequest, Message: "Invalid catalog revision"}
	}
	current, err := s.repo.CatalogRevision(ctx, account)
	if err != nil {
		return api.CatalogChanges{}, err
	}
	if after != revision(current) {
		return api.CatalogChanges{}, api.Error{Code: api.SnapshotExpired, Message: "Catalog history is unavailable; refresh the catalog"}
	}
	return api.CatalogChanges{Page: api.Page{Snapshot: newID()}, FromRevision: after, ThroughRevision: after, Changes: []api.CatalogChange{}}, nil
}
