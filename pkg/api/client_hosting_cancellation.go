package api

import (
	"context"
	"net/http"
)

func (c *Client) CancelHostingCIRun(ctx context.Context, site, run string) (HostingCIRun, error) {
	var out HostingCIRun
	// Terminal run state supplies idempotency without replaying past feature gates.
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, hostingCIRunPath(site, run)+"/cancel", nil, &out)
	return out, err
}
