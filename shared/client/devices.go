package client

import (
	"context"
	"errors"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

func (c *Client) Devices(ctx context.Context, before string) ([]api.DeviceInfo, error) {
	if before != "" && protocol.ID(before).Validate() != nil {
		return nil, errors.New("invalid device cursor")
	}
	var devices []api.DeviceInfo
	err := c.exchange(ctx, call{method: "GET", path: "/sessions/devices?before=" + before, token: c.token()}, nil, &devices)
	return devices, err
}
func (c *Client) RevokeDevice(ctx context.Context, id string) error {
	if id != "others" && protocol.ID(id).Validate() != nil {
		return errors.New("invalid device")
	}
	return c.exchange(ctx, call{method: "POST", path: "/sessions/devices/" + id + "/revoke", token: c.token()}, struct{}{}, nil)
}
