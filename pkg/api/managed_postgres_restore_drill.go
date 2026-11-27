package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type RequestManagedPostgresRestoreDrill struct {
	RequestID string `json:"request_id"`
}

type ManagedPostgresRestoreDrill struct {
	DatabaseID string `json:"database_id"`
	RequestID  string `json:"request_id"`
	ManagedPostgresRestoreDrillStatus
}

func (d *ManagedPostgresRestoreDrillStatus) SetDuration() {
	if d.FinishedAt != nil {
		seconds := int64(max(d.FinishedAt.Sub(d.StartedAt), 0) / time.Second)
		d.DurationSeconds = &seconds
	}
}

func (c *Client) StartManagedPostgresRestoreDrill(ctx context.Context, id string, req RequestManagedPostgresRestoreDrill) (ManagedPostgresRestoreDrill, error) {
	return c.startManagedPostgresRestoreDrill(ctx, "/v1/postgres", id, req)
}

func (c *Client) StartOrgManagedPostgresRestoreDrill(ctx context.Context, organization, id string, req RequestManagedPostgresRestoreDrill) (ManagedPostgresRestoreDrill, error) {
	return c.startManagedPostgresRestoreDrill(ctx, orgPostgresPath(organization), id, req)
}

func (c *Client) startManagedPostgresRestoreDrill(ctx context.Context, prefix, id string, req RequestManagedPostgresRestoreDrill) (ManagedPostgresRestoreDrill, error) {
	var out ManagedPostgresRestoreDrill
	err := c.do(ctx, http.MethodPost, prefix+"/databases/"+url.PathEscape(id)+"/restore-drills", req, &out)
	return out, err
}

func (c *Client) GetManagedPostgresRestoreDrill(ctx context.Context, id, drillID string) (ManagedPostgresRestoreDrill, error) {
	return c.getManagedPostgresRestoreDrill(ctx, "/v1/postgres", id, drillID)
}

func (c *Client) GetOrgManagedPostgresRestoreDrill(ctx context.Context, organization, id, drillID string) (ManagedPostgresRestoreDrill, error) {
	return c.getManagedPostgresRestoreDrill(ctx, orgPostgresPath(organization), id, drillID)
}

func (c *Client) getManagedPostgresRestoreDrill(ctx context.Context, prefix, id, drillID string) (ManagedPostgresRestoreDrill, error) {
	var out ManagedPostgresRestoreDrill
	err := c.do(ctx, http.MethodGet, prefix+"/databases/"+url.PathEscape(id)+"/restore-drills/"+url.PathEscape(drillID), nil, &out)
	return out, err
}
