package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// CreateObjectBucketRequest describes logical placement. Empty scope/region
// select the server defaults; retries use the same app, scope and name.
type CreateObjectBucketRequest struct {
	Name   string `json:"name"`
	Scope  string `json:"scope,omitempty"`
	Region string `json:"region,omitempty"`
}

// BucketObject is one item from the upstream object listing, not a usage bill.
type BucketObject struct {
	Key          string    `json:"key"`
	SizeBytes    int64     `json:"size_bytes"`
	LastModified time.Time `json:"last_modified"`
}

type BucketObjectPage struct {
	Items      []BucketObject `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

func (c *Client) ListObjectBuckets(ctx context.Context, slug string) (ObjectBucketList, error) {
	var out ObjectBucketList
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/buckets", nil, &out)
	return out, err
}

func (c *Client) CreateObjectBucket(ctx context.Context, slug string, req CreateObjectBucketRequest) (ObjectBucket, error) {
	var out ObjectBucket
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/buckets", req, &out)
	return out, err
}

func (c *Client) DeleteObjectBucket(ctx context.Context, slug, bucket string) error {
	return c.do(ctx, http.MethodDelete, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket), nil, nil)
}

func (c *Client) ListBucketObjects(ctx context.Context, slug, bucket, prefix, cursor string, limit int) (BucketObjectPage, error) {
	query := url.Values{"prefix": {prefix}, "cursor": {cursor}}
	if limit != 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var out BucketObjectPage
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/objects?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) DeleteBucketObject(ctx context.Context, slug, bucket, key string) error {
	return c.do(ctx, http.MethodDelete, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/objects?"+url.Values{"key": {key}}.Encode(), nil, nil)
}

// SignBucketObject only contacts Gregale. Execute the returned capability with
// a separate HTTP client; never send the Gregale token to its provider URL.
func (c *Client) SignBucketObject(ctx context.Context, slug, bucket string, req ObjectSignRequest) (ObjectSignedRequest, error) {
	var out ObjectSignedRequest
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/signed-url", req, &out)
	return out, err
}
