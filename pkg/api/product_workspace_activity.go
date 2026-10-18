package api

import "time"

// ProductWorkspaceActivityEvent is a customer projection, separate from the
// owner's activity evidence. It contains no source documents or internal notes.
type ProductWorkspaceActivityEvent struct {
	ID          string                        `json:"id"`
	Kind        string                        `json:"kind"`
	ReferenceID string                        `json:"reference_id"`
	OccurredAt  time.Time                     `json:"occurred_at"`
	Feature     string                        `json:"feature,omitempty"`
	HelpKind    string                        `json:"help_kind,omitempty"`
	Plan        *ProductWorkspaceActivityPlan `json:"plan,omitempty"`
	Billing     *ProductWorkspaceActivityBill `json:"billing,omitempty"`
}

type ProductWorkspaceActivityPlan struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

type ProductWorkspaceActivityBill struct {
	Period           string `json:"period"`
	Currency         string `json:"currency"`
	AmountMillicents int64  `json:"amount_millicents"` // Statements are nonnegative; credits are negative.
}

// This is retained history, not a complete audit journal. Help entries reflect
// the latest retained update; attention entries exist while a run needs review.
type ProductWorkspaceActivityPage struct {
	AccountID    string                          `json:"account_id"`
	AppID        string                          `json:"app_id"`
	TenantID     string                          `json:"tenant_id"`
	Filter       string                          `json:"filter"`
	Events       []ProductWorkspaceActivityEvent `json:"events"`
	NextCursor   string                          `json:"next_cursor,omitempty"`
	ObservedAt   time.Time                       `json:"observed_at"`
	HistoryScope string                          `json:"history_scope"`
}
