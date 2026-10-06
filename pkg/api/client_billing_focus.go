package api

import (
	"context"
	"net/http"
	"net/url"
)

// ExportFOCUSInvoices downloads a partial FOCUS 1.4 Invoice Detail projection
// for a YYYY-MM month (filtered by invoice period end). An empty format selects
// zip, containing CSV and matching metadata. Other formats are csv or metadata.
func (c *Client) ExportFOCUSInvoices(ctx context.Context, month, format string) ([]byte, error) {
	q := url.Values{"month": []string{month}}
	if format != "" {
		q.Set("format", format)
	}
	var body []byte
	if err := c.doBytes(ctx, http.MethodGet, "/v1/billing/focus?"+q.Encode(), nil, &body); err != nil {
		return nil, err
	}
	return body, nil
}
