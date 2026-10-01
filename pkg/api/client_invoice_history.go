package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// BackfillInvoiceHistory imports one page of missing documents from the active billing provider.
func (c *Client) BackfillInvoiceHistory(ctx context.Context, cursor string, limit int) (InvoiceHistoryBackfillResponse, error) {
	var result InvoiceHistoryBackfillResponse
	if limit == 0 {
		limit = MaxInvoiceHistoryPageSize
	}
	client := *c.http
	client.Timeout = InvoiceHistoryTimeout
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	err := c.doWithClientAndIdempotencyKey(ctx, &client, http.MethodPost, "/v1/invoices/backfill?"+query.Encode(), nil, &result, "")
	return result, err
}
