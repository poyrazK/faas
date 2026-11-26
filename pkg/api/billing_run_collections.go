package api

// BillingRunCollectionsResponse summarizes recorded Stripe payments across the whole run.
type BillingRunCollectionsResponse struct {
	Totals []BillingRunCollectionTotal `json:"totals"`
}

type BillingRunCollectionTotal struct {
	Currency               string `json:"currency"`
	Livemode               bool   `json:"livemode"`
	AmountPaidMinor        int64  `json:"amount_paid_minor"`
	AmountOutstandingMinor int64  `json:"amount_outstanding_minor"`
	AmountOverdueMinor     int64  `json:"amount_overdue_minor"`
	AwaitingCheck          int    `json:"awaiting_check"`
	InvoicesDue            int    `json:"invoices_due"`
	PaidByDueDate          int    `json:"paid_by_due_date"`
}
