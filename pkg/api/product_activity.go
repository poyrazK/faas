package api

import "time"

// A page is an observation of retained Product Control records, not a complete
// audit log or a snapshot carried across requests. Events never contain tokens,
// signed reviews, operation input/results, or billing snapshots.
type ProductCustomerActivityPage struct {
	AccountID    string                         `json:"account_id"`
	AppID        string                         `json:"app_id"`
	TenantID     string                         `json:"tenant_id"`
	ObservedAt   time.Time                      `json:"observed_at"`
	HistoryScope string                         `json:"history_scope"`
	Events       []ProductCustomerActivityEvent `json:"events"`
	NextCursor   string                         `json:"next_cursor,omitempty"`
}

type ProductCustomerActivityEvent struct {
	ID         string                     `json:"id"`
	Kind       string                     `json:"kind"`
	SourceID   string                     `json:"source_id"`
	OccurredAt time.Time                  `json:"occurred_at"`
	Period     string                     `json:"period,omitempty"`
	Assignment *ProductActivityAssignment `json:"assignment,omitempty"`
	Terms      *ProductOperationReceipt   `json:"terms,omitempty"`
	Outcome    OperationState             `json:"outcome,omitempty"`
	Reason     string                     `json:"reason,omitempty"`
	Document   *ProductBillingDocument    `json:"document,omitempty"`
	Handoff    *ProductInvoiceHandoff     `json:"handoff,omitempty"`
	Milestone  *ProductOnboardingSuccess  `json:"milestone,omitempty"`
	ReviewURL  string                     `json:"review_url,omitempty"`
}

type ProductActivityAssignment struct {
	Before    *ProductCustomer `json:"before,omitempty"`
	After     ProductCustomer  `json:"after"`
	ActorRole string           `json:"actor_role,omitempty"`
}
