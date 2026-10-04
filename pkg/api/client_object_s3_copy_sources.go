package api

import (
	"context"
	"net/http"
	"net/url"
)

func copySourcesPath(slug, bucket, credential string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/buckets/" + url.PathEscape(bucket) + "/s3-credentials/" + url.PathEscape(credential) + "/copy-sources"
}
func (c *Client) ListObjectS3CopySources(ctx context.Context, slug, bucket, credential string) (ObjectS3CopySourceList, error) {
	var out ObjectS3CopySourceList
	err := c.do(ctx, http.MethodGet, copySourcesPath(slug, bucket, credential), nil, &out)
	return out, err
}
func (c *Client) SetObjectS3CopySource(ctx context.Context, slug, bucket, credential, source string, req SetObjectS3CopySourceRequest) (ObjectS3CopySource, error) {
	var out ObjectS3CopySource
	err := c.do(ctx, http.MethodPut, copySourcesPath(slug, bucket, credential)+"/"+url.PathEscape(source), req, &out)
	return out, err
}
func (c *Client) DeleteObjectS3CopySource(ctx context.Context, slug, bucket, credential, source string) error {
	return c.do(ctx, http.MethodDelete, copySourcesPath(slug, bucket, credential)+"/"+url.PathEscape(source), nil, nil)
}
