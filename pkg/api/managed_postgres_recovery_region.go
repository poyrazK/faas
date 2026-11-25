package api

import (
	"context"
	"net/http"
	"net/url"
)

// SetManagedPostgresRecoveryRegionRequest selects a configured recovery
// region. An explicit empty string disables regional recovery.
type SetManagedPostgresRecoveryRegionRequest struct {
	Region *string `json:"region"`
}

func (c *Client) SetManagedPostgresRecoveryRegion(ctx context.Context, database string, req SetManagedPostgresRecoveryRegionRequest) error {
	return c.do(ctx, http.MethodPut, "/v1/postgres/databases/"+url.PathEscape(database)+"/recovery-region", req, nil)
}

func (c *Client) SetOrgManagedPostgresRecoveryRegion(ctx context.Context, org, database string, req SetManagedPostgresRecoveryRegionRequest) error {
	return c.do(ctx, http.MethodPut, "/v1/orgs/"+url.PathEscape(org)+"/postgres/databases/"+url.PathEscape(database)+"/recovery-region", req, nil)
}
