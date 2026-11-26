package api

import "time"

// BillingRunDraftExportPageSize bounds local review work and one approval.
const BillingRunDraftExportPageSize = 10

// CodeStripeCustomerMissing marks a review row that can link a Stripe customer.
const CodeStripeCustomerMissing = "stripe_customer_missing"

// BillingRunDraftExportRow is the live review of one billing run item.
type BillingRunDraftExportRow struct {
	ID                  string                          `json:"id"`
	ConsumerID          string                          `json:"consumer_id,omitempty"`
	CustomerName        string                          `json:"customer_name"`
	StripeCustomer      string                          `json:"stripe_customer,omitempty"`
	StatementID         string                          `json:"statement_id,omitempty"`
	Revision            int                             `json:"revision,omitempty"`
	Currency            string                          `json:"currency,omitempty"`
	AmountDueMillicents *int64                          `json:"amount_due_millicents,omitempty"`
	Status              string                          `json:"status"`
	Reason              string                          `json:"reason,omitempty"`
	BlockerCode         string                          `json:"blocker_code,omitempty"`
	Invoice             *StatementInvoiceExportResponse `json:"invoice,omitempty"`
}

type BillingRunDraftExportReview struct {
	Items               []BillingRunDraftExportRow `json:"items"`
	NextCursor          string                     `json:"next_cursor,omitempty"`
	Livemode            *bool                      `json:"livemode,omitempty"`
	ConnectionUpdatedAt *time.Time                 `json:"connection_updated_at,omitempty"`
	ConnectionProblem   string                     `json:"connection_problem,omitempty"`
}

// ApproveBillingRunDraftExportRequest binds an approval to all the values
// seen in the review. Each selected statement has its own export receipt.
type ApproveBillingRunDraftExportRequest struct {
	ConsumerID          string    `json:"consumer_id"`
	StatementID         string    `json:"statement_id"`
	Revision            int       `json:"revision"`
	Currency            string    `json:"currency"`
	AmountDueMillicents int64     `json:"amount_due_millicents"`
	StripeCustomer      string    `json:"stripe_customer"`
	Livemode            *bool     `json:"livemode"`
	ConnectionUpdatedAt time.Time `json:"connection_updated_at"`
}

type LinkBillingRunStripeCustomerRequest struct {
	StripeCustomer      string    `json:"stripe_customer"`
	StatementID         string    `json:"statement_id"`
	Livemode            *bool     `json:"livemode"`
	ConnectionUpdatedAt time.Time `json:"connection_updated_at"`
}
