package client

import (
	"context"
	"github.com/chironjit/serversend/shared/api"
	"net/http"
	"time"
)

func (c *Client) StorageStatistics(ctx context.Context) (api.StorageStatistics, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var result api.StorageStatistics
	err := c.exchange(ctx, call{method: http.MethodGet, path: "/statistics", token: c.token()}, nil, &result)
	return result, err
}
