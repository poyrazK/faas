// ADR-715: durable business facts are independent of execution attempts.
package api

import (
	"encoding/json"
	"time"
)

type OperationMilestoneRequest struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Payload    json.RawMessage `json:"payload"`
	OccurredAt time.Time       `json:"occurred_at"`
}

type OperationMilestoneValidationRequest struct {
	Milestones []OperationMilestoneRequest `json:"milestones"`
}

type OperationMilestoneValidationResponse struct {
	Valid bool `json:"valid"`
}

type OperationMilestone struct {
	WorkflowSteps    []OperationWorkflowSpec `json:"workflow_steps,omitempty"`
	ID               string                  `json:"id"`
	OperationID      string                  `json:"operation_id"`
	PlatformTenantID string                  `json:"platform_tenant_id,omitempty"`
	Subject          *OperationSubject       `json:"subject,omitempty"`
	Name             string                  `json:"name"`
	Payload          json.RawMessage         `json:"payload"`
	OccurredAt       time.Time               `json:"occurred_at"`
	CreatedAt        time.Time               `json:"created_at"`
	Sequence         int64                   `json:"sequence"`
}

type OperationMilestonesResponse struct {
	Milestones              []OperationMilestone                 `json:"milestones"`
	WorkflowStates          []OperationWorkflowState             `json:"workflow_states,omitempty"`
	WorkflowStateHistory    []OperationWorkflowStateHistoryEntry `json:"workflow_state_history,omitempty"`
	WorkflowInstance        *OperationWorkflowInstanceSnapshot   `json:"workflow_instance,omitempty"`
	NextCursor              string                               `json:"next_cursor,omitempty"`
	NextWorkflowStateCursor string                               `json:"next_workflow_state_cursor,omitempty"`
}

// OperationWorkflowInstanceSnapshot groups the selected contract's steps and
// allowed transitions with the latest app-reported state for one workflow
// instance. Page-scoped facts and transition history follow their cursors;
// retention summaries do not.
type OperationWorkflowInstanceSnapshot struct {
	Bottlenecks                 *OperationWorkflowBottlenecks             `json:"bottlenecks,omitempty"`
	ResolutionVerifications     []OperationWorkflowResolutionVerification `json:"resolution_verifications,omitempty"`
	AwaitingVerificationCount   int64                                     `json:"awaiting_verification_count"`
	ResolutionVerificationCount int64                                     `json:"resolution_verification_count"`
	Readiness                   *OperationWorkflowReadinessOverview       `json:"readiness,omitempty"`
	DependencyTrace             *OperationWorkflowDependencyTrace         `json:"dependency_trace,omitempty"`
	DependencyImpact            *OperationWorkflowDependencyImpact        `json:"dependency_impact,omitempty"`
	RelatedWorkflows            []OperationWorkflowRelatedInstance        `json:"related_workflows,omitempty"`
	Decision                    *OperationWorkflowDecision                `json:"decision,omitempty"`
	Workflow                    string                                    `json:"workflow"`
	InstanceID                  string                                    `json:"instance_id"`
	ContractVersion             int                                       `json:"contract_version"`
	State                       *OperationWorkflowState                   `json:"state,omitempty"`
	Steps                       []OperationWorkflowInstanceStep           `json:"steps"`
	AllowedTransitions          []OperationWorkflowInstanceTransition     `json:"allowed_transitions"`
	Transitions                 []OperationWorkflowStateHistoryEntry      `json:"transitions"`
	HasMore                     bool                                      `json:"has_more"`
	NextMilestoneCursor         string                                    `json:"next_milestone_cursor,omitempty"`
	NextTransitionCursor        string                                    `json:"next_transition_cursor,omitempty"`
}

// OperationWorkflowInstanceTransition is an allowed edge from the selected
// workflow contract, bound to the Operation that can report it.
type OperationWorkflowInstanceTransition struct {
	From                        string                                  `json:"from"`
	To                          string                                  `json:"to"`
	Operation                   string                                  `json:"operation"`
	RequiredDependencyWorkflows *[]string                               `json:"required_dependency_workflows,omitempty"`
	RequiredEffects             []OperationWorkflowEffectRequirement    `json:"required_effects,omitempty"`
	RequiredInvariants          []OperationWorkflowInvariantRequirement `json:"required_invariants,omitempty"`
	RequiredPolicies            []OperationWorkflowPolicyRequirement    `json:"required_policies,omitempty"`
	RequiredMilestones          []string                                `json:"required_milestones,omitempty"`
}

