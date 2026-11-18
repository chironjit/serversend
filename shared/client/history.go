package client

import (
	"context"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"net/url"
)

func (c *Client) findUploadTarget(ctx context.Context, parent protocol.ID, name string) (*api.CatalogEntry, error) {
	var result struct {
		Entry *api.CatalogEntry `json:"entry"`
	}
	err := retryTransfer(ctx, func() error {
		return c.exchange(ctx, call{method: "GET", path: "/catalog/target?parent_id=" + url.QueryEscape(string(parent)) + "&name=" + url.QueryEscape(name), token: c.token()}, nil, &result)
	})
	if err == nil && result.Entry != nil {
		err = result.Entry.Validate(nil)
		if err == nil && (result.Entry.ParentID != parent || result.Entry.Standard == nil || result.Entry.Standard.Name != name) {
			return nil, api.Error{Code: api.InvalidRequest, Message: "Server returned a different upload destination"}
		}
	}
	return result.Entry, err
}
func (c *Client) History(ctx context.Context, file protocol.ID, cursor api.Cursor) (p api.VersionPage, err error) {
	if file.Validate() != nil {
		return p, api.Error{Code: api.InvalidRequest, Message: "Invalid file"}
	}
	err = c.exchange(ctx, call{method: "GET", path: "/files/" + string(file) + "/versions?limit=100&cursor=" + url.QueryEscape(string(cursor)), token: c.token()}, nil, &p)
	if err == nil {
		err = p.Validate()
	}
	return
}
func (c *Client) TrashPage(ctx context.Context, cursor api.Cursor) (p api.CatalogPage, err error) {
	err = c.exchange(ctx, call{method: "GET", path: "/trash?limit=100&cursor=" + url.QueryEscape(string(cursor)), token: c.token()}, nil, &p)
	if err == nil {
		err = p.Validate(nil)
	}
	return
}
func (c *Client) RestoreTrash(ctx context.Context, id protocol.ID) error {
	if id.Validate() != nil {
		return api.Error{Code: api.InvalidRequest, Message: "Invalid item"}
	}
	return c.exchange(ctx, call{method: "POST", path: "/trash/" + string(id) + "/restore", token: c.token()}, nil, nil)
}
func (c *Client) PurgeTrash(ctx context.Context, id protocol.ID) error {
	if id.Validate() != nil {
		return api.Error{Code: api.InvalidRequest, Message: "Invalid item"}
	}
	return c.exchange(ctx, call{method: "DELETE", path: "/trash/" + string(id) + "?confirm=purge", token: c.token()}, nil, nil)
}
func (c *Client) SelectVersion(ctx context.Context, file, version protocol.ID, expected protocol.Revision) error {
	if file.Validate() != nil || version.Validate() != nil || expected.Validate() != nil {
		return api.Error{Code: api.InvalidRequest, Message: "Invalid version selection"}
	}
	return c.exchange(ctx, call{method: "POST", path: "/files/" + string(file) + "/versions/" + string(version) + "/restore", token: c.token()}, struct {
		Expected protocol.Revision `json:"expected_head"`
	}{expected}, nil)
}
