package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type ObjectUploadRoute struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	BucketID            string            `json:"bucket_id"`
	KeyPrefix           string            `json:"key_prefix,omitempty"`
	MaxBytes            int64             `json:"max_bytes"`
	AllowedContentTypes []string          `json:"allowed_content_types,omitempty"`
	Enabled             bool              `json:"enabled"`
	Encryption          *ObjectEncryption `json:"encryption,omitempty"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

type ObjectUploadRouteList struct {
	Items []ObjectUploadRoute `json:"items"`
}

type CreateObjectUploadRouteRequest struct {
	Name                string            `json:"name"`
	BucketID            string            `json:"bucket_id"`
	KeyPrefix           string            `json:"key_prefix,omitempty"`
	MaxBytes            int64             `json:"max_bytes"`
	AllowedContentTypes []string          `json:"allowed_content_types,omitempty"`
	Enabled             *bool             `json:"enabled,omitempty"`
	Encryption          *ObjectEncryption `json:"encryption,omitempty"`
}

// ListObjectUploadRoutes returns the policy-controlled upload endpoints for an app.
func (c *Client) ListObjectUploadRoutes(ctx context.Context, slug string) (ObjectUploadRouteList, error) {
	var out ObjectUploadRouteList
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/upload-routes", nil, &out)
	return out, err
}

// CreateObjectUploadRoute creates or updates an authenticated upload endpoint.
func (c *Client) CreateObjectUploadRoute(ctx context.Context, slug string, req CreateObjectUploadRouteRequest) (ObjectUploadRoute, error) {
	var out ObjectUploadRoute
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/upload-routes", req, &out)
	return out, err
}

// DeleteObjectUploadRoute stops new uploads for the named endpoint without
// deleting objects already written through it.
func (c *Client) DeleteObjectUploadRoute(ctx context.Context, slug, route string) error {
	return c.do(ctx, http.MethodDelete, "/v1/apps/"+url.PathEscape(slug)+"/upload-routes/"+url.PathEscape(route), nil, nil)
}
