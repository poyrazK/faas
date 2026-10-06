package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) GetManagedPostgresRecoveryStatus(ctx context.Context, databaseID string) (ManagedPostgresRecoveryStatus, error) {
	var out ManagedPostgresRecoveryStatus
	err := c.do(ctx, http.MethodGet, "/v1/postgres/databases/"+url.PathEscape(databaseID)+"/recovery", nil, &out)
	return out, err
}