// OperationWorkflowInstanceStep is one declared step with page-scoped and
// retention-wide summaries of matching facts for this instance.
type OperationWorkflowInstanceStep struct {
	Step                    string                                 `json:"step"`
	Label                   string                                 `json:"label"`
	Operation               string                                 `json:"operation,omitempty"`
	OperationID             string                                 `json:"operation_id,omitempty"`
	Milestone               string                                 `json:"milestone"`
	Position                int                                    `json:"position"`
	Observed                bool                                   `json:"observed"`
	MilestonesInPage        int                                    `json:"milestones_in_page"`
	LatestMilestone         *OperationWorkflowInstanceMilestoneRef `json:"latest_milestone,omitempty"`
	ObservedInRetention     bool                                   `json:"observed_in_retention"`
	MilestonesInRetention   int64                                  `json:"milestones_in_retention"`
	LatestRetainedMilestone *OperationWorkflowInstanceMilestoneRef `json:"latest_retained_milestone,omitempty"`
}

// OperationWorkflowInstanceMilestoneRef identifies a matching fact without
// duplicating its payload.
type OperationWorkflowInstanceMilestoneRef struct {
	ID          string    `json:"id"`
	OperationID string    `json:"operation_id"`
	OccurredAt  time.Time `json:"occurred_at"`
	PublishedAt time.Time `json:"published_at"`
}

// OperationWorkflowStateReport is an explicit app-owned snapshot update. Its
// revision is assigned inside the app transaction and keeps late publications
// from replacing a newer business state.
type OperationWorkflowStateReport struct {
	DependsOn          []OperationWorkflowDependency        `json:"depends_on,omitempty"`
	DependenciesOnly   bool                                 `json:"dependencies_only,omitempty"`
	OutcomeCode        string                               `json:"outcome_code,omitempty"`
	OutcomeDescription string                               `json:"outcome_description,omitempty"`
	OutcomeOnly        bool                                 `json:"outcome_only,omitempty"`
	DeadlineAt         string                               `json:"deadline_at,omitempty"`
	DeadlineOnly       bool                                 `json:"deadline_only,omitempty"`
	BlockerResolutions []OperationWorkflowBlockerResolution `json:"blocker_resolutions,omitempty"`
	Blockers           []OperationWorkflowBlocker           `json:"blockers,omitempty"`
	BlockersOnly       bool                                 `json:"blockers_only,omitempty"`
	ID                 string                               `json:"id"`
	Workflow           string                               `json:"workflow"`
	InstanceID         string                               `json:"instance_id"`
	FromState          string                               `json:"from_state,omitempty"`
	State              string                               `json:"state"`
	Revision           int64                                `json:"revision"`
	OccurredAt         time.Time                            `json:"occurred_at"`
	ContractVersion    int                                  `json:"contract_version,omitempty"`
	EvidenceMilestones []OperationWorkflowEvidenceMilestone `json:"evidence_milestones,omitempty"`
}

// OperationWorkflowEvidenceMilestone links a state transition to a fact
// committed in the same application transaction.
type OperationWorkflowEvidenceMilestone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type OperationWorkflowStateValidationRequest struct {
	WorkflowStates []OperationWorkflowStateReport `json:"workflow_states"`
	Milestones     []OperationMilestoneRequest    `json:"milestones,omitempty"`
}

type OperationWorkflowStateValidationResponse struct {
	Valid bool `json:"valid"`
}

type OperationWorkflowStateReportResponse struct {
	DependsOn          []OperationWorkflowDependency        `json:"depends_on,omitempty"`
	DependenciesOnly   bool                                 `json:"dependencies_only,omitempty"`
	OutcomeCode        string                               `json:"outcome_code,omitempty"`
	OutcomeDescription string                               `json:"outcome_description,omitempty"`
	OutcomeOnly        bool                                 `json:"outcome_only,omitempty"`
	DeadlineAt         string                               `json:"deadline_at,omitempty"`
	DeadlineOnly       bool                                 `json:"deadline_only,omitempty"`
	BlockerResolutions []OperationWorkflowBlockerResolution `json:"blocker_resolutions,omitempty"`
	Blockers           []OperationWorkflowBlocker           `json:"blockers,omitempty"`
	BlockersOnly       bool                                 `json:"blockers_only,omitempty"`
	ID                 string                               `json:"id"`
	OperationID        string                               `json:"operation_id"`
	Workflow           string                               `json:"workflow"`
	InstanceID         string                               `json:"instance_id"`
	FromState          string                               `json:"from_state,omitempty"`
	State              string                               `json:"state"`
	Revision           int64                                `json:"revision"`
	ContractVersion    int                                  `json:"contract_version"`
	EvidenceMilestones []OperationWorkflowEvidenceMilestone `json:"evidence_milestones,omitempty"`
}

