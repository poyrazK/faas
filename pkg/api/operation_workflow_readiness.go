package api

import "time"

// Readiness checks declared requirements against retained reports, not execution authority.
type OperationWorkflowReadinessRequest struct {
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
	Transition                 OperationWorkflowInstanceTransition  `json:"transition"`
	Declared                   bool                                 `json:"declared"`
	Ready                      bool                                 `json:"ready"`
	Reasons                    []string                             `json:"reasons"`
	Advisories                 []string                             `json:"advisories"`
	StateRevision              int64                                `json:"state_revision,omitempty"`
	ContractVersion            int                                  `json:"contract_version"`
	InvariantBlockers          []OperationWorkflowBlocker           `json:"invariant_blockers,omitempty"`
	Blockers                   []OperationWorkflowBlocker           `json:"blockers"`
	UnmetDependencies          []OperationWorkflowRelatedInstance   `json:"unmet_dependencies"`
	MissingDependencyWorkflows []string                             `json:"missing_dependency_workflows,omitempty"`
	UnmetEffects               []OperationWorkflowUnmetEffect       `json:"unmet_effects,omitempty"`
	UnmetInvariants            []OperationWorkflowUnmetInvariant    `json:"unmet_invariants,omitempty"`
	MissingPolicies            []OperationWorkflowPolicyRequirement `json:"missing_policies,omitempty"`
	MissingMilestones          []string                             `json:"missing_milestones"`
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
