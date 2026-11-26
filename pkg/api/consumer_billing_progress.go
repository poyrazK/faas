package api

import "time"

// APIConsumerBillingProgressResponse combines metering, pricing and invoice
// facts for the first-invoice setup of one app customer.
type APIConsumerBillingProgressResponse struct {
	ConsumerID      string                               `json:"consumer_id"`
	RegisteredAt    time.Time                            `json:"registered_at"`
	Usage           APIConsumerBillingUsageProgress      `json:"usage"`
	FirstPricedAt   *time.Time                           `json:"first_priced_at,omitempty"`
	Statement       *APIConsumerBillingStatementProgress `json:"statement,omitempty"`
	Invoice         *APIConsumerBillingInvoiceProgress   `json:"invoice,omitempty"`
	FirstLivePaidAt *time.Time                           `json:"first_live_paid_at,omitempty"`
}

type APIConsumerBillingUsageProgress struct {
	Since            time.Time `json:"since"`
	Until            time.Time `json:"until"`
	Status           string    `json:"status"`
	RequestCount     int64     `json:"request_count"`
	UnpricedUnits    int64     `json:"unpriced_units"`
	AmountMillicents int64     `json:"amount_millicents"`
	Currency         string    `json:"currency,omitempty"`
}

type APIConsumerBillingStatementProgress struct {
	ID                  string `json:"id"`
	Status              string `json:"status"`
	AmountDueMillicents int64  `json:"amount_due_millicents"`
	UnpricedUnits       int64  `json:"unpriced_units"`
}

type APIConsumerBillingInvoiceProgress struct {
	ID            string     `json:"id"`
	Provider      string     `json:"provider"`
	PaymentStatus string     `json:"payment_status,omitempty"`
	Livemode      bool       `json:"livemode"`
	SentAt        *time.Time `json:"sent_at,omitempty"`
	PaidAt        *time.Time `json:"paid_at,omitempty"`
}
