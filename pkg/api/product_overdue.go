package api

import (
	"net/url"
	"time"
)

// CodeProductInvoiceOverdue refuses a new Product Control start from a
// customer held for an overdue invoice (ADR-20261018212918).
const CodeProductInvoiceOverdue = "product_invoice_overdue"

// PutProductOverduePolicyRequest sets an app's overdue policy: a customer
// whose exported statement is unpaid this many days after its due date
// cannot start new work (0 holds as soon as the invoice is past due).
type PutProductOverduePolicyRequest struct {
	BlockOverdueAfterDays *int `json:"block_overdue_after_days"`
}

type ProductOverduePolicy struct {
	AppID                 string    `json:"app_id"`
	BlockOverdueAfterDays int       `json:"block_overdue_after_days"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// ProductOverdueHold is one held customer and the oldest overdue statement
// holding it. A hold is not the owner's suspended flag.
type ProductOverdueHold struct {
	TenantID         string    `json:"tenant_id"`
	DocumentID       string    `json:"document_id"`
	ExportID         string    `json:"export_id"`
	DueAt            time.Time `json:"due_at"`
	Since            time.Time `json:"since"`
	HostedInvoiceURL string    `json:"hosted_invoice_url,omitempty"`
}

type ProductOverdueHolds struct {
	Holds []ProductOverdueHold `json:"holds"`
}

// ProductCustomerOverdueHold is what a held customer sees: the due date and
// the provider's own invoice page.
type ProductCustomerOverdueHold struct {
	DueAt            time.Time `json:"due_at"`
	HostedInvoiceURL string    `json:"hosted_invoice_url,omitempty"`
}

// ProviderHostedURL returns raw when it is a plain https URL with a host and
// no credentials, and "" otherwise. Gregale only links to a provider's own
// invoice pages and never renders payment forms.
func ProviderHostedURL(raw string) string {
	link, err := url.Parse(raw)
	if err != nil || link.Scheme != "https" || link.Host == "" || link.User != nil {
		return ""
	}
	return link.String()
}
