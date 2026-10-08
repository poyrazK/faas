package api

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var operationWorkflowName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// OperationWorkflowStateStaleAfterMaxSeconds bounds app-declared state age
// thresholds while allowing long-running business workflows.
const OperationWorkflowStateStaleAfterMaxSeconds int64 = 10 * 365 * 24 * 60 * 60

// OperationWorkflowSpec is the deployment-resolved projection attached to an
// Operation definition. It is returned with a fact only when its milestone
// name matches. Source manifests pin the milestone and instance ID pointer;
// reads populate InstanceID from the retained public payload.
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

var operationWorkflowStateName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func ValidateOperationWorkflowName(value string) error {
	if len(value) == 0 || len(value) > OperationWorkflowNameMaxBytes || !operationWorkflowName.MatchString(value) {
		return fmt.Errorf("operation workflow name must be a bounded lowercase slug")
	}
	return nil
}

func ValidateOperationWorkflowStateName(value string) error {
	if len(value) == 0 || len(value) > OperationWorkflowStateMaxBytes || !operationWorkflowStateName.MatchString(value) {
		return fmt.Errorf("operation workflow state must be a bounded lowercase slug")
	}
	return nil
}

// ValidateOperationWorkflowInstanceID validates the stable, app-provided key
// that distinguishes two runs of the same workflow for one business reference.
func ValidateOperationWorkflowInstanceID(value string) error {
	if len(value) == 0 || len(value) > OperationWorkflowInstanceIDMaxBytes || !utf8.ValidString(value) || strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return fmt.Errorf("operation workflow instance ID must be a nonempty bounded UTF-8 string without control characters")
	}
	return nil
}
