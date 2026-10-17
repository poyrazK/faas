package api

import (
	"context"
	"net/http"
	"net/url"
)

// NewHostingAutomationClient rejects redirects and disables account completion
// discovery. The bearer is accepted only by the server's exact site allowlist.
func NewHostingAutomationClient(base, token string) *Client {
	c := NewClient(base, token)
	c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.cache = nil
	return c
}
func (c *Client) CreateHostingAutomationCredential(ctx context.Context, site string, req CreateHostingAutomationCredentialRequest) (HostingAutomationCredentialResponse, error) {
	var out HostingAutomationCredentialResponse
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, hostingSitePath(site)+"/automation-credentials", req, &out)
	return out, err
}
func (c *Client) ListHostingAutomationCredentials(ctx context.Context, site string) ([]HostingAutomationCredential, error) {
	var out []HostingAutomationCredential
	err := c.do(ctx, http.MethodGet, hostingSitePath(site)+"/automation-credentials", nil, &out)
	return out, err
}
func (c *Client) RevokeHostingAutomationCredential(ctx context.Context, site, id string) error {
	return c.doWithoutIdempotencyKey(ctx, http.MethodDelete, hostingSitePath(site)+"/automation-credentials/"+url.PathEscape(id), nil, nil)
}
func hostingCIRunPath(site, id string) string {
	return hostingSitePath(site) + "/ci/" + url.PathEscape(id)
}
func (c *Client) CreateHostingCIRun(ctx context.Context, site, id string, req HostingCIRunRequest) (HostingCIRun, error) {
	var out HostingCIRun
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPut, hostingCIRunPath(site, id), req, &out)
	return out, err
}
func (c *Client) GetHostingCIRun(ctx context.Context, site, id string) (HostingCIRun, error) {
	var out HostingCIRun
	err := c.do(ctx, http.MethodGet, hostingCIRunPath(site, id), nil, &out)
	return out, err
}
func (c *Client) CreateHostingCIPreview(ctx context.Context, site, id string) (HostingPreview, error) {
	var out HostingPreview
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, hostingCIRunPath(site, id)+"/preview", nil, &out)
	return out, err
}
