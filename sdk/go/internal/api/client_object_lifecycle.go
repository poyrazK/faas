package api

import (
	"context"
	"net/http"
	"net/url"
)

func objectLifecyclePath(slug, bucket string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/buckets/" + url.PathEscape(bucket) + "/lifecycle"
}
func (c *Client) GetObjectBucketLifecycle(ctx context.Context, slug, bucket string) (ObjectBucketLifecycle, error) {
	var out ObjectBucketLifecycle
	err := c.do(ctx, http.MethodGet, objectLifecyclePath(slug, bucket), nil, &out)
	return out, err
}
func (c *Client) PutObjectBucketLifecycle(ctx context.Context, slug, bucket string, in ObjectBucketLifecycleRequest) (ObjectBucketLifecycle, error) {
	var out ObjectBucketLifecycle
	err := c.do(ctx, http.MethodPut, objectLifecyclePath(slug, bucket), in, &out)
	return out, err
}
func (c *Client) DeleteObjectBucketLifecycle(ctx context.Context, slug, bucket string) (ObjectBucketLifecycle, error) {
	var out ObjectBucketLifecycle
	err := c.do(ctx, http.MethodDelete, objectLifecyclePath(slug, bucket), nil, &out)
	return out, err
}
func (c *Client) CreateObjectLifecycleScan(ctx context.Context, slug, bucket string) (ObjectLifecycleScan, error) {
	var out ObjectLifecycleScan
	err := c.do(ctx, http.MethodPost, objectLifecyclePath(slug, bucket)+"/scans", nil, &out)
	return out, err
}
func (c *Client) GetObjectLifecycleScan(ctx context.Context, slug, bucket, scan string) (ObjectLifecycleScan, error) {
	var out ObjectLifecycleScan
	err := c.do(ctx, http.MethodGet, objectLifecyclePath(slug, bucket)+"/scans/"+url.PathEscape(scan), nil, &out)
	return out, err
}
