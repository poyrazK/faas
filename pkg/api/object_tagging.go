package api

import (
	"context"
	"net/http"
	"net/url"
)

type ObjectTaggingRequest struct {
	Tags map[string]string `json:"tags"`
}

type ObjectTaggingResult struct {
	VersionID string            `json:"version_id,omitempty"`
	Tags      map[string]string `json:"tags"`
}

// GetObjectBucketTags returns tags for the current object, or an owned public
// version UUID/null when version is nonempty.
func (c *Client) GetObjectBucketTags(ctx context.Context, app, bucket, key, version string) (ObjectTaggingResult, error) {
	var out ObjectTaggingResult
	err := c.do(ctx, http.MethodGet, objectTagsPath(app, bucket, key, version), nil, &out)
	return out, err
}

// PutObjectBucketTags replaces the entire tag set without creating a version.
func (c *Client) PutObjectBucketTags(ctx context.Context, app, bucket, key, version string, input ObjectTaggingRequest) (ObjectTaggingResult, error) {
	var out ObjectTaggingResult
	err := c.do(ctx, http.MethodPut, objectTagsPath(app, bucket, key, version), input, &out)
	return out, err
}

func (c *Client) DeleteObjectBucketTags(ctx context.Context, app, bucket, key, version string) (ObjectTaggingResult, error) {
	var out ObjectTaggingResult
	err := c.do(ctx, http.MethodDelete, objectTagsPath(app, bucket, key, version), nil, &out)
	return out, err
}

func objectTagsPath(app, bucket, key, version string) string {
	q := url.Values{"key": {key}}
	if version != "" {
		q.Set("version_id", version)
	}
	return "/v1/apps/" + url.PathEscape(app) + "/buckets/" + url.PathEscape(bucket) + "/objects/tags?" + q.Encode()
}
