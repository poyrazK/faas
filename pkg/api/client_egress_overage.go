package api

import (
	"context"
	"net/http"
	"net/url"
)

// GetEgressOverage returns the account's egress overage election
// (ADR-20261104204500).
func (c *Client) GetEgressOverage(ctx context.Context) (EgressOverageResponse, error) {
	var out EgressOverageResponse
	return out, c.do(ctx, http.MethodGet, "/v1/account/egress-overage", nil, &out)
}

// SetEgressOverage turns the account's egress overage election on or off.
// Turning it on needs a paid plan (CodePlanEgressOverageNotAllowed).
func (c *Client) SetEgressOverage(ctx context.Context, enabled bool) (EgressOverageResponse, error) {
	var out EgressOverageResponse
	return out, c.do(ctx, http.MethodPut, "/v1/account/egress-overage", EgressOverageRequest{Enabled: &enabled}, &out)
}

// GetOrgEgressOverage returns the organization's egress overage election.
func (c *Client) GetOrgEgressOverage(ctx context.Context, slug string) (EgressOverageResponse, error) {
	var out EgressOverageResponse
	return out, c.do(ctx, http.MethodGet, "/v1/orgs/"+url.PathEscape(slug)+"/egress-overage", nil, &out)
}

// SetOrgEgressOverage turns the organization's egress overage election on or
// off.
func (c *Client) SetOrgEgressOverage(ctx context.Context, slug string, enabled bool) (EgressOverageResponse, error) {
	var out EgressOverageResponse
	return out, c.do(ctx, http.MethodPut, "/v1/orgs/"+url.PathEscape(slug)+"/egress-overage", EgressOverageRequest{Enabled: &enabled}, &out)
}
