package api

import "time"

// OperationWorkflowPerformanceOptions selects a single app/environment/workflow.
type OperationWorkflowPerformanceOptions struct {
	AppID, Scope, Workflow, TenantID string
}

// OperationWorkflowPerformanceSummary compares complete retained histories by cohort.
type OperationWorkflowPerformanceSummary struct {
	DecisionOutcomes *OperationWorkflowDecisionOutcomeSummary `json:"decision_outcomes,omitempty"`
	CohortToken      string                                   `json:"cohort_token"`
	EvaluatedAt      time.Time                                `json:"evaluated_at"`
	Workflow         string                                   `json:"workflow"`
	CohortLimit      int                                      `json:"cohort_limit"`
	Completed        OperationWorkflowPerformanceCohort       `json:"completed"`
	Ongoing          OperationWorkflowPerformanceCohort       `json:"ongoing"`
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

// OperationWorkflowDecisionOutcomeSummary groups completed workflows by reported business decisions and paths.
type OperationWorkflowDecisionOutcomeSummary struct {
	RuleComparisons                 *OperationWorkflowRuleComparisonSummary `json:"rule_comparisons,omitempty"`
	MatchingWorkflowCount           int64                                   `json:"matching_workflow_count"`
	SampledWorkflowCount            int64                                   `json:"sampled_workflow_count"`
	LinkedWorkflowCount             int64                                   `json:"linked_workflow_count"`
	ExcludedIncompleteWorkflowCount int64                                   `json:"excluded_incomplete_workflow_count"`
	MissingOutcomeWorkflowCount     int64                                   `json:"missing_outcome_workflow_count"`
	UnclassifiedWorkflowCount       int64                                   `json:"unclassified_workflow_count"`
	CohortTruncated                 bool                                    `json:"cohort_truncated"`
	GroupsTruncated                 bool                                    `json:"groups_truncated"`
	Groups                          []OperationWorkflowDecisionOutcomeGroup `json:"groups"`
}

// OperationWorkflowDecisionOutcomeGroup counts each workflow once per exact group.
type OperationWorkflowDecisionOutcomeGroup struct {
	Kind            string                                `json:"kind"`
	ContractVersion int                                   `json:"contract_version"`
	DecisionCode    string                                `json:"decision_code,omitempty"`
	RuleID          string                                `json:"rule_id,omitempty"`
	RuleVersion     string                                `json:"rule_version,omitempty"`
	GroupID         string                                `json:"group_id,omitempty"`
	PathID          string                                `json:"path_id,omitempty"`
	OutcomeCode     string                                `json:"outcome_code"`
	CompletionTime  OperationWorkflowDurationDistribution `json:"completion_time"`
	BlockedTime     OperationWorkflowDurationDistribution `json:"blocked_time"`
}

// OperationWorkflowRuleComparisonSummary compares observed rule versions within the completed sample.
type OperationWorkflowRuleComparisonSummary struct {
	SampledWorkflowCount      int64                             `json:"sampled_workflow_count"`
	UnattributedWorkflowCount int64                             `json:"unattributed_workflow_count"`
	ComparisonsTruncated      bool                              `json:"comparisons_truncated"`
	Comparisons               []OperationWorkflowRuleComparison `json:"comparisons"`
}
type OperationWorkflowRuleComparison struct {
	SingleVersionStatus               string                                   `json:"single_version_status"`
	MultiVersionWorkflowCount         int64                                    `json:"multi_version_workflow_count"`
	EligibleMultiVersionWorkflowCount int64                                    `json:"eligible_multi_version_workflow_count"`
	ExamplesTruncated                 bool                                     `json:"examples_truncated"`
	MultiVersionExamples              []OperationWorkflowRuleVersionOverlap    `json:"multi_version_examples"`
	ContractVersion                   int                                      `json:"contract_version"`
	DecisionCode                      string                                   `json:"decision_code"`
	RuleID                            string                                   `json:"rule_id"`
	Status                            string                                   `json:"status"`
	VersionsTruncated                 bool                                     `json:"versions_truncated"`
	Versions                          []OperationWorkflowRuleVersionStatistics `json:"versions"`
}
type OperationWorkflowRuleVersionStatistics struct {
	SingleVersion                   *OperationWorkflowRuleVersionCohort   `json:"single_version,omitempty"`
	RuleVersion                     string                                `json:"rule_version"`
	ObservedWorkflowCount           int64                                 `json:"observed_workflow_count"`
	EligibleWorkflowCount           int64                                 `json:"eligible_workflow_count"`
	ExcludedIncompleteWorkflowCount int64                                 `json:"excluded_incomplete_workflow_count"`
	MissingOutcomeWorkflowCount     int64                                 `json:"missing_outcome_workflow_count"`
	CompletionTime                  OperationWorkflowDurationDistribution `json:"completion_time"`
	BlockedTime                     OperationWorkflowDurationDistribution `json:"blocked_time"`
	OutcomesTruncated               bool                                  `json:"outcomes_truncated"`
	Outcomes                        []OperationWorkflowRuleVersionOutcome `json:"outcomes"`
}
type OperationWorkflowRuleVersionOutcome struct {
	OutcomeCode           string `json:"outcome_code"`
	WorkflowCount         int64  `json:"workflow_count"`
	ProportionBasisPoints int64  `json:"proportion_basis_points"`
}

// OperationWorkflowRuleVersionCohort restricts outcomes to complete workflows reporting exactly one version for the comparison identity.
type OperationWorkflowRuleVersionCohort struct {
	EligibleWorkflowCount int64                                 `json:"eligible_workflow_count"`
	CompletionTime        OperationWorkflowDurationDistribution `json:"completion_time"`
	BlockedTime           OperationWorkflowDurationDistribution `json:"blocked_time"`
	OutcomesTruncated     bool                                  `json:"outcomes_truncated"`
	Outcomes              []OperationWorkflowRuleVersionOutcome `json:"outcomes"`
}

// OperationWorkflowRuleVersionOverlap is a bounded example of observed version overlap, not proof of a policy replacement.
type OperationWorkflowRuleVersionOverlap struct {
	PlatformTenantID  string                                     `json:"platform_tenant_id,omitempty"`
	Subject           OperationSubject                           `json:"subject"`
	InstanceID        string                                     `json:"instance_id"`
	OperationID       string                                     `json:"operation_id"`
	HistoryComplete   bool                                       `json:"history_complete"`
	Eligible          bool                                       `json:"eligible"`
	EvidenceTruncated bool                                       `json:"evidence_truncated"`
	Evidence          []OperationWorkflowDecisionOutcomeEvidence `json:"evidence"`
}
