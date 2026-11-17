package client

import (
	"context"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

func (c *Client) CreateDirectory(ctx context.Context, parent protocol.ID, name string) (api.CatalogEntry, error) {
	request := api.CreateDirectoryRequest{ID: randomID(), ParentID: parent, Mode: protocol.Standard, ExpectedHead: "0", Standard: &protocol.StandardMetadata{Name: name}}
	if err := request.Validate(nil); err != nil {
		return api.CatalogEntry{}, err
	}
	var entry api.CatalogEntry
	// The same parent/name and ID are replayed; the server merges existing folders.
	err := retryTransfer(ctx, func() error {
		return c.exchange(ctx, call{method: "POST", path: "/directories", token: c.token()}, request, &entry)
	})
	if err == nil {
		err = entry.Validate(nil)
	}
	return entry, err
}

// CreateUser requires a recently authenticated administrator session.
func (c *Client) CreateUser(ctx context.Context, username string) (api.TemporaryPassword, error) {
	var issued api.TemporaryPassword
	err := c.exchange(ctx, call{method: "POST", path: "/admin/users", token: c.token()}, struct {
		Username string `json:"username"`
	}{username}, &issued)
	return issued, err
}

func (c *Client) DeleteEntry(ctx context.Context, id protocol.ID) error {
	if err := id.Validate(); err != nil {
		return err
	}
	return c.exchange(ctx, call{method: "DELETE", path: "/catalog/" + string(id), token: c.token()}, nil, nil)
}
