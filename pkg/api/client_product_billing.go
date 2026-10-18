package api

import (
	"context"
	"net/http"
	"net/url"
)

func productBillingPath(app, tenant, period, suffix, cursor string) string {
	query := url.Values{"tenant_id": {tenant}, "period": {period}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	return productPath(app) + "/billing/" + suffix + "?" + query.Encode()
}

func (c *Client) ReviewProductBilling(ctx context.Context, app, tenant, period string) (ProductBillingReview, error) {
	var out ProductBillingReview
	err := c.do(ctx, http.MethodGet, productBillingPath(app, tenant, period, "review", ""), nil, &out)
	return out, err
}

func (c *Client) ListProductBillingReceipts(ctx context.Context, app, tenant, period, cursor string) (ProductBillingReceipts, error) {
	var out ProductBillingReceipts
	err := c.do(ctx, http.MethodGet, productBillingPath(app, tenant, period, "receipts", cursor), nil, &out)
	return out, err
}

func (c *Client) ListProductBillingDocuments(ctx context.Context, app, tenant, period, cursor string) (ProductBillingDocuments, error) {
	var out ProductBillingDocuments
	err := c.do(ctx, http.MethodGet, productBillingPath(app, tenant, period, "documents", cursor), nil, &out)
	return out, err
}

func (c *Client) FinalizeProductBilling(ctx context.Context, app string, req ProductFinalizeRequest) (ProductBillingDocument, error) {
	var out ProductBillingDocument
	err := c.do(ctx, http.MethodPost, productBillingPath(app, req.TenantID, req.Period, "statements/finalize", ""), req, &out)
	return out, err
}

func (c *Client) WaiveProductReceipt(ctx context.Context, app, period, operation string, req ProductWaiverRequest) (ProductReceiptEvidence, error) {
	var out ProductReceiptEvidence
	err := c.do(ctx, http.MethodPost, productBillingPath(app, req.TenantID, period, "receipts/"+url.PathEscape(operation)+"/waive", ""), req, &out)
	return out, err
}

func (c *Client) CreditProductReceipt(ctx context.Context, app, period string, req ProductCreditRequest) (ProductBillingDocument, error) {
	var out ProductBillingDocument
	err := c.do(ctx, http.MethodPost, productBillingPath(app, req.TenantID, period, "credits", ""), req, &out)
	return out, err
}

func (c *Client) HandoffProductBilling(ctx context.Context, app, period, document string, req ProductHandoffRequest) (ProductInvoiceHandoff, error) {
	var out ProductInvoiceHandoff
	err := c.do(ctx, http.MethodPost, productBillingPath(app, req.TenantID, period, "documents/"+url.PathEscape(document)+"/handoff", ""), req, &out)
	return out, err
}

func (c *Client) GetProductBillingHandoff(ctx context.Context, app, tenant, period, document string) (ProductInvoiceHandoff, error) {
	var out ProductInvoiceHandoff
	err := c.do(ctx, http.MethodGet, productBillingPath(app, tenant, period, "documents/"+url.PathEscape(document)+"/handoff", ""), nil, &out)
	return out, err
}

func (c *Client) GetProductCustomerBilling(ctx context.Context, app, period, cursor string) (ProductCustomerBilling, error) {
	var out ProductCustomerBilling
	query := url.Values{"app": {app}, "period": {period}, "cursor": {cursor}}
	err := c.do(ctx, http.MethodGet, "/v1/platform-tenant-self/product/billing?"+query.Encode(), nil, &out)
	return out, err
}
