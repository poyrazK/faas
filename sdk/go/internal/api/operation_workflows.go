package api

import "time"

// OperationWorkflowSpec is the deployment-resolved projection attached to an
// Operation definition. It is returned with a fact only when its milestone
// name matches. Source manifests pin the milestone and instance ID pointer;
// reads populate InstanceID from the retained public payload.
type OperationWorkflowSpec struct {
	Playbooks              []OperationWorkflowPlaybook                         `json:"playbooks,omitempty"`
	DecisionConditions     []OperationBusinessConditionDefinition              `json:"decision_conditions,omitempty"`
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
	RetainedMilestoneGroups        []OperationWorkflowRetainedMilestoneGroup            `json:"retained_milestone_groups,omitempty"`
	RetainedMilestoneFreshness     []OperationWorkflowRetainedMilestoneFreshnessRule    `json:"retained_milestone_freshness,omitempty"`
	RetainedMilestoneInvalidations []OperationWorkflowRetainedMilestoneInvalidationRule `json:"retained_milestone_invalidations,omitempty"`
	RequiredRetainedMilestones     []OperationWorkflowRetainedMilestoneRequirement      `json:"required_retained_milestones,omitempty"`
	From                           string                                               `json:"from"`
	To                             string                                               `json:"to"`
	RequiredDependencyWorkflows    *[]string                                            `json:"required_dependency_workflows,omitempty"`
	RequiredEffects                []OperationWorkflowEffectRequirement                 `json:"required_effects,omitempty"`
	RequiredInvariants             []OperationWorkflowInvariantRequirement              `json:"required_invariants,omitempty"`
	RequiredPolicies               []OperationWorkflowPolicyRequirement                 `json:"required_policies,omitempty"`
	RequiredMilestones             []string                                             `json:"required_milestones,omitempty"`
}

// OperationWorkflowBlockerEscalationPolicy recommends escalation after a known blocker age.
type OperationWorkflowBlockerEscalationPolicy struct {
	AfterSeconds int64  `json:"after_seconds" yaml:"after_seconds" toml:"after_seconds"`
	Owner        string `json:"owner" yaml:"owner" toml:"owner"`
}

type OperationWorkflowPlaybook struct {
	ID               string   `json:"id" yaml:"id" toml:"id"`
	Version          int      `json:"version" yaml:"version" toml:"version"`
	MatchKind        string   `json:"match_kind" yaml:"match_kind" toml:"match_kind"`
	MatchCode        string   `json:"match_code" yaml:"match_code" toml:"match_code"`
	Summary          string   `json:"summary" yaml:"summary" toml:"summary"`
	Owner            string   `json:"owner,omitempty" yaml:"owner,omitempty" toml:"owner"`
	RequiredEvidence []string `json:"required_evidence,omitempty" yaml:"required_evidence,omitempty" toml:"required_evidence"`
	RunbookURL       string   `json:"runbook_url,omitempty" yaml:"runbook_url,omitempty" toml:"runbook_url"`
	ActionURL        string   `json:"action_url,omitempty" yaml:"action_url,omitempty" toml:"action_url"`
}
type OperationWorkflowPlaybookGuidance struct {
	Playbook             OperationWorkflowPlaybook `json:"playbook" yaml:"playbook" toml:"playbook"`
	ContractVersion      int                       `json:"contract_version" yaml:"contract_version" toml:"contract_version"`
	ReportingOperationID string                    `json:"reporting_operation_id" yaml:"reporting_operation_id" toml:"reporting_operation_id"`
}

// OperationWorkflowPlaybookProgressStartRequest pins currently matched application guidance.
type OperationWorkflowPlaybookProgressStartRequest struct {
	PlaybookID           string `json:"playbook_id"`
	PlaybookVersion      int    `json:"playbook_version"`
	ContractVersion      int    `json:"contract_version"`
	ReportingOperationID string `json:"reporting_operation_id"`
}

// OperationWorkflowPlaybookEvidence references scoped retained application evidence.
type OperationWorkflowPlaybookEvidence struct {
	OperationID string `json:"operation_id"`
	MilestoneID string `json:"milestone_id,omitempty"`
}

// OperationWorkflowPlaybookProgressUpdateRequest replaces one checklist item under a revision fence.
type OperationWorkflowPlaybookProgressUpdateRequest struct {
	Revision  int64                               `json:"revision"`
	ItemIndex int                                 `json:"item_index"`
	Status    string                              `json:"status"`
	Note      string                              `json:"note"`
	Evidence  []OperationWorkflowPlaybookEvidence `json:"evidence"`
}
type OperationWorkflowPlaybookProgressItem struct {
	Label      string                              `json:"label"`
	Status     string                              `json:"status"`
	Note       string                              `json:"note"`
	Evidence   []OperationWorkflowPlaybookEvidence `json:"evidence"`
	ReviewedBy string                              `json:"reviewed_by,omitempty"`
	ReviewedAt *time.Time                          `json:"reviewed_at,omitempty"`
}
type OperationWorkflowPlaybookProgressChange struct {
	Revision  int64                                 `json:"revision"`
	ItemIndex int                                   `json:"item_index"`
	Item      OperationWorkflowPlaybookProgressItem `json:"item"`
	At        time.Time                             `json:"at"`
	Actor     string                                `json:"actor"`
}
type OperationWorkflowPlaybookProgress struct {
	ID                   string                                    `json:"id"`
	Revision             int64                                     `json:"revision"`
	Playbook             OperationWorkflowPlaybook                 `json:"playbook"`
	ContractVersion      int                                       `json:"contract_version"`
	DefinitionID         string                                    `json:"definition_id"`
	ReportingOperationID string                                    `json:"reporting_operation_id"`
	Items                []OperationWorkflowPlaybookProgressItem   `json:"items"`
	CompletedCount       int                                       `json:"completed_count"`
	ChecklistComplete    bool                                      `json:"checklist_complete"`
	CreatedAt            time.Time                                 `json:"created_at"`
	CreatedBy            string                                    `json:"created_by"`
	UpdatedAt            time.Time                                 `json:"updated_at"`
	History              []OperationWorkflowPlaybookProgressChange `json:"history"`
	HistoryTruncated     bool                                      `json:"history_truncated"`
}
