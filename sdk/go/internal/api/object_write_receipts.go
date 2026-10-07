package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type ObjectWriteReceipt struct {
	ID          string            `json:"id"`
	BucketID    string            `json:"bucket_id"`
	Key         string            `json:"key"`
	Operation   string            `json:"operation"`
	Bytes       int64             `json:"bytes"`
	ContentType string            `json:"content_type"`
	ETag        string            `json:"etag"`
	Status      string            `json:"status"`
	ErrorCode   string            `json:"error_code,omitempty"`
	Encryption  *ObjectEncryption `json:"encryption,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
}

type ObjectWriteReceiptList struct {
	Items      []ObjectWriteReceipt `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

func objectWriteReceiptsPath(slug, bucket string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/buckets/" + url.PathEscape(bucket) + "/write-receipts"
}

func (c *Client) GetObjectWriteReceipt(ctx context.Context, slug, bucket, id string) (ObjectWriteReceipt, error) {
	var out ObjectWriteReceipt
	err := c.do(ctx, http.MethodGet, objectWriteReceiptsPath(slug, bucket)+"/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) ListObjectWriteReceipts(ctx context.Context, slug, bucket, status string, limit int, cursor string) (ObjectWriteReceiptList, error) {
	q := url.Values{"status": {status}, "cursor": {cursor}}
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out ObjectWriteReceiptList
	err := c.do(ctx, http.MethodGet, objectWriteReceiptsPath(slug, bucket)+"?"+q.Encode(), nil, &out)
	return out, err
}
