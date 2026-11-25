package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// ManagedPostgresBindingTimeouts contains the saved defaults and their rollout state.
type ManagedPostgresBindingTimeouts struct {
	BindingID           string     `json:"binding_id"`
	StatementMS         int32      `json:"statement_timeout_ms"`
	LockMS              int32      `json:"lock_timeout_ms"`
	IdleInTransactionMS int32      `json:"idle_in_transaction_session_timeout_ms"`
	Revision            int64      `json:"revision"`
	State               string     `json:"state"`
	AppliedAt           *time.Time `json:"applied_at,omitempty"`
}

// SetManagedPostgresBindingTimeoutsRequest leaves omitted settings unchanged.
type SetManagedPostgresBindingTimeoutsRequest struct {
	StatementMS         *int32 `json:"statement_timeout_ms,omitempty"`
	LockMS              *int32 `json:"lock_timeout_ms,omitempty"`
	IdleInTransactionMS *int32 `json:"idle_in_transaction_session_timeout_ms,omitempty"`
}

func (c *Client) GetManagedPostgresBindingTimeouts(ctx context.Context, id string) (ManagedPostgresBindingTimeouts, error) {
	return c.managedPostgresBindingTimeouts(ctx, "/v1/postgres", id, http.MethodGet, nil)
}

func (c *Client) GetOrgManagedPostgresBindingTimeouts(ctx context.Context, organization, id string) (ManagedPostgresBindingTimeouts, error) {
	return c.managedPostgresBindingTimeouts(ctx, orgPostgresPath(organization), id, http.MethodGet, nil)
}

func (c *Client) SetManagedPostgresBindingTimeouts(ctx context.Context, id string, req SetManagedPostgresBindingTimeoutsRequest) (ManagedPostgresBindingTimeouts, error) {
	return c.managedPostgresBindingTimeouts(ctx, "/v1/postgres", id, http.MethodPut, req)
}

func (c *Client) SetOrgManagedPostgresBindingTimeouts(ctx context.Context, organization, id string, req SetManagedPostgresBindingTimeoutsRequest) (ManagedPostgresBindingTimeouts, error) {
	return c.managedPostgresBindingTimeouts(ctx, orgPostgresPath(organization), id, http.MethodPut, req)
}

func (c *Client) managedPostgresBindingTimeouts(ctx context.Context, prefix, id, method string, req any) (ManagedPostgresBindingTimeouts, error) {
	var out ManagedPostgresBindingTimeouts
	err := c.do(ctx, method, prefix+"/bindings/"+url.PathEscape(id)+"/timeouts", req, &out)
	return out, err
}
