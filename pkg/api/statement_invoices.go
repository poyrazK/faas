package api

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// CodeBillingProviderUnavailable reports that the account's payment
// provider could not be reached; the request is safe to retry (ADR-1300).
const CodeBillingProviderUnavailable = "billing_provider_unavailable"

// PutStripeConnectionRequest stores the account's own Stripe key for
// statement invoice exports (ADR-1300). Use a restricted key (rk_live_ or
// rk_test_) with write access to invoices and invoice items.
type PutStripeConnectionRequest struct {
	SecretKey string `json:"secret_key"`
}

// BillingConnectionResponse describes a stored payment-provider key without
// revealing it.
type BillingConnectionResponse struct {
	Provider  string    `json:"provider"`
	KeyLast4  string    `json:"key_last4"`
	Livemode  bool      `json:"livemode"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateStatementInvoiceRequest exports a finalized statement as a draft
// invoice. Customer is the Stripe customer ID (cus_…); omitted, the
// consumer's or tenant's external_ref is used when it is one.
type CreateStatementInvoiceRequest struct {
	Customer string `json:"customer,omitempty"`
}

// StatementInvoiceExportResponse is one statement's draft invoice in the
// account's Stripe. AmountMinor is in the currency's minor unit (cents).
type StatementInvoiceExportResponse struct {
	StatementID  string     `json:"statement_id"`
	Provider     string     `json:"provider"`
	Customer     string     `json:"customer"`
	Status       string     `json:"status"`
	InvoiceID    string     `json:"invoice_id,omitempty"`
	DashboardURL string     `json:"dashboard_url,omitempty"`
	Currency     string     `json:"currency"`
	AmountMinor  int64      `json:"amount_minor"`
	Livemode     bool       `json:"livemode"`
	CreatedAt    time.Time  `json:"created_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	// Payment fields follow the Stripe invoice (ADR-1302).
	SourceKind           string     `json:"source_kind,omitempty"`
	PaymentStatus        string     `json:"payment_status,omitempty"`
	AmountPaidMinor      *int64     `json:"amount_paid_minor,omitempty"`
	AmountRemainingMinor *int64     `json:"amount_remaining_minor,omitempty"`
	DueAt                *time.Time `json:"due_at,omitempty"`
	PaidAt               *time.Time `json:"paid_at,omitempty"`
	HostedInvoiceURL     string     `json:"hosted_invoice_url,omitempty"`
	Overdue              bool       `json:"overdue"`
	StatusSyncedAt       *time.Time `json:"status_synced_at,omitempty"`
}

// StatementInvoiceListResponse pages an account's exported invoices.
type StatementInvoiceListResponse struct {
	Invoices   []StatementInvoiceExportResponse `json:"invoices"`
	NextCursor string                           `json:"next_cursor,omitempty"`
}

// ListStatementInvoices pages the account's exported invoices; status is a
// payment status, unpaid, overdue, or empty for all (ADR-1302).
func (c *Client) ListStatementInvoices(ctx context.Context, status, cursor string, limit int) (StatementInvoiceListResponse, error) {
	var out StatementInvoiceListResponse
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/account/statement-invoices"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// PutStripeConnection stores or replaces the account's Stripe key.
func (c *Client) PutStripeConnection(ctx context.Context, req PutStripeConnectionRequest) (BillingConnectionResponse, error) {
	var out BillingConnectionResponse
	return out, c.do(ctx, "PUT", "/v1/account/billing-connections/stripe", req, &out)
}

// GetStripeConnection describes the account's stored Stripe key.
func (c *Client) GetStripeConnection(ctx context.Context) (BillingConnectionResponse, error) {
	var out BillingConnectionResponse
	return out, c.do(ctx, "GET", "/v1/account/billing-connections/stripe", nil, &out)
}

// DeleteStripeConnection removes the account's Stripe key.
func (c *Client) DeleteStripeConnection(ctx context.Context) error {
	return c.do(ctx, "DELETE", "/v1/account/billing-connections/stripe", nil, nil)
}

// ExportAPIConsumerUsageStatementToStripe creates (or resumes) the draft
// Stripe invoice for a finalized app consumer statement.
func (c *Client) ExportAPIConsumerUsageStatementToStripe(ctx context.Context, slug, consumerID, statementID string, req CreateStatementInvoiceRequest) (StatementInvoiceExportResponse, error) {
	var out StatementInvoiceExportResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/consumers/"+consumerID+"/usage-statements/"+statementID+"/stripe-invoice", req, &out)
}

// GetAPIConsumerUsageStatementStripeInvoice reads an app consumer
// statement's export.
func (c *Client) GetAPIConsumerUsageStatementStripeInvoice(ctx context.Context, slug, consumerID, statementID string) (StatementInvoiceExportResponse, error) {
	var out StatementInvoiceExportResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/consumers/"+consumerID+"/usage-statements/"+statementID+"/stripe-invoice", nil, &out)
}

// ExportPlatformTenantStatementToStripe creates (or resumes) the draft
// Stripe invoice for a finalized platform tenant statement.
func (c *Client) ExportPlatformTenantStatementToStripe(ctx context.Context, tenantID, statementID string, req CreateStatementInvoiceRequest) (StatementInvoiceExportResponse, error) {
	var out StatementInvoiceExportResponse
	return out, c.do(ctx, "POST", "/v1/account/platform-tenants/"+tenantID+"/usage-statements/"+statementID+"/stripe-invoice", req, &out)
}

// GetPlatformTenantStatementStripeInvoice reads a platform tenant
// statement's export.
func (c *Client) GetPlatformTenantStatementStripeInvoice(ctx context.Context, tenantID, statementID string) (StatementInvoiceExportResponse, error) {
	var out StatementInvoiceExportResponse
	return out, c.do(ctx, "GET", "/v1/account/platform-tenants/"+tenantID+"/usage-statements/"+statementID+"/stripe-invoice", nil, &out)
}
