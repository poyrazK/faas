package api

import (
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
	Paths        []OperationWorkflowRetainedMilestonePathResult `json:"paths,omitempty"`
	ID           string                                         `json:"id"`
	Satisfied    bool                                           `json:"satisfied"`
	Alternatives []OperationWorkflowRetainedMilestone           `json:"alternatives,omitempty"`
}

// OperationWorkflowRetainedMilestonePath requires all facts in one alternative business path.
type OperationWorkflowRetainedMilestonePath struct {
	ID    string                                          `json:"id" yaml:"id" toml:"id"`
	AllOf []OperationWorkflowRetainedMilestoneRequirement `json:"all_of" yaml:"all_of" toml:"all_of"`
}

// OperationWorkflowRetainedMilestonePathResult includes every fact needed by a named path.
type OperationWorkflowRetainedMilestonePathResult struct {
	ID           string                               `json:"id"`
	Satisfied    bool                                 `json:"satisfied"`
	Requirements []OperationWorkflowRetainedMilestone `json:"requirements"`
}
