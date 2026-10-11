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

type ObjectEncryption struct {
	Algorithm        string `json:"algorithm,omitempty"`
	KeyID            string `json:"key_id,omitempty"`
	BucketKeyEnabled *bool  `json:"bucket_key_enabled,omitempty"`
	Context          string `json:"context,omitempty"`
}

type ObjectSignRequest struct {
	VersionID          string                 `json:"version_id,omitempty"`
	IfMatch            string                 `json:"if_match,omitempty"`
	IfNoneMatch        string                 `json:"if_none_match,omitempty"`
	Method             string                 `json:"method"`
	Key                string                 `json:"key"`
	ExpiresIn          int64                  `json:"expires_in,omitempty"`
	SizeBytes          *int64                 `json:"size_bytes,omitempty"`
	ContentType        string                 `json:"content_type,omitempty"`
	CacheControl       string                 `json:"cache_control,omitempty"`
	ContentDisposition string                 `json:"content_disposition,omitempty"`
	ContentEncoding    string                 `json:"content_encoding,omitempty"`
	ContentLanguage    string                 `json:"content_language,omitempty"`
	Metadata           map[string]string      `json:"metadata,omitempty"`
	Tags               map[string]string      `json:"tags,omitempty"`
	Encryption         *ObjectEncryption      `json:"encryption,omitempty"`
	Protection         *ObjectWriteProtection `json:"protection,omitempty"`
}

type ObjectSignedRequest struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
	UploadID  string            `json:"upload_id,omitempty"`
}

// SignBucketObject contacts the control API. Execute its returned URL using
// ordinary HTTP with only the returned headers, without the API bearer token.
func (c *Client) SignBucketObject(ctx context.Context, slug, bucket string, req ObjectSignRequest) (ObjectSignedRequest, error) {
	var out ObjectSignedRequest
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/signed-url", req, &out)
	return out, err
}

func (c *Client) ListObjectBuckets(ctx context.Context, slug string) (ObjectBucketList, error) {
	var out ObjectBucketList
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/buckets", nil, &out)
	return out, err
}
