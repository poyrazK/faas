package api

import (
	"context"
	"net/http"
	"net/url"
)

// previewsPath is /v1/previews, or /v1/orgs/{slug}/previews when org is set.
func previewsPath(org, key string) string {
	path := "/v1/previews"
	if org != "" {
		path = "/v1/orgs/" + url.PathEscape(org) + "/previews"
	}
	if key != "" {
		path += "/" + url.PathEscape(key)
	}
	return path
}

// ListPreviews lists the platform previews the caller can join.
func (c *Client) ListPreviews(ctx context.Context) (PreviewList, error) {
	var out PreviewList
	err := c.do(ctx, http.MethodGet, previewsPath("", ""), nil, &out)
	return out, err
}

// JoinPreview opts the caller's account in to a platform preview.
func (c *Client) JoinPreview(ctx context.Context, key string) (Preview, error) {
	var out Preview
	err := c.do(ctx, http.MethodPut, previewsPath("", key), nil, &out)
	return out, err
}

// LeavePreview removes the caller's opt-in.
func (c *Client) LeavePreview(ctx context.Context, key string) (Preview, error) {
	var out Preview
	err := c.do(ctx, http.MethodDelete, previewsPath("", key), nil, &out)
	return out, err
}

// ListOrgPreviews lists the platform previews an organization can join.
func (c *Client) ListOrgPreviews(ctx context.Context, org string) (PreviewList, error) {
	var out PreviewList
	err := c.do(ctx, http.MethodGet, previewsPath(org, ""), nil, &out)
	return out, err
}

// JoinOrgPreview opts an organization in to a platform preview.
func (c *Client) JoinOrgPreview(ctx context.Context, org, key string) (Preview, error) {
	var out Preview
	err := c.do(ctx, http.MethodPut, previewsPath(org, key), nil, &out)
	return out, err
}

// LeaveOrgPreview removes an organization's opt-in.
func (c *Client) LeaveOrgPreview(ctx context.Context, org, key string) (Preview, error) {
	var out Preview
	err := c.do(ctx, http.MethodDelete, previewsPath(org, key), nil, &out)
	return out, err
}
