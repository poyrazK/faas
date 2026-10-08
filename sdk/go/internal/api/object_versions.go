package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ObjectVersion contains only logical object data and an owned public selector.
type ObjectVersion struct {
	Key          string    `json:"key"`
	VersionID    string    `json:"version_id"`
	IsLatest     bool      `json:"is_latest"`
	DeleteMarker bool      `json:"delete_marker"`
	SizeBytes    int64     `json:"size_bytes"`
	ETag         string    `json:"etag,omitempty"`
	LastModified time.Time `json:"last_modified"`
	StorageClass string    `json:"storage_class,omitempty"`
}

type ObjectVersionList struct {
	Items               []ObjectVersion `json:"items"`
	CommonPrefixes      []string        `json:"common_prefixes"`
	NextKeyMarker       string          `json:"next_key_marker,omitempty"`
	NextVersionIDMarker string          `json:"next_version_id_marker,omitempty"`
}

type ObjectVersionListRequest struct {
	Prefix, Delimiter, KeyMarker, VersionIDMarker string
	Limit                                         int32
}

func (c *Client) ListObjectBucketVersions(ctx context.Context, app, bucket string, req ObjectVersionListRequest) (ObjectVersionList, error) {
	q := url.Values{}
	for k, v := range map[string]string{"prefix": req.Prefix, "delimiter": req.Delimiter, "key_marker": req.KeyMarker, "version_id_marker": req.VersionIDMarker} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if req.Limit != 0 {
		q.Set("limit", strconv.Itoa(int(req.Limit)))
	}
	var out ObjectVersionList
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/buckets/"+url.PathEscape(bucket)+"/objects/versions?"+q.Encode(), nil, &out)
	return out, err
}
