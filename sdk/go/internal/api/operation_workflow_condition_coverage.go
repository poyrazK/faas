package api

// OperationWorkflowConditionCoverage summarizes reporting, never business truth.
type OperationWorkflowConditionCoverage struct {
	MissingGroups                          []OperationWorkflowMissingConditionGroup     `json:"missing_groups"`
	MissingGroupsTruncated                 bool                                         `json:"missing_groups_truncated"`
	MatchingWorkflowCount                  int64                                        `json:"matching_workflow_count"`
	SampledWorkflowCount                   int64                                        `json:"sampled_workflow_count"`
	CohortTruncated                        bool                                         `json:"cohort_truncated"`
	ComparedWorkflowCount                  int64                                        `json:"compared_workflow_count"`
	NoExpectationsWorkflowCount            int64                                        `json:"no_expectations_workflow_count"`
	ExcludedIncompleteWorkflowCount        int64                                        `json:"excluded_incomplete_workflow_count"`
	ExcludedAmbiguousWorkflowCount         int64                                        `json:"excluded_ambiguous_workflow_count"`
	ExcludedConditionEvidenceWorkflowCount int64                                        `json:"excluded_condition_evidence_workflow_count"`
	ExpectedConditionCount                 int64                                        `json:"expected_condition_count"`
	ReportedConditionCount                 int64                                        `json:"reported_condition_count"`
	MissingConditionCount                  int64                                        `json:"missing_condition_count"`
	OverdueConditionCount                  int64                                        `json:"overdue_condition_count"`
	Items                                  []OperationWorkflowConditionCoverageInstance `json:"items"`
}

type OperationWorkflowConditionCoverageInstance struct {
	PlatformTenantID       string                                    `json:"platform_tenant_id,omitempty"`
	Subject                OperationSubject                          `json:"subject"`
	State                  OperationWorkflowState                    `json:"state"`
	ExpectedConditionCount int64                                     `json:"expected_condition_count"`
	ReportedConditionCount int64                                     `json:"reported_condition_count"`
	MissingConditionCount  int64                                     `json:"missing_condition_count"`
	OverdueConditionCount  int64                                     `json:"overdue_condition_count"`
	ConditionsTruncated    bool                                      `json:"conditions_truncated"`
	Conditions             []OperationWorkflowConditionCoverageEntry `json:"conditions"`
}

type OperationWorkflowConditionCoverageEntry struct {
	Step       string                                    `json:"step"`
	Milestone  string                                    `json:"milestone"`
	Definition OperationBusinessConditionDefinition      `json:"definition"`
	Status     string                                    `json:"status"`
	Evidence   *OperationWorkflowDecisionOutcomeEvidence `json:"evidence,omitempty"`
}

// OperationWorkflowMissingConditionGroup counts missing evidence in one current state and owner.
type OperationWorkflowMissingConditionGroup struct {
	ContractVersion       int                                   `json:"contract_version"`
	State                 string                                `json:"state"`
	Owner                 string                                `json:"owner,omitempty"`
	Unassigned            bool                                  `json:"unassigned"`
	MissingConditionCount int64                                 `json:"missing_condition_count"`
	BlockedTime           OperationWorkflowDurationDistribution `json:"blocked_time"`
}
