package api

import "time"

// Outcomes cover latest retained terminal snapshots with explicit application outcomes.
type OperationWorkflowOutcomeEntry struct {
	AppID            string                 `json:"app_id"`
	Scope            string                 `json:"scope"`
	PlatformTenantID string                 `json:"platform_tenant_id,omitempty"`
	Subject          OperationSubject       `json:"subject"`
	OperationID      string                 `json:"operation_id"`
	State            OperationWorkflowState `json:"state"`
}
type OperationWorkflowOutcomesResponse struct {
	Items       []OperationWorkflowOutcomeEntry `json:"items"`
	EvaluatedAt time.Time                       `json:"evaluated_at"`
	NextCursor  string                          `json:"next_cursor,omitempty"`
}
type OperationWorkflowOutcomeOptions struct {
	AppID, Scope, TenantID, Workflow, Code, Cursor string
	Limit                                          int
}
type OperationWorkflowOutcomeSummaryOptions struct {
	OperationWorkflowOutcomeOptions
	GroupBy string
}
type OperationWorkflowOutcomeGroup struct {
	Value         string `json:"value"`
	WorkflowCount int64  `json:"workflow_count"`
}
type OperationWorkflowOutcomeSummary struct {
	GroupBy       string                          `json:"group_by"`
	EvaluatedAt   time.Time                       `json:"evaluated_at"`
	WorkflowCount int64                           `json:"workflow_count"`
	Groups        []OperationWorkflowOutcomeGroup `json:"groups"`
	NextCursor    string                          `json:"next_cursor,omitempty"`
}
