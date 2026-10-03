package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// ObjectBucket is a customer-owned logical bucket. Physical upstream names,
// operator identities, credentials and operation leases are not public fields.
type ObjectBucket struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Scope     string    `json:"scope"`
	Region    string    `json:"region"`
	State     string    `json:"state"`
	Public    bool      `json:"public"`
	ServeAt   string    `json:"serve_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ObjectBucketList struct {
	Items                  []ObjectBucket `json:"items"`
	Enabled                bool           `json:"enabled"`
	Regions                []string       `json:"regions"`
	DefaultRegion          string         `json:"default_region"`
	MaxUploadBytes         int64          `json:"max_upload_bytes"`
	MaxBucketsPerApp       int            `json:"max_buckets_per_app"`
	MaxSinglePutBytes      int64          `json:"max_single_put_bytes,omitempty"`
	MaxPartBytes           int64          `json:"max_part_bytes,omitempty"`
	TransferTimeoutSeconds int64          `json:"transfer_timeout_seconds,omitempty"`
	UploadProfile          string         `json:"upload_profile,omitempty"`
}

func (c *Client) ListObjectBuckets(ctx context.Context, slug string) (ObjectBucketList, error) {
	var out ObjectBucketList
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/buckets", nil, &out)
	return out, err
}
