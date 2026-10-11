package api

// OperationWorkflowStateStaleAfterMaxSeconds bounds app-declared state age
// thresholds while allowing long-running business workflows.
const OperationWorkflowStateStaleAfterMaxSeconds int64 = 10 * 365 * 24 * 60 * 60

// OperationWorkflowSpec is the deployment-resolved projection attached to an
// Operation definition. It is returned with a fact only when its milestone
// name matches. Source manifests pin the milestone and instance ID pointer;
// reads populate InstanceID from the retained public payload.
type OperationWorkflowSpec struct {
	StateSLAWarningPercent map[string]int64                                    `json:"state_sla_warning_percent,omitempty"`
	StateSLABudgetSeconds  map[string]int64                                    `json:"state_sla_budget_seconds,omitempty"`
	BlockerEscalations     map[string]OperationWorkflowBlockerEscalationPolicy `json:"blocker_escalations,omitempty"`
	AllowReconciliation    bool                                                `json:"allow_reconciliation,omitempty"`
	Workflow               string                                              `json:"workflow"`
	Title                  string                                              `json:"title"`
	Version                int                                                 `json:"version,omitempty"`
	States                 []string                                            `json:"states,omitempty"`
	TerminalStates         []string                                            `json:"terminal_states,omitempty"`
	StateStaleAfterSeconds map[string]int64                                    `json:"state_stale_after_seconds,omitempty"`
	Transitions            []OperationWorkflowTransition                       `json:"transitions,omitempty"`
	TransitionsDeclared    bool                                                `json:"transitions_declared,omitempty"`
	Step                   string                                              `json:"step"`
	Label                  string                                              `json:"label"`
	Milestone              string                                              `json:"milestone"`
	InstanceIDFrom         string                                              `json:"instance_id_from,omitempty"`
	InstanceID             string                                              `json:"instance_id,omitempty"`
	Position               int                                                 `json:"position"`
}

// OperationWorkflowTransition is an app-declared allowed state edge. The
// application still checks the current business row while holding its lock.
type OperationWorkflowTransition struct {
	From                        string                                  `json:"from"`
	To                          string                                  `json:"to"`
	RequiredDependencyWorkflows *[]string                               `json:"required_dependency_workflows,omitempty"`
	RequiredEffects             []OperationWorkflowEffectRequirement    `json:"required_effects,omitempty"`
	RequiredInvariants          []OperationWorkflowInvariantRequirement `json:"required_invariants,omitempty"`
	RequiredPolicies            []OperationWorkflowPolicyRequirement    `json:"required_policies,omitempty"`
	RequiredMilestones          []string                                `json:"required_milestones,omitempty"`
}

// OperationWorkflowBlockerEscalationPolicy recommends escalation after a known blocker age.
type OperationWorkflowBlockerEscalationPolicy struct {
	AfterSeconds int64  `json:"after_seconds" yaml:"after_seconds" toml:"after_seconds"`
	Owner        string `json:"owner" yaml:"owner" toml:"owner"`
}
