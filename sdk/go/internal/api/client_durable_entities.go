package api

import (
	"context"
	"errors"
	"net/url"
)

// InvokeDurableEntity retries safely only when RequestID and the exact payload
// are retained. A replay returns the original value and version after rollout.
func (c *Client) InvokeDurableEntity(ctx context.Context, slug string, request DurableEntityInvokeRequest) (DurableEntityInvokeResponse, error) {
	var out DurableEntityInvokeResponse
	if request.RequestID == "" {
		return out, errors.New("durable entity request_id is required for safe retries")
	}
	return out, c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/entities/invoke", request, &out)
}
