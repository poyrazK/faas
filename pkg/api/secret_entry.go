package api

import (
	"context"
	"net/url"
)

// RequestSecretEntry starts a one-time secret entry for an app
// (ADR-20261026174727). The response's ticket and exchange code are private
// to the caller; only EntryURL may be shown to an agent or a person.
func (c *Client) RequestSecretEntry(ctx context.Context, slug string, req SecretEntryRequest) (SecretEntryResponse, error) {
	var out SecretEntryResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/secret-requests", req, &out)
}

// GetSecretEntryStatus reports whether the owner has set the requested secret.
func (c *Client) GetSecretEntryStatus(ctx context.Context, slug string, req SecretEntryStatusRequest) (SecretEntryStatus, error) {
	var out SecretEntryStatus
	return out, c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/secret-requests/status", req, &out)
}
