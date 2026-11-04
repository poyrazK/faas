package api

import (
	"context"
	"net/http"
	"net/url"
)

// EgressOverageRequest turns a billing subject's egress overage on or off
// (ADR-20261103131742 amendment 2).
type EgressOverageRequest struct {
	Enabled *bool `json:"enabled"`
}

// EgressOverageResponse is a billing subject's egress overage choice. With it
// on, egress past the plan's monthly allowance is billed at MillicentsPerGiB
// and counts toward the spend cap; with it off, the subject's apps stop at
// the allowance until the month ends. Billed is false while Gregale does not
// bill egress for the plan: apps then stop at the allowance whatever the
// choice.
type EgressOverageResponse struct {
	Enabled          bool  `json:"enabled"`
	Billed           bool  `json:"billed"`
	IncludedGiB      int64 `json:"included_gib"`
	MillicentsPerGiB int64 `json:"millicents_per_gib"`
}

// GetEgressOverage reads the account's egress overage choice.
func (c *Client) GetEgressOverage(ctx context.Context) (EgressOverageResponse, error) {
	var out EgressOverageResponse
	err := c.do(ctx, http.MethodGet, "/v1/account/egress-overage", nil, &out)
	return out, err
}

// SetEgressOverage turns the account's egress overage on or off.
func (c *Client) SetEgressOverage(ctx context.Context, enabled bool) (EgressOverageResponse, error) {
	var out EgressOverageResponse
	err := c.do(ctx, http.MethodPut, "/v1/account/egress-overage", EgressOverageRequest{Enabled: &enabled}, &out)
	return out, err
}

// GetOrgEgressOverage reads the organization's egress overage choice.
func (c *Client) GetOrgEgressOverage(ctx context.Context, slug string) (EgressOverageResponse, error) {
	var out EgressOverageResponse
	err := c.do(ctx, http.MethodGet, "/v1/orgs/"+url.PathEscape(slug)+"/egress-overage", nil, &out)
	return out, err
}

// SetOrgEgressOverage turns the organization's egress overage on or off.
func (c *Client) SetOrgEgressOverage(ctx context.Context, slug string, enabled bool) (EgressOverageResponse, error) {
	var out EgressOverageResponse
	err := c.do(ctx, http.MethodPut, "/v1/orgs/"+url.PathEscape(slug)+"/egress-overage", EgressOverageRequest{Enabled: &enabled}, &out)
	return out, err
}
