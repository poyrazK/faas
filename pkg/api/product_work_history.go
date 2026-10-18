package api

import "time"

// ProductWorkHistoryFilter applies to original receipt metadata. Dates are UTC
// admission dates, inclusive at both ends. An empty state is normalized to all.
type ProductWorkHistoryFilter struct {
	OperationID string `json:"operation_id,omitempty"`
	Feature     string `json:"feature,omitempty"`
	State       string `json:"state"`
	From        string `json:"from,omitempty"`
	Through     string `json:"through,omitempty"`
}

// ProductWorkHistoryEntry contains no request, result payload or billing data.
type ProductWorkHistoryEntry struct {
	OperationID string            `json:"operation_id"`
	Feature     string            `json:"feature"`
	Plan        string            `json:"plan"`
	Period      string            `json:"period"`
	AdmittedAt  time.Time         `json:"admitted_at"`
	Outcome     ProductRunOutcome `json:"outcome"`
}

// Retained receipts are not a complete audit journal. Result availability and
// outcomes are observed anew on every page; admission order stays immutable.
type ProductWorkHistoryPage struct {
	AccountID    string                    `json:"account_id"`
	AppID        string                    `json:"app_id"`
	TenantID     string                    `json:"tenant_id"`
	Filter       ProductWorkHistoryFilter  `json:"filter"`
	Entries      []ProductWorkHistoryEntry `json:"entries"`
	ObservedAt   time.Time                 `json:"observed_at"`
	HistoryScope string                    `json:"history_scope"`
	NextCursor   string                    `json:"next_cursor,omitempty"`
}
