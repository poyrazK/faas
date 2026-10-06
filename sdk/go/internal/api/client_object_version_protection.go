package api

import (
	"context"
	"net/http"
	"net/url"
)

func versionProtectionPath(slug, bucket, key, version, kind string) string {
	q := url.Values{"key": {key}, "version_id": {version}}
	return "/v1/apps/" + url.PathEscape(slug) + "/buckets/" + url.PathEscape(bucket) + "/objects/protection/" + kind + "?" + q.Encode()
}
func (c *Client) GetObjectVersionRetention(ctx context.Context, slug, bucket, key, version string) (ObjectVersionRetentionResult, error) {
	var out ObjectVersionRetentionResult
	err := c.do(ctx, http.MethodGet, versionProtectionPath(slug, bucket, key, version, "retention"), nil, &out)
	return out, err
}
func (c *Client) PutObjectVersionRetention(ctx context.Context, slug, bucket, key, version string, in ObjectVersionRetentionRequest) (ObjectVersionProtection, error) {
	var out ObjectVersionProtection
	err := c.do(ctx, http.MethodPut, versionProtectionPath(slug, bucket, key, version, "retention"), in, &out)
	return out, err
}
func (c *Client) GetObjectVersionLegalHold(ctx context.Context, slug, bucket, key, version string) (ObjectVersionLegalHoldResult, error) {
	var out ObjectVersionLegalHoldResult
	err := c.do(ctx, http.MethodGet, versionProtectionPath(slug, bucket, key, version, "legal-hold"), nil, &out)
	return out, err
}
func (c *Client) PutObjectVersionLegalHold(ctx context.Context, slug, bucket, key, version string, in ObjectVersionLegalHoldRequest) (ObjectVersionProtection, error) {
	var out ObjectVersionProtection
	err := c.do(ctx, http.MethodPut, versionProtectionPath(slug, bucket, key, version, "legal-hold"), in, &out)
	return out, err
}
func (c *Client) GetObjectVersionProtection(ctx context.Context, slug, bucket, id string) (ObjectVersionProtection, error) {
	var out ObjectVersionProtection
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/protection-operations/"+url.PathEscape(id), nil, &out)
	return out, err
}
