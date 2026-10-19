package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) GetProductCustomerOverview(ctx context.Context, slug, cursor string) (ProductCustomerOverviewPage, error) {
	var out ProductCustomerOverviewPage
	err := c.do(ctx, http.MethodGet, productPath(slug)+"/customers/overview?cursor="+url.QueryEscape(cursor), nil, &out)
	return out, err
}
