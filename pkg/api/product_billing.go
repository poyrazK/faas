package api

import "time"

type ProductCustomerBilling struct {
	Review    ProductBillingReview    `json:"review"`
	Documents ProductBillingDocuments `json:"documents"`
}

// ProductBillingReview is a live, bounded projection of admission-month receipts.
// Finalization requires a closed UTC month, no unresolved receipts, and this hash.
type ProductBillingReview struct {
	TenantID         string               `json:"tenant_id"`
	Period           string               `json:"period"`
	Currency         string               `json:"currency"`
	Closed           bool                 `json:"closed"`
	Starts           int64                `json:"starts"`
	Unresolved       int64                `json:"unresolved"`
	AmountMillicents int64                `json:"amount_millicents"`
	Lines            []ProductBillingLine `json:"lines"`
	// Fees is the month's plan fee (ADR-20261021154511), included in
	// AmountMillicents; omitted when there is none, so earlier reviews hash
	// as before.
	Fees       []ProductBillingFee     `json:"fees,omitempty"`
	ReviewHash string                  `json:"review_hash"`
	Finalized  *ProductBillingDocument `json:"finalized,omitempty"`
}

// ProductBillingFee is the monthly fee of the highest-fee plan a customer
// held in the month, prorated from ProratedFrom in the month of their first
// assignment (ADR-20261021154511).
type ProductBillingFee struct {
	Plan                 string     `json:"plan"`
	Revision             string     `json:"revision"`
	MonthlyFeeMillicents int64      `json:"monthly_fee_millicents"`
	AmountMillicents     int64      `json:"amount_millicents"`
	ProratedFrom         *time.Time `json:"prorated_from,omitempty"`
}

type ProductBillingLine struct {
	Feature             string    `json:"feature"`
	Revision            string    `json:"revision"`
	Plan                string    `json:"plan"`
	UnitPriceMillicents int64     `json:"unit_price_millicents"`
	Starts              int64     `json:"starts"`
	Succeeded           int64     `json:"succeeded"`
	Failed              int64     `json:"failed"`
	Cancelled           int64     `json:"cancelled"`
	Waived              int64     `json:"waived"`
	Unresolved          int64     `json:"unresolved"`
	AmountMillicents    int64     `json:"amount_millicents"`
	ChangedAt           time.Time `json:"changed_at"`
}

// Documents are immutable. Credit notes reduce a finalized statement without
// rewriting it. Each document is handed to the owner's invoicer independently.
type ProductBillingDocument struct {
	ID               string                `json:"id"`
	Kind             string                `json:"kind"`
	TenantID         string                `json:"tenant_id"`
	Period           string                `json:"period"`
	Currency         string                `json:"currency"`
	AmountMillicents int64                 `json:"amount_millicents"`
	StatementID      string                `json:"statement_id,omitempty"`
	OperationID      string                `json:"operation_id,omitempty"`
	Reason           string                `json:"reason,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	Snapshot         *ProductBillingReview `json:"snapshot,omitempty"`
	// Invoice is a statement's export to the account's billing provider
	// (ADR-20261018212918); nil until an export starts.
	Invoice *ProductDocumentInvoice `json:"invoice,omitempty"`
}

// ProductDocumentInvoice is a statement's provider invoice and its synced
// payment status (ADR-1504). Provider, InvoiceID, DashboardURL and
// ExportStatus are owner-only; customers see completed exports only, and
// HostedInvoiceURL is always the provider's own https page.
type ProductDocumentInvoice struct {
	Provider             string `json:"provider,omitempty"`
	InvoiceID            string `json:"invoice_id,omitempty"`
	DashboardURL         string `json:"dashboard_url,omitempty"`
	ExportStatus         string `json:"export_status,omitempty"`
	PaymentStatus        string `json:"payment_status,omitempty"`
	Overdue              bool   `json:"overdue"`
	Currency             string `json:"currency"`
	AmountMinor          int64  `json:"amount_minor"`
	AmountRemainingMinor *int64 `json:"amount_remaining_minor,omitempty"`
	// AmountRemainingMillicents is AmountRemainingMinor in millicents, so
	// clients need not know the currency's decimal places.
	AmountRemainingMillicents *int64     `json:"amount_remaining_millicents,omitempty"`
	DueAt                     *time.Time `json:"due_at,omitempty"`
	PaidAt                    *time.Time `json:"paid_at,omitempty"`
	HostedInvoiceURL          string     `json:"hosted_invoice_url,omitempty"`
}

type ProductBillingDocuments struct {
	Documents  []ProductBillingDocument `json:"documents"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

type ProductBillingReceipts struct {
	Receipts   []ProductReceiptEvidence `json:"receipts"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

type ProductFinalizeRequest struct {
	TenantID     string `json:"tenant_id"`
	Period       string `json:"period"`
	ExpectedHash string `json:"expected_review_hash"`
}

type ProductWaiverRequest struct {
	TenantID string `json:"tenant_id"`
	Reason   string `json:"reason"`
}

type ProductCreditRequest struct {
	ID               string `json:"id"`
	TenantID         string `json:"tenant_id"`
	OperationID      string `json:"operation_id"`
	AmountMillicents int64  `json:"amount_millicents"`
	Reason           string `json:"reason"`
}

type ProductInvoiceHandoff struct {
	DocumentID        string    `json:"document_id"`
	ExternalInvoiceID string    `json:"external_invoice_id"`
	Currency          string    `json:"currency"`
	AmountMillicents  int64     `json:"amount_millicents"`
	CreatedAt         time.Time `json:"created_at"`
}

// ProductInvoiceExportRequest exports a finalized statement to the account's
// own billing provider (ADR-20261018212918). Provider defaults to stripe;
// customer defaults to the tenant's external reference when it is one of
// that provider's customer IDs.
type ProductInvoiceExportRequest struct {
	TenantID string `json:"tenant_id"`
	Provider string `json:"provider,omitempty"`
	Customer string `json:"customer,omitempty"`
}

type ProductHandoffRequest struct {
	TenantID          string `json:"tenant_id"`
	ExternalInvoiceID string `json:"external_invoice_id"`
}