// OperationWorkflowState is the latest explicit state reported for one
// business workflow instance.
type OperationWorkflowState struct {
	SLA                *OperationWorkflowStateSLA           `json:"sla,omitempty"`
	DependsOn          []OperationWorkflowDependency        `json:"depends_on,omitempty"`
	DependenciesOnly   bool                                 `json:"dependencies_only,omitempty"`
	OutcomeCode        string                               `json:"outcome_code,omitempty"`
	OutcomeDescription string                               `json:"outcome_description,omitempty"`
	OutcomeOnly        bool                                 `json:"outcome_only,omitempty"`
	Overdue            bool                                 `json:"overdue"`
	OverdueSeconds     int64                                `json:"overdue_seconds,omitempty"`
	DeadlineAt         string                               `json:"deadline_at,omitempty"`
	DeadlineOnly       bool                                 `json:"deadline_only,omitempty"`
	ReportID           string                               `json:"report_id,omitempty"`
	OperationID        string                               `json:"operation_id,omitempty"`
	BlockerResolutions []OperationWorkflowBlockerResolution `json:"blocker_resolutions,omitempty"`
	Blockers           []OperationWorkflowBlocker           `json:"blockers,omitempty"`
	BlockersOnly       bool                                 `json:"blockers_only,omitempty"`
	Workflow           string                               `json:"workflow"`
	InstanceID         string                               `json:"instance_id"`
	State              string                               `json:"state"`
	Terminal           bool                                 `json:"terminal"`
	Stale              bool                                 `json:"stale"`
	OccurredAt         time.Time                            `json:"occurred_at"`
	StaleAfterSeconds  int64                                `json:"stale_after_seconds,omitempty"`
	Revision           int64                                `json:"revision"`
	ContractVersion    int                                  `json:"contract_version"`
	EvidenceMilestones []OperationWorkflowEvidenceMilestone `json:"evidence_milestones,omitempty"`
	UpdatedAt          time.Time                            `json:"updated_at"`
	PlatformTenantID   string                               `json:"platform_tenant_id,omitempty"`
}

// OperationWorkflowStateHistoryEntry is one retained app-reported state
// update, ordered within a run by its app-assigned revision.
type OperationWorkflowStateHistoryEntry struct {
	ResolutionVerifications []OperationWorkflowResolutionVerification `json:"resolution_verifications,omitempty"`
	DependsOn               []OperationWorkflowDependency             `json:"depends_on,omitempty"`
	DependenciesOnly        bool                                      `json:"dependencies_only,omitempty"`
	OutcomeCode             string                                    `json:"outcome_code,omitempty"`
	OutcomeDescription      string                                    `json:"outcome_description,omitempty"`
	OutcomeOnly             bool                                      `json:"outcome_only,omitempty"`
	DeadlineAt              string                                    `json:"deadline_at,omitempty"`
	DeadlineOnly            bool                                      `json:"deadline_only,omitempty"`
	BlockerResolutions      []OperationWorkflowBlockerResolution      `json:"blocker_resolutions,omitempty"`
	Blockers                []OperationWorkflowBlocker                `json:"blockers,omitempty"`
	BlockersOnly            bool                                      `json:"blockers_only,omitempty"`
	ID                      string                                    `json:"id"`
	OperationID             string                                    `json:"operation_id"`
	Workflow                string                                    `json:"workflow"`
	InstanceID              string                                    `json:"instance_id"`
	FromState               string                                    `json:"from_state,omitempty"`
	State                   string                                    `json:"state"`
	Revision                int64                                     `json:"revision"`
	ContractVersion         int                                       `json:"contract_version"`
	EvidenceMilestones      []OperationWorkflowEvidenceMilestone      `json:"evidence_milestones,omitempty"`
	OccurredAt              time.Time                                 `json:"occurred_at"`
	PublishedAt             time.Time                                 `json:"published_at"`
	PlatformTenantID        string                                    `json:"platform_tenant_id,omitempty"`
}

type OperationMilestoneListOptions struct {
	// ReadinessOnly is an internal projection option; clients do not transmit it.
	ReadinessOnly bool
	AppID         string
	Scope         string
	OperationID   string
	SubjectType   string
	SubjectID     string
	// Workflow and WorkflowInstanceID are paired filters for a business-reference feed.
	Workflow            string
	WorkflowInstanceID  string
	WorkflowStateCursor string
	WorkflowStaleOnly   bool
	TenantID            string // Account operator filter; never a tenant-self owner override.
	Limit               int
	Cursor              string
}

