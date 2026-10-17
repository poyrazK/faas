package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) ListProductEmailDeliveries(ctx context.Context, slug, tenant, status, cursor string) (ProductEmailOwnerPage, error) {
	var page ProductEmailOwnerPage
	q := url.Values{"tenant": {tenant}, "status": {status}, "cursor": {cursor}}
	err := c.do(ctx, http.MethodGet, productPath(slug)+"/email-deliveries?"+q.Encode(), nil, &page)
	return page, err
}
func (c *Client) RetryProductEmailDelivery(ctx context.Context, slug, id string, request ProductEmailRetryRequest) (ProductEmailRetryReceipt, error) {
	var receipt ProductEmailRetryReceipt
	err := c.do(ctx, http.MethodPost, productPath(slug)+"/email-deliveries/"+url.PathEscape(id)+"/retry", request, &receipt)
	return receipt, err
}

func (c *Client) ListProductEmailRecoveries(ctx context.Context, slug, tenant, cursor string) (ProductEmailRecoveryPage, error) {
	var page ProductEmailRecoveryPage
	q := url.Values{"tenant": {tenant}, "cursor": {cursor}}
	err := c.do(ctx, http.MethodGet, productPath(slug)+"/email-recoveries?"+q.Encode(), nil, &page)
	return page, err
}

func (c *Client) ListProductEmailOptOuts(ctx context.Context, slug, tenant, cursor string) (ProductEmailOptOutPage, error) {
	var page ProductEmailOptOutPage
	q := url.Values{"tenant": {tenant}, "cursor": {cursor}}
	err := c.do(ctx, http.MethodGet, productPath(slug)+"/email-opt-outs?"+q.Encode(), nil, &page)
	return page, err
}
