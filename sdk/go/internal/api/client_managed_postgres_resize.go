package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) GetManagedPostgresCapabilities(ctx context.Context, region string) (ManagedPostgresCapabilities, error) {
	var out ManagedPostgresCapabilities
	path := "/v1/postgres/capabilities"
	if region != "" {
		path += "?region=" + url.QueryEscape(region)
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return ManagedPostgresCapabilities{}, err
	}
	return out, nil
}

func (c *Client) ResizeManagedPostgresDatabase(ctx context.Context, id string, request ResizeManagedPostgresDatabaseRequest) (ManagedPostgresResize, error) {
	var out ManagedPostgresResize
	err := c.do(ctx, http.MethodPost, "/v1/postgres/databases/"+url.PathEscape(id)+"/resize", request, &out)
	return out, err
}

func (c *Client) GetManagedPostgresResize(ctx context.Context, databaseID, resizeID string) (ManagedPostgresResize, error) {
	var out ManagedPostgresResize
	err := c.do(ctx, http.MethodGet, "/v1/postgres/databases/"+url.PathEscape(databaseID)+"/resizes/"+url.PathEscape(resizeID), nil, &out)
	return out, err
}
