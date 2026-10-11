package api

import "time"

// OperationWorkflowNewMissingConditionProgress explains current missing reports relative to a saved baseline.
type OperationWorkflowNewMissingConditionProgress struct {
	BaselineAt                             time.Time                                              `json:"baseline_at"`
	EvaluatedAt                            time.Time                                              `json:"evaluated_at"`
	SampledWorkflowCount                   int64                                                  `json:"sampled_workflow_count"`
	ComparedWorkflowCount                  int64                                                  `json:"compared_workflow_count"`
	NoBaselineWorkflowCount                int64                                                  `json:"no_baseline_workflow_count"`
	NoCurrentMissingWorkflowCount          int64                                                  `json:"no_current_missing_workflow_count"`
	ExcludedIncompleteWorkflowCount        int64                                                  `json:"excluded_incomplete_workflow_count"`
	ExcludedAmbiguousWorkflowCount         int64                                                  `json:"excluded_ambiguous_workflow_count"`
	ExcludedConditionEvidenceWorkflowCount int64                                                  `json:"excluded_condition_evidence_workflow_count"`
	ExcludedDefinitionWorkflowCount        int64                                                  `json:"excluded_definition_workflow_count"`
	Statuses                               []OperationWorkflowNewMissingConditionProgressCount    `json:"statuses"`
	Items                                  []OperationWorkflowNewMissingConditionProgressInstance `json:"items"`
}
type OperationWorkflowNewMissingConditionProgressCount struct {
	Status         string `json:"status"`
	ConditionCount int64  `json:"condition_count"`
}
type OperationWorkflowNewMissingConditionProgressInstance struct {
	PlatformTenantID             string                                               `json:"platform_tenant_id,omitempty"`
	Subject                      OperationSubject                                     `json:"subject"`
	BaselineReport               OperationWorkflowStateHistoryEntry                   `json:"baseline_report"`
	BaselineTerminal             bool                                                 `json:"baseline_terminal"`
	State                        OperationWorkflowState                               `json:"state"`
	StateChanged                 bool                                                 `json:"state_changed"`
	CurrentMissingConditionCount int64                                                `json:"current_missing_condition_count"`
	Statuses                     []OperationWorkflowNewMissingConditionProgressCount  `json:"statuses"`
	ChangesTruncated             bool                                                 `json:"changes_truncated"`
	Changes                      []OperationWorkflowNewMissingConditionProgressChange `json:"changes"`
}
type OperationWorkflowNewMissingConditionProgressChange struct {
	Status      string                                   `json:"status"`
	Before      *OperationWorkflowConditionCoverageEntry `json:"before,omitempty"`
	After       OperationWorkflowConditionCoverageEntry  `json:"after"`
	Explanation string                                   `json:"explanation"`
}
