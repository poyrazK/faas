package api

import "time"

// OperationWorkflowPerformanceGroup identifies one exact performance dimension.
type OperationWorkflowPerformanceGroup struct {
	Kind            string `json:"kind,omitempty"`
	DecisionCode    string `json:"decision_code,omitempty"`
	RuleID          string `json:"rule_id,omitempty"`
	RuleVersion     string `json:"rule_version,omitempty"`
	GroupID         string `json:"group_id,omitempty"`
	PathID          string `json:"path_id,omitempty"`
	OutcomeCode     string `json:"outcome_code,omitempty"`
	Dimension       string `json:"dimension"`
	ContractVersion int    `json:"contract_version,omitempty"`
	State           string `json:"state,omitempty"`
	Operation       string `json:"operation,omitempty"`
	Code            string `json:"code,omitempty"`
	Owner           string `json:"owner,omitempty"`
	Unassigned      bool   `json:"unassigned,omitempty"`
}

type OperationWorkflowPerformanceInstanceOptions struct {
	OperationWorkflowPerformanceOptions
	OperationWorkflowPerformanceGroup
	Cohort, CohortToken string
}

// OperationWorkflowPerformanceInstance is a complete-history contributor.
type OperationWorkflowPerformanceInstance struct {
	DecisionEvidence            *OperationWorkflowDecisionOutcomeEvidence `json:"decision_evidence,omitempty"`
	PlatformTenantID            string                                    `json:"platform_tenant_id,omitempty"`
	Subject                     OperationSubject                          `json:"subject"`
	OperationID                 string                                    `json:"operation_id"`
	State                       OperationWorkflowState                    `json:"state"`
	ObservedSeconds             int64                                     `json:"observed_seconds"`
	ResolutionVerifications     []OperationWorkflowResolutionVerification `json:"resolution_verifications"`
	AwaitingVerificationCount   int64                                     `json:"awaiting_verification_count"`
	ResolutionVerificationCount int64                                     `json:"resolution_verification_count"`
}

// OperationWorkflowPerformanceInstancesResponse preserves cohort coverage alongside ranked contributors.
type OperationWorkflowPerformanceInstancesResponse struct {
	DecisionOutcomes                *OperationWorkflowDecisionOutcomeSummary     `json:"decision_outcomes,omitempty"`
	EvaluatedAt                     time.Time                                    `json:"evaluated_at"`
	CohortToken                     string                                       `json:"cohort_token"`
	Workflow                        string                                       `json:"workflow"`
	Cohort                          string                                       `json:"cohort"`
	Group                           OperationWorkflowPerformanceGroup            `json:"group"`
	MatchingWorkflowCount           int64                                        `json:"matching_workflow_count"`
	SampledWorkflowCount            int64                                        `json:"sampled_workflow_count"`
	CompleteHistoryWorkflowCount    int64                                        `json:"complete_history_workflow_count"`
	ExcludedIncompleteWorkflowCount int64                                        `json:"excluded_incomplete_workflow_count"`
	CohortTruncated                 bool                                         `json:"cohort_truncated"`
	Exclusions                      []OperationWorkflowPerformanceCoverageReason `json:"exclusions"`
	Duration                        OperationWorkflowDurationDistribution        `json:"duration"`
	Items                           []OperationWorkflowPerformanceInstance       `json:"items"`
}

// OperationWorkflowDecisionOutcomeEvidence identifies the earliest qualifying report and optional decision fact.
type OperationWorkflowDecisionOutcomeEvidence struct {
	ReportID    string                     `json:"report_id"`
	OperationID string                     `json:"operation_id"`
	Revision    int64                      `json:"revision"`
	FromState   string                     `json:"from_state,omitempty"`
	State       string                     `json:"state"`
	OccurredAt  time.Time                  `json:"occurred_at"`
	MilestoneID string                     `json:"milestone_id,omitempty"`
	Decision    *OperationBusinessDecision `json:"decision,omitempty"`
}
