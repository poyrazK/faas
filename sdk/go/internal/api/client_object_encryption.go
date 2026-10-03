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
