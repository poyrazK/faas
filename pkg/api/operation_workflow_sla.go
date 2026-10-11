package api

import "time"

// OperationWorkflowStateSLA evaluates the observed current state visit against its pinned budget.
type OperationWorkflowStateSLA struct {
	EvaluatedAt      time.Time  `json:"evaluated_at"`
	WarningPercent   *int64     `json:"warning_percent,omitempty"`
	WarningAt        *time.Time `json:"warning_at,omitempty"`
	BudgetSeconds    int64      `json:"budget_seconds"`
	Status           string     `json:"status"`
	HistoryComplete  bool       `json:"history_complete"`
	EnteredAt        *time.Time `json:"entered_at,omitempty"`
	DueAt            *time.Time `json:"due_at,omitempty"`
	ElapsedSeconds   *int64     `json:"elapsed_seconds,omitempty"`
	RemainingSeconds *int64     `json:"remaining_seconds,omitempty"`
	BreachedSeconds  *int64     `json:"breached_seconds,omitempty"`
}
