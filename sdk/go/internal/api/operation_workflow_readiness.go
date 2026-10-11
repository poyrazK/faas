package api

import "time"

// Readiness checks declared requirements against retained reports, not execution authority.
type OperationWorkflowReadinessRequest struct {
	SelectedPaths   []OperationWorkflowPathSelection    `json:"selected_paths,omitempty"`
	AppID           string                              `json:"app_id,omitempty"`
	TenantID        string                              `json:"tenant_id,omitempty"`
	Scope           string                              `json:"scope"`
	Subject         OperationSubject                    `json:"subject"`
	Workflow        string                              `json:"workflow"`
	InstanceID      string                              `json:"instance_id"`
	Operation       string                              `json:"operation"`
	FromState       string                              `json:"from_state"`
	ToState         string                              `json:"to_state"`
	Effects         []OperationWorkflowPlannedEffect    `json:"effects,omitempty"`
	Invariants      []OperationWorkflowPlannedInvariant `json:"invariants,omitempty"`
	Decisions       []OperationWorkflowPlannedDecision  `json:"decisions,omitempty"`
	Milestones      []string                            `json:"milestones,omitempty"`
	StateRevision   int64                               `json:"state_revision,omitempty"`
	ContractVersion int                                 `json:"contract_version,omitempty"`
}

type OperationWorkflowTransitionReadiness struct {
	Reporting                     *OperationWorkflowTransitionReporting           `json:"reporting,omitempty"`
	PendingVerifications          []OperationWorkflowResolutionVerification       `json:"pending_verifications,omitempty"`
	PendingVerificationCount      int64                                           `json:"pending_verification_count"`
	PendingVerificationsTruncated bool                                            `json:"pending_verifications_truncated"`
	RetainedMilestoneGroups       []OperationWorkflowRetainedMilestoneGroupResult `json:"retained_milestone_groups,omitempty"`
	RetainedMilestones            []OperationWorkflowRetainedMilestone            `json:"retained_milestones,omitempty"`
	Transition                    OperationWorkflowInstanceTransition             `json:"transition"`
	Declared                      bool                                            `json:"declared"`
	Ready                         bool                                            `json:"ready"`
	Reasons                       []string                                        `json:"reasons"`
	Advisories                    []string                                        `json:"advisories"`
	StateRevision                 int64                                           `json:"state_revision,omitempty"`
	ContractVersion               int                                             `json:"contract_version"`
	InvariantBlockers             []OperationWorkflowBlocker                      `json:"invariant_blockers,omitempty"`
	Blockers                      []OperationWorkflowBlocker                      `json:"blockers"`
	UnmetDependencies             []OperationWorkflowRelatedInstance              `json:"unmet_dependencies"`
	MissingDependencyWorkflows    []string                                        `json:"missing_dependency_workflows,omitempty"`
	UnmetEffects                  []OperationWorkflowUnmetEffect                  `json:"unmet_effects,omitempty"`
	UnmetInvariants               []OperationWorkflowUnmetInvariant               `json:"unmet_invariants,omitempty"`
	MissingPolicies               []OperationWorkflowPolicyRequirement            `json:"missing_policies,omitempty"`
	MissingMilestones             []string                                        `json:"missing_milestones"`
}

type OperationWorkflowReadinessResponse struct {
	Subject     OperationSubject                     `json:"subject"`
	Workflow    string                               `json:"workflow"`
	InstanceID  string                               `json:"instance_id"`
	EvaluatedAt time.Time                            `json:"evaluated_at"`
	Readiness   OperationWorkflowTransitionReadiness `json:"readiness"`
}

// Overview evaluates at most 100 declared current-state edges with no planned milestones.
type OperationWorkflowReadinessOverview struct {
	Items           []OperationWorkflowTransitionReadiness `json:"items"`
	TransitionCount int                                    `json:"transition_count"`
	HasMore         bool                                   `json:"has_more"`
}

// OperationWorkflowTransitionReporting describes current-state reporting, not transition guards.
type OperationWorkflowTransitionReporting struct {
	EvaluatedAt            time.Time                                 `json:"evaluated_at"`
	State                  string                                    `json:"state"`
	ReportingOperationID   string                                    `json:"reporting_operation_id"`
	Status                 string                                    `json:"status"`
	ExpectedConditionCount int64                                     `json:"expected_condition_count"`
	ReportedConditionCount int64                                     `json:"reported_condition_count"`
	MissingConditionCount  int64                                     `json:"missing_condition_count"`
	OverdueConditionCount  int64                                     `json:"overdue_condition_count"`
	Conditions             []OperationWorkflowConditionCoverageEntry `json:"conditions"`
	ConditionsTruncated    bool                                      `json:"conditions_truncated"`
}
