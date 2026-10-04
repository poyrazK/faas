package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) GetObjectBucketEncryptionCapabilities(ctx context.Context, slug, bucket string) (ObjectEncryptionCapabilities, error) {
	var out ObjectEncryptionCapabilities
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/encryption-capabilities", nil, &out)
	return out, err
}

func (c *Client) GetObjectBucketEncryption(ctx context.Context, slug, bucket string) (ObjectBucketEncryption, error) {
	var out ObjectBucketEncryption
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/encryption", nil, &out)
	return out, err
}
func (c *Client) PutObjectBucketEncryption(ctx context.Context, slug, bucket string, encryption ObjectEncryption) (ObjectBucketEncryption, error) {
	var out ObjectBucketEncryption
	err := c.do(ctx, http.MethodPut, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/encryption", ObjectBucketEncryptionRequest{Encryption: encryption}, &out)
	return out, err
}
func (c *Client) DeleteObjectBucketEncryption(ctx context.Context, slug, bucket string) (ObjectBucketEncryption, error) {
	var out ObjectBucketEncryption
	err := c.do(ctx, http.MethodDelete, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/encryption", nil, &out)
	return out, err
}
