package api

import "time"

// OperationWorkflowMissingConditionProgress reconstructs missing-report gaps in today's retained sample.
type OperationWorkflowMissingConditionProgress struct {
	BaselineAt                             time.Time                                           `json:"baseline_at"`
	EvaluatedAt                            time.Time                                           `json:"evaluated_at"`
	SampledWorkflowCount                   int64                                               `json:"sampled_workflow_count"`
	ComparedWorkflowCount                  int64                                               `json:"compared_workflow_count"`
	NoBaselineWorkflowCount                int64                                               `json:"no_baseline_workflow_count"`
	NoMissingBaselineWorkflowCount         int64                                               `json:"no_missing_baseline_workflow_count"`
	ExcludedIncompleteWorkflowCount        int64                                               `json:"excluded_incomplete_workflow_count"`
	ExcludedAmbiguousWorkflowCount         int64                                               `json:"excluded_ambiguous_workflow_count"`
	ExcludedConditionEvidenceWorkflowCount int64                                               `json:"excluded_condition_evidence_workflow_count"`
	ExcludedDefinitionWorkflowCount        int64                                               `json:"excluded_definition_workflow_count"`
	Statuses                               []OperationWorkflowMissingConditionProgressCount    `json:"statuses"`
	Items                                  []OperationWorkflowMissingConditionProgressInstance `json:"items"`
}
type OperationWorkflowMissingConditionProgressCount struct {
	Status         string `json:"status"`
	ConditionCount int64  `json:"condition_count"`
}
type OperationWorkflowMissingConditionProgressInstance struct {
	PlatformTenantID              string                                            `json:"platform_tenant_id,omitempty"`
	Subject                       OperationSubject                                  `json:"subject"`
	BaselineReport                OperationWorkflowStateHistoryEntry                `json:"baseline_report"`
	State                         OperationWorkflowState                            `json:"state"`
	StateChanged                  bool                                              `json:"state_changed"`
	BaselineMissingConditionCount int64                                             `json:"baseline_missing_condition_count"`
	Statuses                      []OperationWorkflowMissingConditionProgressCount  `json:"statuses"`
	ChangesTruncated              bool                                              `json:"changes_truncated"`
	Changes                       []OperationWorkflowMissingConditionProgressChange `json:"changes"`
}
type OperationWorkflowMissingConditionProgressChange struct {
	Status      string                                   `json:"status"`
	Before      OperationWorkflowConditionCoverageEntry  `json:"before"`
	After       *OperationWorkflowConditionCoverageEntry `json:"after,omitempty"`
	Explanation string                                   `json:"explanation"`
}
