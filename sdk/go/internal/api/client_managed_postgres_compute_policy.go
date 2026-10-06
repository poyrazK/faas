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

func (c *Client) GetManagedPostgresComputePolicyChange(ctx context.Context, databaseID, changeID string) (ManagedPostgresComputePolicyChange, error) {
	var out ManagedPostgresComputePolicyChange
	err := c.do(ctx, http.MethodGet, "/v1/postgres/databases/"+url.PathEscape(databaseID)+"/compute-policy-changes/"+url.PathEscape(changeID), nil, &out)
	return out, err
}
