package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

const hostingAPIPath = "/v1/internal/hosting/sites"

func (c *Client) GetHostingSiteStatus(ctx context.Context, site string) (HostingSiteStatus, error) {
	var out HostingSiteStatus
	err := c.do(ctx, http.MethodGet, hostingSitePath(site)+"/status", nil, &out)
	return out, err
}

func (c *Client) SetHostingSiteSuspended(ctx context.Context, site string, req SetHostingSuspendedRequest) (HostingSite, error) {
	var out HostingSite
	err := c.do(ctx, http.MethodPut, hostingSitePath(site)+"/suspension", req, &out)
	return out, err
}

func (c *Client) DeleteHostingSite(ctx context.Context, site string) (HostingSite, error) {
	var out HostingSite
	err := c.do(ctx, http.MethodDelete, hostingSitePath(site), nil, &out)
	return out, err
}

func (c *Client) CreateHostingSite(ctx context.Context, req CreateHostingSiteRequest) (HostingSite, error) {
	var out HostingSite
	err := c.do(ctx, http.MethodPost, hostingAPIPath, req, &out)
	return out, err
}

func (c *Client) ListHostingSites(ctx context.Context) ([]HostingSite, error) {
	var out []HostingSite
	err := c.do(ctx, http.MethodGet, hostingAPIPath, nil, &out)
	return out, err
}

func hostingSitePath(site string) string { return hostingAPIPath + "/" + url.PathEscape(site) }
func hostingReleasePath(site, release string) string {
	return hostingSitePath(site) + "/releases/" + url.PathEscape(release)
}

func (c *Client) GetHostingSite(ctx context.Context, site string) (HostingSite, error) {
	var out HostingSite
	err := c.do(ctx, http.MethodGet, hostingSitePath(site), nil, &out)
	return out, err
}

func (c *Client) CreateHostingRelease(ctx context.Context, site string, manifest HostingManifest) (HostingRelease, error) {
	var out HostingRelease
	err := c.do(ctx, http.MethodPost, hostingSitePath(site)+"/releases", manifest, &out)
	return out, err
}

func (c *Client) ListHostingReleases(ctx context.Context, site string) ([]HostingRelease, error) {
	var out []HostingRelease
	err := c.do(ctx, http.MethodGet, hostingSitePath(site)+"/releases", nil, &out)
	return out, err
}

func (c *Client) HostingReleaseUploads(ctx context.Context, site, release string) (HostingUploadResponse, error) {
	var out HostingUploadResponse
	err := c.do(ctx, http.MethodPost, hostingReleasePath(site, release)+"/uploads", nil, &out)
	return out, err
}

func (c *Client) FinalizeHostingRelease(ctx context.Context, site, release string) (HostingRelease, error) {
	var out HostingRelease
	cli := *c.http
	cli.Timeout = HostingFinalizeTimeout + HostingStorageTimeout
	err := c.doWithClientAndIdempotencyKey(ctx, &cli, http.MethodPost, hostingReleasePath(site, release)+"/finalize", nil, &out, "")
	return out, err
}

func (c *Client) PromoteHostingRelease(ctx context.Context, site string, req PromoteHostingReleaseRequest) (HostingSite, error) {
	var out HostingSite
	err := c.do(ctx, http.MethodPost, hostingSitePath(site)+"/promotions", req, &out)
	return out, err
}

func (c *Client) RetireHostingRelease(ctx context.Context, site, release string) (HostingRelease, error) {
	var out HostingRelease
	err := c.do(ctx, http.MethodDelete, hostingReleasePath(site, release), nil, &out)
	return out, err
}

func (c *Client) GetHostingCapabilities(ctx context.Context) (HostingCapabilities, error) {
	var out HostingCapabilities
	err := c.do(ctx, http.MethodGet, "/v1/internal/hosting/capabilities", nil, &out)
	return out, err
}

func (c *Client) GetHostingUsage(ctx context.Context) (HostingUsage, error) {
	var out HostingUsage
	err := c.do(ctx, http.MethodGet, "/v1/internal/hosting/usage", nil, &out)
	return out, err
}

func (c *Client) ListHostingPreviews(ctx context.Context, site string) ([]HostingPreview, error) {
	var out []HostingPreview
	err := c.do(ctx, http.MethodGet, hostingSitePath(site)+"/previews", nil, &out)
	return out, err
}
func (c *Client) CreateHostingPreview(ctx context.Context, site string, req CreateHostingPreviewRequest) (HostingPreview, error) {
	var out HostingPreview
	err := c.do(ctx, http.MethodPost, hostingSitePath(site)+"/previews", req, &out)
	return out, err
}
func (c *Client) RevokeHostingPreview(ctx context.Context, site, id string, generation int64) (HostingPreview, error) {
	var out HostingPreview
	err := c.do(ctx, http.MethodDelete, hostingSitePath(site)+"/previews/"+url.PathEscape(id), RevokeHostingPreviewRequest{ExpectedGeneration: generation}, &out)
	return out, err
}

func (c *Client) CreateHostingReleaseWithProvenance(ctx context.Context, site string, manifest HostingManifest, p *HostingReleaseProvenance) (HostingRelease, error) {
	var out HostingRelease
	err := c.do(ctx, http.MethodPost, hostingSitePath(site)+"/releases", CreateHostingReleaseRequest{HostingManifest: manifest, Provenance: p}, &out)
	return out, err
}
func (c *Client) ListHostingHistory(ctx context.Context, site string, before int64) (HostingHistoryPage, error) {
	var out HostingHistoryPage
	path := hostingSitePath(site) + "/history"
	if before != 0 {
		path += "?before_revision=" + strconv.FormatInt(before, 10)
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
