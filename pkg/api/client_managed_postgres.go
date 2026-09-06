package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) ListManagedPostgresDatabases(ctx context.Context) (ManagedPostgresDatabaseList, error) {
	var out ManagedPostgresDatabaseList
	err := c.do(ctx, http.MethodGet, "/v1/postgres/databases", nil, &out)
	return out, err
}

func (c *Client) CreateManagedPostgresDatabase(ctx context.Context, req CreateManagedPostgresDatabaseRequest) (ManagedPostgresDatabase, error) {
	var out ManagedPostgresDatabase
	err := c.do(ctx, http.MethodPost, "/v1/postgres/databases", req, &out)
	return out, err
}

func (c *Client) GetManagedPostgresDatabase(ctx context.Context, id string) (ManagedPostgresDatabase, error) {
	var out ManagedPostgresDatabase
	err := c.do(ctx, http.MethodGet, "/v1/postgres/databases/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) DeleteManagedPostgresDatabase(ctx context.Context, id string) (ManagedPostgresDatabase, error) {
	var out ManagedPostgresDatabase
	err := c.do(ctx, http.MethodDelete, "/v1/postgres/databases/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) RestoreManagedPostgresDatabase(ctx context.Context, id string, req RestoreManagedPostgresDatabaseRequest) (ManagedPostgresDatabase, error) {
	var out ManagedPostgresDatabase
	err := c.do(ctx, http.MethodPost, "/v1/postgres/databases/"+url.PathEscape(id)+"/restore", req, &out)
	return out, err
}

func (c *Client) ListManagedPostgresBindings(ctx context.Context, databaseID string) (ManagedPostgresBindingList, error) {
	var out ManagedPostgresBindingList
	err := c.do(ctx, http.MethodGet, "/v1/postgres/databases/"+url.PathEscape(databaseID)+"/bindings", nil, &out)
	return out, err
}

func (c *Client) CreateManagedPostgresBinding(ctx context.Context, databaseID string, req CreateManagedPostgresBindingRequest) (ManagedPostgresBinding, error) {
	var out ManagedPostgresBinding
	err := c.do(ctx, http.MethodPost, "/v1/postgres/databases/"+url.PathEscape(databaseID)+"/bindings", req, &out)
	return out, err
}

func (c *Client) GetManagedPostgresBinding(ctx context.Context, id string) (ManagedPostgresBinding, error) {
	var out ManagedPostgresBinding
	err := c.do(ctx, http.MethodGet, "/v1/postgres/bindings/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) DeleteManagedPostgresBinding(ctx context.Context, id string) (ManagedPostgresBinding, error) {
	var out ManagedPostgresBinding
	err := c.do(ctx, http.MethodDelete, "/v1/postgres/bindings/"+url.PathEscape(id), nil, &out)
	return out, err
}
