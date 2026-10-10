package api

import (
	"fmt"
	"regexp"
	"time"
)

// OperationWorkflowRetainedMilestoneRequirement selects a transaction-backed
// fact from a declared step of the same workflow contract.
type OperationWorkflowRetainedMilestoneRequirement struct {
	Operation string `json:"operation" yaml:"operation" toml:"operation"`
	Milestone string `json:"milestone" yaml:"milestone" toml:"milestone"`
}

// Verified means a matching app-reported fact remains retained in the selected
// ownership, subject, workflow instance and version. It is not external proof.
type OperationWorkflowRetainedMilestone struct {
	Recovery     *OperationWorkflowPrerequisiteRecovery          `json:"recovery,omitempty"`
	Freshness    *OperationWorkflowRetainedMilestoneFreshness    `json:"freshness,omitempty"`
	Invalidation *OperationWorkflowRetainedMilestoneInvalidation `json:"invalidation,omitempty"`
	Requirement  OperationWorkflowRetainedMilestoneRequirement   `json:"requirement"`
	Status       string                                          `json:"status"`
	Evidence     *OperationWorkflowInstanceMilestoneRef          `json:"evidence,omitempty"`
}

// OperationWorkflowRetainedMilestoneInvalidationRule links a prerequisite to a
// later or simultaneous app-reported fact in the same workflow instance/version.
type OperationWorkflowRetainedMilestoneInvalidationRule struct {
	Prerequisite  OperationWorkflowRetainedMilestoneRequirement `json:"prerequisite" yaml:"prerequisite" toml:"prerequisite"`
	InvalidatedBy OperationWorkflowRetainedMilestoneRequirement `json:"invalidated_by" yaml:"invalidated_by" toml:"invalidated_by"`
}

// OperationWorkflowRetainedMilestoneInvalidation identifies the retained fact
// that made a prerequisite unusable at the evaluation time.
type OperationWorkflowRetainedMilestoneInvalidation struct {
	Source   OperationWorkflowRetainedMilestoneRequirement `json:"source"`
	Evidence OperationWorkflowInstanceMilestoneRef         `json:"evidence"`
}

// OperationWorkflowRetainedMilestoneFreshnessRule pins the maximum age of a prerequisite.
type OperationWorkflowRetainedMilestoneFreshnessRule struct {
	Prerequisite  OperationWorkflowRetainedMilestoneRequirement `json:"prerequisite" yaml:"prerequisite" toml:"prerequisite"`
	MaxAgeSeconds int64                                         `json:"max_age_seconds" yaml:"max_age_seconds" toml:"max_age_seconds"`
}

// OperationWorkflowRetainedMilestoneFreshness describes eligibility at the server evaluation time.
type OperationWorkflowRetainedMilestoneFreshness struct {
	MaxAgeSeconds    int64      `json:"max_age_seconds"`
	EvaluatedAt      time.Time  `json:"evaluated_at"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	AgeSeconds       *int64     `json:"age_seconds,omitempty"`
	RemainingSeconds *int64     `json:"remaining_seconds,omitempty"`
}

// OperationWorkflowPrerequisiteRecovery describes application-owned recovery, not executable commands.
type OperationWorkflowPrerequisiteRecovery struct {
	Reason          string     `json:"reason"`
	Operation       string     `json:"operation"`
	Milestone       string     `json:"milestone"`
	AfterOccurredAt *time.Time `json:"after_occurred_at,omitempty"`
	Steps           []string   `json:"steps"`
}

// OperationWorkflowRetainedMilestoneGroup accepts any one fact or any one complete named path.
type OperationWorkflowRetainedMilestoneGroup struct {
	Paths []OperationWorkflowRetainedMilestonePath        `json:"paths,omitempty" yaml:"paths,omitempty" toml:"paths"`
	ID    string                                          `json:"id" yaml:"id" toml:"id"`
	AnyOf []OperationWorkflowRetainedMilestoneRequirement `json:"any_of,omitempty" yaml:"any_of,omitempty" toml:"any_of"`
}

// OperationWorkflowRetainedMilestoneGroupResult preserves every alternative's diagnostics.
type OperationWorkflowRetainedMilestoneGroupResult struct {
	SelectedPathID string                                         `json:"selected_path_id,omitempty"`
	Paths          []OperationWorkflowRetainedMilestonePathResult `json:"paths,omitempty"`
	ID             string                                         `json:"id"`
	Satisfied      bool                                           `json:"satisfied"`
	Alternatives   []OperationWorkflowRetainedMilestone           `json:"alternatives,omitempty"`
}

// OperationWorkflowRetainedMilestonePath requires all facts in one alternative business path.
type OperationWorkflowRetainedMilestonePath struct {
	Decision *OperationWorkflowPolicyRequirement             `json:"decision,omitempty" yaml:"decision,omitempty" toml:"decision"`
	ID       string                                          `json:"id" yaml:"id" toml:"id"`
	AllOf    []OperationWorkflowRetainedMilestoneRequirement `json:"all_of" yaml:"all_of" toml:"all_of"`
}

// OperationWorkflowRetainedMilestonePathResult includes every fact needed by a named path.
type OperationWorkflowRetainedMilestonePathResult struct {
	Decision     *OperationWorkflowPathDecisionResult `json:"decision,omitempty"`
	ID           string                               `json:"id"`
	Satisfied    bool                                 `json:"satisfied"`
	Requirements []OperationWorkflowRetainedMilestone `json:"requirements"`
}

// OperationWorkflowPathDecisionResult compares planned application reasoning with the pinned path rule.
type OperationWorkflowPathDecisionResult struct {
	Requirement OperationWorkflowPolicyRequirement `json:"requirement"`
	Reported    *OperationBusinessDecision         `json:"reported,omitempty"`
	Satisfied   bool                               `json:"satisfied"`
}

// OperationWorkflowPathSelection is the application's chosen path within a compound group.
type OperationWorkflowPathSelection struct {
	GroupID             string `json:"group_id"`
	PathID              string `json:"path_id"`
	DecisionMilestoneID string `json:"decision_milestone_id,omitempty"`
	DecisionCode        string `json:"decision_code,omitempty"`
	DecisionDescription string `json:"decision_description,omitempty"`
	DecisionRuleID      string `json:"decision_rule_id,omitempty"`
	DecisionRuleVersion string `json:"decision_rule_version,omitempty"`
}

// ValidateOperationWorkflowPathDecision validates the bounded explanation snapshot.
// The state store verifies its identity against transaction-backed milestone evidence.
func ValidateOperationWorkflowPathDecision(p OperationWorkflowPathSelection) error {
	if p.DecisionMilestoneID == "" {
		if p.DecisionCode != "" || p.DecisionDescription != "" || p.DecisionRuleID != "" || p.DecisionRuleVersion != "" {
			return fmt.Errorf("path decision explanation requires a milestone reference")
		}
		return nil
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(p.DecisionMilestoneID) || p.DecisionMilestoneID == "00000000-0000-0000-0000-000000000000" {
		return fmt.Errorf("path decision milestone requires a canonical UUID")
	}
	return ValidateOperationBusinessDecision(OperationBusinessDecision{Workflow: "path", InstanceID: "path", Code: p.DecisionCode, Description: p.DecisionDescription, RuleID: p.DecisionRuleID, RuleVersion: p.DecisionRuleVersion})
}
