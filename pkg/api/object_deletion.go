package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// ObjectDeletionRequest identifies one mutation. Reuse ID for retries of the
// same key and selector; a new ID requests a distinct deletion.
type ObjectDeletionRequest struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	VersionID string `json:"version_id,omitempty"`
}

// ObjectDeletion retains uncertain attempts without expiry. Recovery can retry
// exact immutable versions; mutable mutations require unique completion proof.
type ObjectDeletion struct {
	ID            string    `json:"id"`
	BucketID      string    `json:"bucket_id"`
	Key           string    `json:"key"`
	Selector      string    `json:"selector"`
	State         string    `json:"state"`
	VersionID     string    `json:"version_id,omitempty"`
	DeleteMarker  bool      `json:"delete_marker"`
	LastErrorCode string    `json:"last_error_code,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (c *Client) CreateObjectDeletion(ctx context.Context, app, bucket string, request ObjectDeletionRequest) (ObjectDeletion, error) {
	var out ObjectDeletion
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(app)+"/buckets/"+url.PathEscape(bucket)+"/objects/deletions", request, &out)
	return out, err
}

func (c *Client) GetObjectDeletion(ctx context.Context, app, bucket, id string) (ObjectDeletion, error) {
	var out ObjectDeletion
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/buckets/"+url.PathEscape(bucket)+"/objects/deletions/"+url.PathEscape(id), nil, &out)
	return out, err
}
