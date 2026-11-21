package api

import (
	"context"
	"net/http"
	"net/url"
)

// RefreshInvoiceFacts enriches an existing owned invoice from its provider.
func (c *Client) RefreshInvoiceFacts(ctx context.Context, invoiceID string) (InvoiceRefreshResponse, error) {
	var result InvoiceRefreshResponse
	client := c.operationHTTP(InvoiceRefreshTimeout)
	err := c.doWithClientAndIdempotencyKey(ctx, client, http.MethodPost, "/v1/invoices/"+url.PathEscape(invoiceID)+"/refresh", nil, &result, "")
	return result, err
}
