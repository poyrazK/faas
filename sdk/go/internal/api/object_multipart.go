package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ObjectMultipartUpload is Gregale's durable upload session. The upstream S3
// upload ID is intentionally never exposed.
type ObjectMultipartUpload struct {
	ID                  string            `json:"id"`
	Key                 string            `json:"key"`
	SizeBytes           int64             `json:"size_bytes"`
	PartSizeBytes       int64             `json:"part_size_bytes"`
	PartCount           int32             `json:"part_count"`
	ContentType         string            `json:"content_type"`
	State               string            `json:"state"`
	CompletionErrorCode string            `json:"completion_error_code,omitempty"`
	ETag                string            `json:"etag,omitempty"`
	VersionID           string            `json:"version_id,omitempty"`
	Encryption          *ObjectEncryption `json:"encryption,omitempty"`
	ExpiresAt           time.Time         `json:"expires_at"`
	CreatedAt           time.Time         `json:"created_at"`
}

type ObjectMultipartUploadList struct {
	Items      []ObjectMultipartUpload `json:"items"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

type CreateObjectMultipartUploadRequest struct {
	Key         string                 `json:"key"`
	SizeBytes   int64                  `json:"size_bytes"`
	ContentType string                 `json:"content_type,omitempty"`
	Encryption  *ObjectEncryption      `json:"encryption,omitempty"`
	Protection  *ObjectWriteProtection `json:"protection,omitempty"`
}

type ObjectMultipartPartSignRequest struct {
	ExpiresIn int64 `json:"expires_in,omitempty"`
}

type ObjectMultipartCompletedPart struct {
	PartNumber int32  `json:"part_number"`
	ETag       string `json:"etag"`
}

// ObjectMultipartPart is provider-confirmed state for an in-progress upload.
// The provider upload ID remains private; clients use the ETag when completing
// the Gregale session.
type ObjectMultipartPart struct {
	PartNumber   int32     `json:"part_number"`
	ETag         string    `json:"etag"`
	SizeBytes    int64     `json:"size_bytes"`
	LastModified time.Time `json:"last_modified"`
}

type ObjectMultipartPartList struct {
	Items                []ObjectMultipartPart `json:"items"`
	NextPartNumberMarker int32                 `json:"next_part_number_marker,omitempty"`
}

type CompleteObjectMultipartUploadRequest struct {
	Parts []ObjectMultipartCompletedPart `json:"parts"`
}

func (c *Client) CreateObjectMultipartUpload(ctx context.Context, slug, bucket string, req CreateObjectMultipartUploadRequest) (ObjectMultipartUpload, error) {
	var out ObjectMultipartUpload
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/multipart-uploads", req, &out)
	return out, err
}

// ListObjectMultipartUploads lists durable upload sessions so clients can
// recover an upload after losing their local session identifier.
func (c *Client) ListObjectMultipartUploads(ctx context.Context, slug, bucket string, limit int, cursor string) (ObjectMultipartUploadList, error) {
	query := url.Values{"cursor": {cursor}}
	if limit != 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var out ObjectMultipartUploadList
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/multipart-uploads?"+query.Encode(), nil, &out)
	return out, err
}

// ListObjectMultipartParts lists provider-confirmed parts for a resumable
// upload so clients can reconstruct the completion request after a retry.
func (c *Client) ListObjectMultipartParts(ctx context.Context, slug, bucket, upload string, partNumberMarker, limit int) (ObjectMultipartPartList, error) {
	query := url.Values{}
	if partNumberMarker != 0 {
		query.Set("part_number_marker", strconv.Itoa(partNumberMarker))
	}
	if limit != 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var out ObjectMultipartPartList
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/multipart-uploads/"+url.PathEscape(upload)+"/parts?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) GetObjectMultipartUpload(ctx context.Context, slug, bucket, upload string) (ObjectMultipartUpload, error) {
	var out ObjectMultipartUpload
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/buckets/"+url.PathEscape(bucket)+"/multipart-uploads/"+url.PathEscape(upload), nil, &out)
	return out, err
}

func (c *Client) SignObjectMultipartPart(ctx context.Context, slug, bucket, upload string, part int, req ObjectMultipartPartSignRequest) (ObjectSignedRequest, error) {
	var out ObjectSignedRequest
	path := "/v1/apps/" + url.PathEscape(slug) + "/buckets/" + url.PathEscape(bucket) + "/multipart-uploads/" + url.PathEscape(upload) + "/parts/" + strconv.Itoa(part) + "/signed-url"
	err := c.do(ctx, http.MethodPost, path, req, &out)
	return out, err
}

func (c *Client) CompleteObjectMultipartUpload(ctx context.Context, slug, bucket, upload string, req CompleteObjectMultipartUploadRequest) (ObjectMultipartUpload, error) {
	var out ObjectMultipartUpload
	path := "/v1/apps/" + url.PathEscape(slug) + "/buckets/" + url.PathEscape(bucket) + "/multipart-uploads/" + url.PathEscape(upload) + "/complete"
	err := c.do(ctx, http.MethodPost, path, req, &out)
	return out, err
}

func (c *Client) AbortObjectMultipartUpload(ctx context.Context, slug, bucket, upload string) error {
	path := "/v1/apps/" + url.PathEscape(slug) + "/buckets/" + url.PathEscape(bucket) + "/multipart-uploads/" + url.PathEscape(upload)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
