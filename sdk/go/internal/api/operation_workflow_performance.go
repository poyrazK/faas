package api

import "time"

// OperationWorkflowPerformanceOptions selects a single app/environment/workflow.
type OperationWorkflowPerformanceOptions struct {
	AppID, Scope, Workflow, TenantID string
}

// OperationWorkflowPerformanceSummary compares complete retained histories by cohort.
type OperationWorkflowPerformanceSummary struct {
	CohortToken string                             `json:"cohort_token"`
	EvaluatedAt time.Time                          `json:"evaluated_at"`
	Workflow    string                             `json:"workflow"`
	CohortLimit int                                `json:"cohort_limit"`
	Completed   OperationWorkflowPerformanceCohort `json:"completed"`
	Ongoing     OperationWorkflowPerformanceCohort `json:"ongoing"`
}

type OperationWorkflowDurationDistribution struct {
	WorkflowCount int64 `json:"workflow_count"`
	TotalSeconds  int64 `json:"total_seconds"`
	P50Seconds    int64 `json:"p50_seconds"`
	P95Seconds    int64 `json:"p95_seconds"`
}

type OperationWorkflowPerformanceCoverageReason struct {
	Reason        string `json:"reason"`
	WorkflowCount int64  `json:"workflow_count"`
}

type OperationWorkflowPerformanceCohort struct {
	SLAConfiguredWorkflowCount      int64                                        `json:"sla_configured_workflow_count"`
	SLAEvaluatedWorkflowCount       int64                                        `json:"sla_evaluated_workflow_count"`
	SLABreachedWorkflowCount        int64                                        `json:"sla_breached_workflow_count"`
	SLAUnknownWorkflowCount         int64                                        `json:"sla_unknown_workflow_count"`
	MatchingWorkflowCount           int64                                        `json:"matching_workflow_count"`
	SampledWorkflowCount            int64                                        `json:"sampled_workflow_count"`
	CompleteHistoryWorkflowCount    int64                                        `json:"complete_history_workflow_count"`
	ExcludedIncompleteWorkflowCount int64                                        `json:"excluded_incomplete_workflow_count"`
	CohortTruncated                 bool                                         `json:"cohort_truncated"`
	Exclusions                      []OperationWorkflowPerformanceCoverageReason `json:"exclusions"`
	StateTime                       OperationWorkflowDurationDistribution        `json:"state_time"`
	BlockedTime                     OperationWorkflowDurationDistribution        `json:"blocked_time"`
	VerificationWait                OperationWorkflowDurationDistribution        `json:"verification_wait"`
	States                          []OperationWorkflowStatePerformance          `json:"states"`
	Blockers                        []OperationWorkflowBlockerPerformance        `json:"blockers"`
	VerificationOwners              []OperationWorkflowVerificationPerformance   `json:"verification_owners"`
	StatesTruncated                 bool                                         `json:"states_truncated"`
	BlockersTruncated               bool                                         `json:"blockers_truncated"`
	VerificationOwnersTruncated     bool                                         `json:"verification_owners_truncated"`
}

type OperationWorkflowStatePerformance struct {
	SLAEvaluatedVisitCount int64                                 `json:"sla_evaluated_visit_count"`
	SLABreachedVisitCount  int64                                 `json:"sla_breached_visit_count"`
	ContractVersion        int                                   `json:"contract_version"`
	State                  string                                `json:"state"`
	Duration               OperationWorkflowDurationDistribution `json:"duration"`
}

type OperationWorkflowBlockerPerformance struct {
	ContractVersion int                                   `json:"contract_version"`
	Operation       string                                `json:"operation"`
	Code            string                                `json:"code"`
	Owner           string                                `json:"owner"`
	Duration        OperationWorkflowDurationDistribution `json:"duration"`
}

type OperationWorkflowVerificationPerformance struct {
	Owner                  string                                `json:"owner"`
	PendingResolutionCount int64                                 `json:"pending_resolution_count"`
	Duration               OperationWorkflowDurationDistribution `json:"duration"`
}
