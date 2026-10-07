package api

import (
	"context"
	"net/http"
	"net/url"
)

func objectNotificationsPath(slug, bucket string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/buckets/" + url.PathEscape(bucket) + "/notifications"
}
func (c *Client) GetObjectBucketNotifications(ctx context.Context, slug, bucket string) (ObjectBucketNotifications, error) {
	var out ObjectBucketNotifications
	err := c.do(ctx, http.MethodGet, objectNotificationsPath(slug, bucket), nil, &out)
	return out, err
}
func (c *Client) PutObjectBucketNotifications(ctx context.Context, slug, bucket string, in ObjectBucketNotificationsRequest) (ObjectBucketNotifications, error) {
	var out ObjectBucketNotifications
	err := c.do(ctx, http.MethodPut, objectNotificationsPath(slug, bucket), in, &out)
	return out, err
}
func (c *Client) DeleteObjectBucketNotifications(ctx context.Context, slug, bucket string) (ObjectBucketNotifications, error) {
	var out ObjectBucketNotifications
	err := c.do(ctx, http.MethodDelete, objectNotificationsPath(slug, bucket), nil, &out)
	return out, err
}
