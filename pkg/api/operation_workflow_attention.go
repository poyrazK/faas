package api

import "time"

// OperationWorkflowAttentionEntry is a retained workflow snapshot needing investigation.
type OperationWorkflowAttentionEntry struct {
	DependencyAttention []OperationWorkflowRelatedInstance `json:"dependency_attention,omitempty"`
	AppID               string                             `json:"app_id"`
	Scope               string                             `json:"scope"`
	PlatformTenantID    string                             `json:"platform_tenant_id,omitempty"`
	Subject             OperationSubject                   `json:"subject"`
	OperationID         string                             `json:"operation_id"`
	State               OperationWorkflowState             `json:"state"`
	Reasons             []string                           `json:"reasons"`
}

type OperationWorkflowAttentionResponse struct {
	Items       []OperationWorkflowAttentionEntry `json:"items"`
	EvaluatedAt time.Time                         `json:"evaluated_at"`
	NextCursor  string                            `json:"next_cursor,omitempty"`
}

type OperationWorkflowAttentionOptions struct {
	DependencyStatus, RequiredOutcomeCode                                          string
	AppID, Scope, TenantID, Workflow, TargetOperation, Reason, Cursor, BlockerCode string
	Limit                                                                          int
}

// Summary totals cover every matching retained workflow, independent of group pagination.
type OperationWorkflowAttentionStats struct {
	DependencyWorkflowCount   int64      `json:"dependency_workflow_count"`
	DependencyCount           int64      `json:"dependency_count"`
	OverdueWorkflowCount      int64      `json:"overdue_workflow_count"`
	EarliestOverdueDeadlineAt *time.Time `json:"earliest_overdue_deadline_at,omitempty"`
	LongestOverdueSeconds     *int64     `json:"longest_overdue_seconds,omitempty"`
	WorkflowCount             int64      `json:"workflow_count"`
	BlockedWorkflowCount      int64      `json:"blocked_workflow_count"`
	StaleWorkflowCount        int64      `json:"stale_workflow_count"`
	BlockerCount              int64      `json:"blocker_count"`
	UnknownAgeBlockers        int64      `json:"unknown_age_blockers"`
	OldestBlockerAt           *time.Time `json:"oldest_blocker_at,omitempty"`
	OldestBlockerAgeSeconds   *int64     `json:"oldest_blocker_age_seconds,omitempty"`
}
type OperationWorkflowAttentionGroup struct {
	Value string                          `json:"value"`
	Stats OperationWorkflowAttentionStats `json:"stats"`
}
type OperationWorkflowAttentionSummary struct {
	GroupBy     string                            `json:"group_by"`
	EvaluatedAt time.Time                         `json:"evaluated_at"`
	Totals      OperationWorkflowAttentionStats   `json:"totals"`
	Groups      []OperationWorkflowAttentionGroup `json:"groups"`
	NextCursor  string                            `json:"next_cursor,omitempty"`
}
type OperationWorkflowAttentionSummaryOptions struct {
	OperationWorkflowAttentionOptions
	GroupBy string
}
