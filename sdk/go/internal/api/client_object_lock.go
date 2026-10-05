package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) GetObjectBucketObjectLockCapabilities(ctx context.Context, slug, bucket string) (ObjectLockCapabilities, error) {
	var out ObjectLockCapabilities
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/object-lock-capabilities", nil, &out)
	return out, err
}
func (c *Client) GetObjectBucketObjectLock(ctx context.Context, slug, bucket string) (ObjectBucketObjectLock, error) {
	var out ObjectBucketObjectLock
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/object-lock", nil, &out)
	return out, err
}
func (c *Client) PutObjectBucketObjectLock(ctx context.Context, slug, bucket string, configuration ObjectBucketObjectLockConfiguration) (ObjectBucketObjectLock, error) {
	var out ObjectBucketObjectLock
	err := c.do(ctx, http.MethodPut, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/object-lock", ObjectBucketObjectLockRequest{Configuration: configuration}, &out)
	return out, err
}
