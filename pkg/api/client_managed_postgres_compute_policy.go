package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) ChangeManagedPostgresComputePolicy(ctx context.Context, id string, request ChangeManagedPostgresComputePolicyRequest) (ManagedPostgresComputePolicyChange, error) {
	var out ManagedPostgresComputePolicyChange
	err := c.do(ctx, http.MethodPost, "/v1/postgres/databases/"+url.PathEscape(id)+"/compute-policy", request, &out)
	return out, err
}

// SetManagedPostgresSuspendTimeout sets a scale-to-zero database's own
// suspend timeout, or with a nil Seconds returns it to the default.
func (c *Client) SetManagedPostgresSuspendTimeout(ctx context.Context, id string, request SetManagedPostgresSuspendTimeoutRequest) (ManagedPostgresSuspendTimeout, error) {
	var out ManagedPostgresSuspendTimeout
	err := c.do(ctx, http.MethodPut, "/v1/postgres/databases/"+url.PathEscape(id)+"/suspend-timeout", request, &out)
	return out, err
}

func (c *Client) GetManagedPostgresComputePolicyChange(ctx context.Context, databaseID, changeID string) (ManagedPostgresComputePolicyChange, error) {
	var out ManagedPostgresComputePolicyChange
	err := c.do(ctx, http.MethodGet, "/v1/postgres/databases/"+url.PathEscape(databaseID)+"/compute-policy-changes/"+url.PathEscape(changeID), nil, &out)
	return out, err
}

// SetManagedPostgresWakeWithApp turns starting a scale-to-zero database when
// a bound app wakes on or off (ADR-20261125105038).
func (c *Client) SetManagedPostgresWakeWithApp(ctx context.Context, id string, request SetManagedPostgresWakeWithAppRequest) (ManagedPostgresWakeWithApp, error) {
	var out ManagedPostgresWakeWithApp
	err := c.do(ctx, http.MethodPut, "/v1/postgres/databases/"+url.PathEscape(id)+"/wake-with-app", request, &out)
	return out, err
}

// GetManagedPostgresActivity reads why a database was awake since a time
// (ADR-20261125231222); since is a duration such as 24h or 7d, an RFC 3339
// time, or empty for the last day.
func (c *Client) GetManagedPostgresActivity(ctx context.Context, id, since string) (ManagedPostgresActivity, error) {
	var out ManagedPostgresActivity
	path := "/v1/postgres/databases/" + url.PathEscape(id) + "/activity"
	if since != "" {
		path += "?since=" + url.QueryEscape(since)
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
