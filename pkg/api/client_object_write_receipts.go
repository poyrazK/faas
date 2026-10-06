package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

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
