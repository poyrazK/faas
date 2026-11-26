package api

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

type ManagedPostgresAutovacuum struct {
	DatabaseID string `json:"database_id"`
	dbguest.AutovacuumStatus
}

type SetManagedPostgresAutovacuumRequest struct {
	Profile dbguest.AutovacuumProfile `json:"profile"`
}

func (c *Client) GetManagedPostgresAutovacuum(ctx context.Context, id string) (ManagedPostgresAutovacuum, error) {
	return c.managedPostgresAutovacuum(ctx, "/v1/postgres", id, http.MethodGet, nil)
}

func (c *Client) GetOrgManagedPostgresAutovacuum(ctx context.Context, organization, id string) (ManagedPostgresAutovacuum, error) {
	return c.managedPostgresAutovacuum(ctx, orgPostgresPath(organization), id, http.MethodGet, nil)
}

func (c *Client) SetManagedPostgresAutovacuum(ctx context.Context, id string, req SetManagedPostgresAutovacuumRequest) (ManagedPostgresAutovacuum, error) {
	return c.managedPostgresAutovacuum(ctx, "/v1/postgres", id, http.MethodPut, req)
}

func (c *Client) SetOrgManagedPostgresAutovacuum(ctx context.Context, organization, id string, req SetManagedPostgresAutovacuumRequest) (ManagedPostgresAutovacuum, error) {
	return c.managedPostgresAutovacuum(ctx, orgPostgresPath(organization), id, http.MethodPut, req)
}

func (c *Client) managedPostgresAutovacuum(ctx context.Context, prefix, id, method string, req any) (ManagedPostgresAutovacuum, error) {
	var out ManagedPostgresAutovacuum
	err := c.do(ctx, method, prefix+"/databases/"+url.PathEscape(id)+"/autovacuum", req, &out)
	return out, err
}
