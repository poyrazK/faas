package api

import (
	"context"
	"net/url"
)

func postgresChangesPath(slug string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/postgres-changes"
}

// PutPostgresChanges declares the app's Postgres change capture.
func (c *Client) PutPostgresChanges(ctx context.Context, slug string, req PutPostgresChangesRequest) (PostgresChangesResponse, error) {
	var out PostgresChangesResponse
	err := c.do(ctx, "PUT", postgresChangesPath(slug), req, &out)
	return out, err
}

// GetPostgresChanges reads the app's Postgres change capture.
func (c *Client) GetPostgresChanges(ctx context.Context, slug string) (PostgresChangesResponse, error) {
	var out PostgresChangesResponse
	err := c.do(ctx, "GET", postgresChangesPath(slug), nil, &out)
	return out, err
}

// DeletePostgresChanges stops the app's Postgres change capture.
func (c *Client) DeletePostgresChanges(ctx context.Context, slug string) error {
	return c.do(ctx, "DELETE", postgresChangesPath(slug), nil, nil)
}

// PutPostgresChangesConnection stores the relay login a BYO install created.
func (c *Client) PutPostgresChangesConnection(ctx context.Context, slug, connection string) error {
	return c.do(ctx, "PUT", postgresChangesPath(slug)+"/connection", map[string]string{"connection_url": connection}, nil)
}
