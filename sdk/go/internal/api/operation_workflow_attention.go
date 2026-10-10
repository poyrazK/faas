package api

import "time"

// OperationWorkflowAttentionEntry is a retained workflow snapshot needing investigation.
type OperationWorkflowAttentionEntry struct {
	ResolutionVerifications     []OperationWorkflowResolutionVerification `json:"resolution_verifications,omitempty"`
	AwaitingVerificationCount   int64                                     `json:"awaiting_verification_count"`
	ResolutionVerificationCount int64                                     `json:"resolution_verification_count"`
	Escalations                 []OperationWorkflowBlockerEscalation      `json:"escalations,omitempty"`
	DependencyAttention         []OperationWorkflowRelatedInstance        `json:"dependency_attention,omitempty"`
	AppID                       string                                    `json:"app_id"`
	Scope                       string                                    `json:"scope"`
	PlatformTenantID            string                                    `json:"platform_tenant_id,omitempty"`
	Subject                     OperationSubject                          `json:"subject"`
	OperationID                 string                                    `json:"operation_id"`
	State                       OperationWorkflowState                    `json:"state"`
	Reasons                     []string                                  `json:"reasons"`
}

type OperationWorkflowAttentionResponse struct {
	Items       []OperationWorkflowAttentionEntry `json:"items"`
	EvaluatedAt time.Time                         `json:"evaluated_at"`
	NextCursor  string                            `json:"next_cursor,omitempty"`
}

type OperationWorkflowAttentionOptions struct {
	Priority, Sort                                                                 string
	Owner                                                                          string
	Unassigned                                                                     bool
	DependencyStatus, RequiredOutcomeCode                                          string
	AppID, Scope, TenantID, Workflow, TargetOperation, Reason, Cursor, BlockerCode string
	Limit                                                                          int
}

// Summary totals cover every matching retained workflow, independent of group pagination.
type OperationWorkflowAttentionStats struct {
	SLAAtRiskWorkflowCount              int64      `json:"sla_at_risk_workflow_count"`
	SLABreachedWorkflowCount            int64      `json:"sla_breached_workflow_count"`
	SLAUnknownWorkflowCount             int64      `json:"sla_unknown_workflow_count"`
	AwaitingVerificationWorkflowCount   int64      `json:"awaiting_verification_workflow_count"`
	AwaitingVerificationResolutionCount int64      `json:"awaiting_verification_resolution_count"`
	LowBlockerCount                     int64      `json:"low_blocker_count"`
	NormalBlockerCount                  int64      `json:"normal_blocker_count"`
	HighBlockerCount                    int64      `json:"high_blocker_count"`
	UrgentBlockerCount                  int64      `json:"urgent_blocker_count"`
	UnacknowledgedBlockerCount          int64      `json:"unacknowledged_blocker_count"`
	FollowUpOverdueBlockerCount         int64      `json:"follow_up_overdue_blocker_count"`
	EscalatedWorkflowCount              int64      `json:"escalated_workflow_count"`
	EscalatedBlockerCount               int64      `json:"escalated_blocker_count"`
	DependencyWorkflowCount             int64      `json:"dependency_workflow_count"`
	DependencyCount                     int64      `json:"dependency_count"`
	OverdueWorkflowCount                int64      `json:"overdue_workflow_count"`
	EarliestOverdueDeadlineAt           *time.Time `json:"earliest_overdue_deadline_at,omitempty"`
	LongestOverdueSeconds               *int64     `json:"longest_overdue_seconds,omitempty"`
	WorkflowCount                       int64      `json:"workflow_count"`
	BlockedWorkflowCount                int64      `json:"blocked_workflow_count"`
	StaleWorkflowCount                  int64      `json:"stale_workflow_count"`
	BlockerCount                        int64      `json:"blocker_count"`
	UnknownAgeBlockers                  int64      `json:"unknown_age_blockers"`
	OldestBlockerAt                     *time.Time `json:"oldest_blocker_at,omitempty"`
	OldestBlockerAgeSeconds             *int64     `json:"oldest_blocker_age_seconds,omitempty"`
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

type OperationWorkflowBlockerEscalation struct {
	Code         string    `json:"code"`
	Operation    string    `json:"operation"`
	Owner        string    `json:"owner"`
	AfterSeconds int64     `json:"after_seconds"`
	EscalatedAt  time.Time `json:"escalated_at"`
}

// OperationWorkflowResolutionVerification projects retained evidence for an explicit resolution obligation.
type OperationWorkflowResolutionVerification struct {
	Resolution            OperationWorkflowBlockerResolution `json:"resolution"`
	ResolutionOperationID string                             `json:"resolution_operation_id"`
	ResolutionReportID    string                             `json:"resolution_report_id"`
	ResolutionRevision    int64                              `json:"resolution_revision"`
	Status                string                             `json:"status"`
	VerifiedAt            *time.Time                         `json:"verified_at,omitempty"`
}
