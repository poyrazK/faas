package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) GetManagedPostgresLiveQueries(ctx context.Context, id string) (ManagedPostgresLiveQueries, error) {
	var out ManagedPostgresLiveQueries
	err := c.do(ctx, http.MethodGet, "/v1/postgres/databases/"+url.PathEscape(id)+"/live", nil, &out)
	return out, err
}

func (c *Client) EnableManagedPostgresLiveQueries(ctx context.Context, id string) (ManagedPostgresLiveQueries, error) {
	var out ManagedPostgresLiveQueries
	err := c.do(ctx, http.MethodPost, "/v1/postgres/databases/"+url.PathEscape(id)+"/live", nil, &out)
	return out, err
}

func (c *Client) RotateManagedPostgresLiveSecret(ctx context.Context, id string, request RotateManagedPostgresLiveSecretRequest) (ManagedPostgresLiveQueries, error) {
	var out ManagedPostgresLiveQueries
	err := c.do(ctx, http.MethodPost, "/v1/postgres/databases/"+url.PathEscape(id)+"/live/rotate", request, &out)
	return out, err
}

func (c *Client) DisableManagedPostgresLiveQueries(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/postgres/databases/"+url.PathEscape(id)+"/live", nil, nil)
}