// OperationWorkflowDecision explains contract options from reported facts, not execution authorization.
type OperationWorkflowDecision struct {
	Blockers       []OperationWorkflowBlocker            `json:"blockers"`
	Reason         string                                `json:"reason"`
	Explanation    string                                `json:"explanation"`
	NeedsAttention bool                                  `json:"needs_attention"`
	StateRevision  int64                                 `json:"state_revision,omitempty"`
	NextActions    []OperationWorkflowInstanceTransition `json:"next_actions"`
}

// OperationWorkflowBlocker is a public application-reported reason a target Operation must wait.
type OperationWorkflowBlocker struct {
	Priority        string `json:"priority,omitempty"`
	BusinessImpact  string `json:"business_impact,omitempty"`
	AcknowledgedAt  string `json:"acknowledged_at,omitempty"`
	AcknowledgedBy  string `json:"acknowledged_by,omitempty"`
	FollowUpAt      string `json:"follow_up_at,omitempty"`
	Owner           string `json:"owner,omitempty"`
	NextAction      string `json:"next_action,omitempty"`
	FirstObservedAt string `json:"first_observed_at,omitempty"`
	Code            string `json:"code"`
	Description     string `json:"description"`
	Operation       string `json:"operation"`
}

// OperationWorkflowBlockerResolution records why one prior reported blocker was cleared.
// Its containing state report provides the resolution identity, revision and timestamps.
type OperationWorkflowBlockerResolution struct {
	VerificationMilestoneID   string `json:"verification_milestone_id,omitempty"`
	VerificationMilestoneName string `json:"verification_milestone_name,omitempty"`
	VerificationOperationID   string `json:"verification_operation_id,omitempty"`
	VerificationOwner         string `json:"verification_owner,omitempty"`

	ResolvedBy         string `json:"resolved_by,omitempty"`
	Code               string `json:"code"`
	Operation          string `json:"operation"`
	Description        string `json:"description"`
	BlockerOperationID string `json:"blocker_operation_id"`
	BlockerReportID    string `json:"blocker_report_id"`
	BlockerRevision    int64  `json:"blocker_revision"`
}

// OperationWorkflowDependency is an application-reported prerequisite in the same
// app/customer/environment. Subject identifies the linked business object.
type OperationWorkflowDependency struct {
	SubjectType         string `json:"subject_type"`
	SubjectID           string `json:"subject_id"`
	Workflow            string `json:"workflow"`
	InstanceID          string `json:"instance_id"`
	RequiredOutcomeCode string `json:"required_outcome_code,omitempty"`
}
type OperationWorkflowRelatedInstance struct {
	Dependency OperationWorkflowDependency `json:"dependency"`
	Status     string                      `json:"status"`
	State      *OperationWorkflowState     `json:"state,omitempty"`
}

// OperationWorkflowDependencyImpact counts retained sources pointing at one prerequisite.
// Items are capped at 100 and ordered with affected workflows first.
type OperationWorkflowDependencyImpact struct {
	Items                 []OperationWorkflowDependentInstance `json:"items"`
	WorkflowCount         int64                                `json:"workflow_count"`
	ImpactedWorkflowCount int64                                `json:"impacted_workflow_count"`
	HasMore               bool                                 `json:"has_more"`
}

type OperationWorkflowDependentInstance struct {
	Subject             OperationSubject       `json:"subject"`
	State               OperationWorkflowState `json:"state"`
	RequiredOutcomeCode string                 `json:"required_outcome_code,omitempty"`
	DependencyStatus    string                 `json:"dependency_status"`
	NeedsAttention      bool                   `json:"needs_attention"`
}

// OperationWorkflowDependencyTrace is a bounded observational trace of unmet prerequisites.
type OperationWorkflowDependencyTrace struct {
	Findings                []OperationWorkflowDependencyFinding `json:"findings"`
	VisitedWorkflowCount    int                                  `json:"visited_workflow_count"`
	ExaminedDependencyCount int                                  `json:"examined_dependency_count"`
	DepthLimit              int                                  `json:"depth_limit"`
	WorkflowLimit           int                                  `json:"workflow_limit"`
	FindingLimit            int                                  `json:"finding_limit"`
	DependencyLimit         int                                  `json:"dependency_limit"`
	Truncated               bool                                 `json:"truncated"`
	LimitsReached           []string                             `json:"limits_reached,omitempty"`
}

type OperationWorkflowDependencyFinding struct {
	Kind        string                        `json:"kind"`
	Path        []OperationWorkflowDependency `json:"path"`
	Explanation string                        `json:"explanation"`
	State       *OperationWorkflowState       `json:"state,omitempty"`
	Limit       string                        `json:"limit,omitempty"`
}
