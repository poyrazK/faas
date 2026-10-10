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
