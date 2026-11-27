package api

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// ManagedPostgresDiagnostics reports an on-demand sample of a native primary.
type ManagedPostgresDiagnostics struct {
	DatabaseID string `json:"database_id"`
	dbguest.Diagnostics
}

func (c *Client) GetManagedPostgresDiagnostics(ctx context.Context, id string) (ManagedPostgresDiagnostics, error) {
	return c.getManagedPostgresDiagnostics(ctx, "/v1/postgres", id)
}

func (c *Client) GetOrgManagedPostgresDiagnostics(ctx context.Context, organization, id string) (ManagedPostgresDiagnostics, error) {
	return c.getManagedPostgresDiagnostics(ctx, orgPostgresPath(organization), id)
}

func (c *Client) getManagedPostgresDiagnostics(ctx context.Context, prefix, id string) (ManagedPostgresDiagnostics, error) {
	var out ManagedPostgresDiagnostics
	err := c.do(ctx, http.MethodGet, prefix+"/databases/"+url.PathEscape(id)+"/diagnostics", nil, &out)
	return out, err
}
