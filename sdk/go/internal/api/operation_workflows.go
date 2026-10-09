package api

const OperationWorkflowStateStaleAfterMaxSeconds int64 = 10 * 365 * 24 * 60 * 60

type OperationWorkflowSpec struct {
	AllowReconciliation    bool                          `json:"allow_reconciliation,omitempty"`
	Workflow               string                        `json:"workflow"`
	Title                  string                        `json:"title"`
	Version                int                           `json:"version,omitempty"`
	States                 []string                      `json:"states,omitempty"`
	TerminalStates         []string                      `json:"terminal_states,omitempty"`
	StateStaleAfterSeconds map[string]int64              `json:"state_stale_after_seconds,omitempty"`
	Transitions            []OperationWorkflowTransition `json:"transitions,omitempty"`
	TransitionsDeclared    bool                          `json:"transitions_declared,omitempty"`
	Step                   string                        `json:"step"`
	Label                  string                        `json:"label"`
	Milestone              string                        `json:"milestone"`
	InstanceIDFrom         string                        `json:"instance_id_from,omitempty"`
	InstanceID             string                        `json:"instance_id,omitempty"`
	Position               int                           `json:"position"`
}

type OperationWorkflowTransition struct {
	From                        string                                  `json:"from"`
	To                          string                                  `json:"to"`
	RequiredDependencyWorkflows *[]string                               `json:"required_dependency_workflows,omitempty"`
	RequiredEffects             []OperationWorkflowEffectRequirement    `json:"required_effects,omitempty"`
	RequiredInvariants          []OperationWorkflowInvariantRequirement `json:"required_invariants,omitempty"`
	RequiredPolicies            []OperationWorkflowPolicyRequirement    `json:"required_policies,omitempty"`
	RequiredMilestones          []string                                `json:"required_milestones,omitempty"`
}
