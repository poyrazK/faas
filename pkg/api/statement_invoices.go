package api

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// CodeBillingProviderUnavailable reports that the account's payment
// provider could not be reached; the request is safe to retry (ADR-1502).
const CodeBillingProviderUnavailable = "billing_provider_unavailable"

// CodeStatementInvoiceExportInProgress reports that another attempt is
// exporting the statement; retry once it ends (ADR-20261019180000).
const CodeStatementInvoiceExportInProgress = "statement_invoice_export_in_progress"

// PutBillingConnectionRequest stores the account's own key for a billing
// provider (ADR-1502, ADR-1507). Stripe takes a restricted key (rk_live_ or
// rk_test_) with write access to invoices and invoice items, Paddle an API
// key with transaction write access, and Lago an API key plus, for the EU
// region or a self-hosted Lago, APIURL.
type PutBillingConnectionRequest struct {
	SecretKey string `json:"secret_key"`
	APIURL    string `json:"api_url,omitempty"`
	// Collection is what the provider does with exported invoices
	// (ADR-1311): none (drafts, the default), send (Stripe, and Paddle for
	// customers with an address, ADR-20261019100333) or charge (Stripe and
	// Lago). DaysUntilDue applies to send; default 30.
	Collection   string `json:"collection,omitempty"`
	DaysUntilDue int    `json:"days_until_due,omitempty"`
	// AutomaticTax has Stripe calculate tax on exported invoices (ADR-1313;
	// Stripe only).
	AutomaticTax bool `json:"automatic_tax,omitempty"`
}

// PutStripeSetupRequest creates a Stripe connection if absent, or rotates
// the exact version reviewed by the operator.
type PutStripeSetupRequest struct {
	SecretKey         string     `json:"secret_key"`
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at,omitempty"`
}

// BillingConnectionResponse describes a stored payment-provider key without
// revealing it.
type BillingConnectionResponse struct {
	Provider string `json:"provider"`
	KeyLast4 string `json:"key_last4"`
	Livemode bool   `json:"livemode"`
	APIURL   string `json:"api_url,omitempty"`
	// Collection and DaysUntilDue are the connection's collection mode
	// (ADR-1311).
	Collection   string    `json:"collection"`
	DaysUntilDue int       `json:"days_until_due"`
	AutomaticTax bool      `json:"automatic_tax"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// CreateStatementInvoiceRequest exports a finalized statement as an invoice
// in Provider (stripe, paddle or lago; default stripe). Customer is the
// provider's customer: a Stripe cus_… ID, a Paddle ctm_… ID, or a Lago
// external customer ID. Omitted, the consumer's or tenant's external_ref is
// used when it is one.
type CreateStatementInvoiceRequest struct {
	Provider string `json:"provider,omitempty"`
	Customer string `json:"customer,omitempty"`
}

// ProductInvoiceExportReview contains the terms shown before an owner confirms an export.
type ProductInvoiceExportReview struct {
	StatementID, Provider, Customer, Currency, Collection string
	AmountMinor                                           int64
	Livemode, AutomaticTax                                bool
	DaysUntilDue                                          int
	Hash                                                  string
}

// ProductPaddleInvoiceIssueReview describes the existing draft to issue.
type ProductPaddleInvoiceIssueReview struct {
	InvoiceID, Customer, Currency, PaymentTermsInterval, Hash string
	AmountMinor                                               int64
	PaymentTermsFrequency                                     int
	Livemode                                                  bool
}

// ProductStripeInvoiceSendReview is the approved recipient and terms of an existing Stripe invoice.
type ProductStripeInvoiceSendReview struct {
	InvoiceID, Recipient, Currency, Hash string
	TotalMinor, AmountDueMinor           int64
	DueAt                                time.Time
	Livemode                             bool
}

// ProductStripeInvoiceSendCheck is the confirmed send and optional Stripe event.
type ProductStripeInvoiceSendCheck struct {
	InvoiceID, EventID string
	EventAt            time.Time
}

// ProductStripeInvoiceReminderReview describes a reminder for an overdue invoice.
type ProductStripeInvoiceReminderReview struct {
	InvoiceID, Recipient, Currency, Hash string
	AmountRemainingMinor                 int64
	DueAt                                time.Time
	Livemode                             bool
}

// StatementInvoiceExportResponse is one statement's invoice in the account's
// billing provider. AmountMinor is in the currency's minor unit (cents).
type StatementInvoiceExportResponse struct {
	StatementID  string `json:"statement_id"`
	Provider     string `json:"provider"`
	Customer     string `json:"customer"`
	Status       string `json:"status"`
	InvoiceID    string `json:"invoice_id,omitempty"`
	DashboardURL string `json:"dashboard_url,omitempty"`
	Currency     string `json:"currency"`
	AmountMinor  int64  `json:"amount_minor"`
	Livemode     bool   `json:"livemode"`
	// Collection is what the provider was asked to do with the invoice
	// (ADR-1311): none, send or charge.
	Collection string `json:"collection,omitempty"`
	// AutomaticTax reports whether Stripe calculated the invoice's tax
	// (ADR-1313).
	AutomaticTax bool       `json:"automatic_tax,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	// Payment fields follow the provider invoice (ADR-1504).
	SourceKind           string     `json:"source_kind,omitempty"`
	PaymentStatus        string     `json:"payment_status,omitempty"`
	AmountPaidMinor      *int64     `json:"amount_paid_minor,omitempty"`
	AmountRemainingMinor *int64     `json:"amount_remaining_minor,omitempty"`
	DueAt                *time.Time `json:"due_at,omitempty"`
	PaidAt               *time.Time `json:"paid_at,omitempty"`
	HostedInvoiceURL     string     `json:"hosted_invoice_url,omitempty"`
	Overdue              bool       `json:"overdue"`
	StatusSyncedAt       *time.Time `json:"status_synced_at,omitempty"`
	StripeSendPending    bool       `json:"stripe_send_pending,omitempty"`
	StripeIssuedAt       *time.Time `json:"stripe_issued_at,omitempty"`
	StripeSentAt         *time.Time `json:"stripe_sent_at,omitempty"`
}

