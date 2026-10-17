package api

import (
	"context"
	"net/http"
)

func (c *Client) GetHostingPreviewCapacity(ctx context.Context, site string) (HostingPreviewCapacity, error) {
	var out HostingPreviewCapacity
	err := c.do(ctx, http.MethodGet, hostingSitePath(site)+"/preview-capacity", nil, &out)
	return out, err
}
