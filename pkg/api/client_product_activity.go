package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) GetProductCustomerActivity(ctx context.Context, slug, tenant, cursor string) (ProductCustomerActivityPage, error) {
	var out ProductCustomerActivityPage
	q := url.Values{"tenant": {tenant}, "cursor": {cursor}}
	err := c.do(ctx, http.MethodGet, productPath(slug)+"/customers/activity?"+q.Encode(), nil, &out)
	return out, err
}
