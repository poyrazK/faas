package api

import "time"

// OperationWorkflowPerformanceGroup identifies one exact performance dimension.
type OperationWorkflowPerformanceGroup struct {
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
