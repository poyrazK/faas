package api

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

type ManagedPostgresSessions struct {
	DatabaseID string `json:"database_id"`
	dbguest.Sessions
}

type CancelManagedPostgresQueryRequest struct {
	TargetID string `json:"target_id"`
}

type ManagedPostgresQueryCancellation struct {
	DatabaseID string `json:"database_id"`
	TargetID   string `json:"target_id"`
	Outcome    string `json:"outcome"`
}

func (c *Client) ListManagedPostgresSessions(ctx context.Context, id string) (ManagedPostgresSessions, error) {
	return c.listManagedPostgresSessions(ctx, "/v1/postgres", id)
}

func (c *Client) ListOrgManagedPostgresSessions(ctx context.Context, org, id string) (ManagedPostgresSessions, error) {
	return c.listManagedPostgresSessions(ctx, orgPostgresPath(org), id)
}

func (c *Client) listManagedPostgresSessions(ctx context.Context, prefix, id string) (ManagedPostgresSessions, error) {
	var report ManagedPostgresSessions
	err := c.do(ctx, http.MethodGet, prefix+"/databases/"+url.PathEscape(id)+"/sessions", nil, &report)
	return report, err
}

func (c *Client) CancelManagedPostgresQuery(ctx context.Context, id string, req CancelManagedPostgresQueryRequest) (ManagedPostgresQueryCancellation, error) {
	return c.cancelManagedPostgresQuery(ctx, "/v1/postgres", id, req)
}

func (c *Client) CancelOrgManagedPostgresQuery(ctx context.Context, org, id string, req CancelManagedPostgresQueryRequest) (ManagedPostgresQueryCancellation, error) {
	return c.cancelManagedPostgresQuery(ctx, orgPostgresPath(org), id, req)
}

func (c *Client) cancelManagedPostgresQuery(ctx context.Context, prefix, id string, req CancelManagedPostgresQueryRequest) (ManagedPostgresQueryCancellation, error) {
	var report ManagedPostgresQueryCancellation
	err := c.do(ctx, http.MethodPost, prefix+"/databases/"+url.PathEscape(id)+"/cancel-query", req, &report)
	return report, err
}
