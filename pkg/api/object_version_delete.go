package api

import (
	"context"
	"net/http"
	"net/url"
)

// ObjectVersionDeleteResult identifies the permanently removed version. A
// repeated delete can return a false marker flag when the version is gone.
type ObjectVersionDeleteResult struct {
	VersionID    string `json:"version_id"`
	DeleteMarker bool   `json:"delete_marker"`
}

// DeleteObjectBucketVersion removes an immutable public version. The mutable
// null selector is unsupported. Capacity is reclaimed by verified inventory.
func (c *Client) DeleteObjectBucketVersion(ctx context.Context, app, bucket, key, version string) (ObjectVersionDeleteResult, error) {
	var out ObjectVersionDeleteResult
	q := url.Values{"key": {key}, "version_id": {version}}
	err := c.do(ctx, http.MethodDelete, "/v1/apps/"+url.PathEscape(app)+"/buckets/"+url.PathEscape(bucket)+"/objects/versions?"+q.Encode(), nil, &out)
	return out, err
}