// StripeInvoiceSendReview binds the recipient and terms seen by the owner.
type StripeInvoiceSendReview struct {
	InvoiceID      string    `json:"invoice_id"`
	StripeCustomer string    `json:"stripe_customer"`
	RecipientEmail string    `json:"recipient_email"`
	Currency       string    `json:"currency"`
	AmountDueMinor int64     `json:"amount_due_minor"`
	DueAt          time.Time `json:"due_at"`
	Livemode       bool      `json:"livemode"`
	ReviewToken    string    `json:"review_token"`
}

type SendStripeInvoiceRequest struct {
	ReviewToken string `json:"review_token"`
}

// StatementInvoiceListResponse pages an account's exported invoices.
type StatementInvoiceListResponse struct {
	Invoices   []StatementInvoiceExportResponse `json:"invoices"`
	NextCursor string                           `json:"next_cursor,omitempty"`
}

// ListStatementInvoices pages the account's exported invoices; status is a
// payment status, unpaid, overdue, or empty for all (ADR-1504).
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

func billingConnectionPath(provider string) string {
	return "/v1/account/billing-connections/" + url.PathEscape(provider)
}

// PutBillingConnection stores or replaces the account's key for provider.
func (c *Client) PutBillingConnection(ctx context.Context, provider string, req PutBillingConnectionRequest) (BillingConnectionResponse, error) {
	var out BillingConnectionResponse
	return out, c.do(ctx, "PUT", billingConnectionPath(provider), req, &out)
}

// GetBillingConnection describes the account's stored key for provider.
func (c *Client) GetBillingConnection(ctx context.Context, provider string) (BillingConnectionResponse, error) {
	var out BillingConnectionResponse
	return out, c.do(ctx, "GET", billingConnectionPath(provider), nil, &out)
}

// DeleteBillingConnection removes the account's key for provider.
func (c *Client) DeleteBillingConnection(ctx context.Context, provider string) error {
	return c.do(ctx, "DELETE", billingConnectionPath(provider), nil, nil)
}

// ExportAPIConsumerUsageStatementInvoice creates (or resumes) the invoice
// for a finalized app consumer statement.
func (c *Client) ExportAPIConsumerUsageStatementInvoice(ctx context.Context, slug, consumerID, statementID string, req CreateStatementInvoiceRequest) (StatementInvoiceExportResponse, error) {
	var out StatementInvoiceExportResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/consumers/"+consumerID+"/usage-statements/"+statementID+"/invoice", req, &out)
}

// GetAPIConsumerUsageStatementInvoice reads an app consumer statement's
// export.
func (c *Client) GetAPIConsumerUsageStatementInvoice(ctx context.Context, slug, consumerID, statementID string) (StatementInvoiceExportResponse, error) {
	var out StatementInvoiceExportResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/consumers/"+consumerID+"/usage-statements/"+statementID+"/invoice", nil, &out)
}

// ExportPlatformTenantStatementInvoice creates (or resumes) the invoice for
// a finalized platform tenant statement.
func (c *Client) ExportPlatformTenantStatementInvoice(ctx context.Context, tenantID, statementID string, req CreateStatementInvoiceRequest) (StatementInvoiceExportResponse, error) {
	var out StatementInvoiceExportResponse
	return out, c.do(ctx, "POST", "/v1/account/platform-tenants/"+tenantID+"/usage-statements/"+statementID+"/invoice", req, &out)
}

// GetPlatformTenantStatementInvoice reads a platform tenant statement's
// export.
func (c *Client) GetPlatformTenantStatementInvoice(ctx context.Context, tenantID, statementID string) (StatementInvoiceExportResponse, error) {
	var out StatementInvoiceExportResponse
	return out, c.do(ctx, "GET", "/v1/account/platform-tenants/"+tenantID+"/usage-statements/"+statementID+"/invoice", nil, &out)
}

// BillingProviderKeyMaxBytes bounds a stored billing provider key; real keys
// are ~110 bytes.
const BillingProviderKeyMaxBytes = 512

// BillingProviderKeyNamespace is the secretbox namespace an account's
// billing provider key is sealed under; it binds the ciphertext to its
// provider and account.
func BillingProviderKeyNamespace(provider, accountID string) string {
	return "gregale.billing." + provider + "." + accountID
}
