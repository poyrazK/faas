package api

import (
	"context"
	"net/http"
	"net/url"
)

// GetProductOwnerHelp reads a retained owner-scoped request. It cannot create or
// reply to help and does not substitute for customer-session visibility checks.
func (c *Client) GetProductOwnerHelp(ctx context.Context, slug, id string) (ProductHelpRequest, error) {
	var out ProductHelpRequest
	err := c.do(ctx, http.MethodGet, productPath(slug)+"/help/"+url.PathEscape(id), nil, &out)
	return out, err
}
