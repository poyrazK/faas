package api

import "time"

// OperationWorkflowStateStaleAfterMaxSeconds bounds app-declared state age
// thresholds while allowing long-running business workflows.
const OperationWorkflowStateStaleAfterMaxSeconds int64 = 10 * 365 * 24 * 60 * 60

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

// OperationWorkflowPlaybookEvidenceRequirement binds one checklist label to current application evidence.
type OperationWorkflowPlaybookEvidenceRequirement struct {
	Label                 string `json:"label" yaml:"label" toml:"label"`
	Kind                  string `json:"kind" yaml:"kind" toml:"kind"`
	Operation             string `json:"operation" yaml:"operation" toml:"operation"`
	PrerequisiteOperation string `json:"prerequisite_operation,omitempty" yaml:"prerequisite_operation,omitempty" toml:"prerequisite_operation"`
	Milestone             string `json:"milestone,omitempty" yaml:"milestone,omitempty" toml:"milestone"`
	FromState             string `json:"from_state,omitempty" yaml:"from_state,omitempty" toml:"from_state"`
	ToState               string `json:"to_state,omitempty" yaml:"to_state,omitempty" toml:"to_state"`
	GroupID               string `json:"group_id,omitempty" yaml:"group_id,omitempty" toml:"group_id"`
	PathID                string `json:"path_id,omitempty" yaml:"path_id,omitempty" toml:"path_id"`
	BlockerCode           string `json:"blocker_code,omitempty" yaml:"blocker_code,omitempty" toml:"blocker_code"`
}

// OperationWorkflowPlaybookEvidenceCheck is ephemeral; it does not certify attached references or change human reviews.
type OperationWorkflowPlaybookEvidenceCheck struct {
	Requirement          OperationWorkflowPlaybookEvidenceRequirement `json:"requirement"`
	Status               string                                       `json:"status"`
	Reason               string                                       `json:"reason"`
	CheckedAt            time.Time                                    `json:"checked_at"`
	StateRevision        int64                                        `json:"state_revision,omitempty"`
	ReportingOperationID string                                       `json:"reporting_operation_id,omitempty"`
	ReviewSuggested      bool                                         `json:"review_suggested"`
	Evidence             *OperationWorkflowInstanceMilestoneRef       `json:"evidence,omitempty"`
	Prerequisite         *OperationWorkflowRetainedMilestone          `json:"prerequisite,omitempty"`
}

type OperationWorkflowPlaybook struct {
	OutcomeGuidance      []OperationWorkflowPlaybookOutcomeGuidance        `json:"outcome_guidance,omitempty" yaml:"outcome_guidance,omitempty" toml:"outcome_guidance"`
	OutcomeAcceptance    *OperationWorkflowPlaybookOutcomeAcceptancePolicy `json:"outcome_acceptance,omitempty" yaml:"outcome_acceptance,omitempty" toml:"outcome_acceptance"`
	OutcomeDeadline      *OperationWorkflowPlaybookOutcomeDeadlinePolicy   `json:"outcome_deadline,omitempty" yaml:"outcome_deadline,omitempty" toml:"outcome_deadline"`
	ExpectedOutcomeCode  string                                            `json:"expected_outcome_code,omitempty" yaml:"expected_outcome_code,omitempty" toml:"expected_outcome_code"`
	EvidenceRequirements []OperationWorkflowPlaybookEvidenceRequirement    `json:"evidence_requirements,omitempty" yaml:"evidence_requirements,omitempty" toml:"evidence_requirements"`
	ID                   string                                            `json:"id" yaml:"id" toml:"id"`
	Version              int                                               `json:"version" yaml:"version" toml:"version"`
	MatchKind            string                                            `json:"match_kind" yaml:"match_kind" toml:"match_kind"`
	MatchCode            string                                            `json:"match_code" yaml:"match_code" toml:"match_code"`
	Summary              string                                            `json:"summary" yaml:"summary" toml:"summary"`
	Owner                string                                            `json:"owner,omitempty" yaml:"owner,omitempty" toml:"owner"`
	RequiredEvidence     []string                                          `json:"required_evidence,omitempty" yaml:"required_evidence,omitempty" toml:"required_evidence"`
	RunbookURL           string                                            `json:"runbook_url,omitempty" yaml:"runbook_url,omitempty" toml:"runbook_url"`
	ActionURL            string                                            `json:"action_url,omitempty" yaml:"action_url,omitempty" toml:"action_url"`
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
	Owner       *string                             `json:"owner,omitempty"`
	OutcomeCode string                              `json:"outcome_code,omitempty"`
	Revision    int64                               `json:"revision"`
	ItemIndex   int                                 `json:"item_index"`
	Status      string                              `json:"status"`
	Note        string                              `json:"note"`
	Evidence    []OperationWorkflowPlaybookEvidence `json:"evidence"`
}
type OperationWorkflowPlaybookProgressItem struct {
	ReviewImpact             *OperationWorkflowPlaybookReviewImpact  `json:"review_impact,omitempty"`
	DependencyReviewDueAt    *time.Time                              `json:"dependency_review_due_at,omitempty"`
	DependencyReviewRevision int64                                   `json:"dependency_review_revision,omitempty"`
	Readiness                *OperationWorkflowPlaybookStepReadiness `json:"readiness,omitempty"`
	Owner                    string                                  `json:"owner,omitempty"`
	DueAt                    *time.Time                              `json:"due_at,omitempty"`
	EvidenceCheck            *OperationWorkflowPlaybookEvidenceCheck `json:"evidence_check,omitempty"`
	Label                    string                                  `json:"label"`
	Status                   string                                  `json:"status"`
	Note                     string                                  `json:"note"`
	Evidence                 []OperationWorkflowPlaybookEvidence     `json:"evidence"`
	ReviewedBy               string                                  `json:"reviewed_by,omitempty"`
	ReviewedAt               *time.Time                              `json:"reviewed_at,omitempty"`
}
type OperationWorkflowPlaybookProgressChange struct {
	OutcomeCode string                                `json:"outcome_code,omitempty"`
	Revision    int64                                 `json:"revision"`
	ItemIndex   int                                   `json:"item_index"`
	Item        OperationWorkflowPlaybookProgressItem `json:"item"`
	At          time.Time                             `json:"at"`
	Actor       string                                `json:"actor"`
}
type OperationWorkflowPlaybookProgress struct {
	OutcomeFollowUps    []OperationWorkflowPlaybookOutcomeFollowUp `json:"outcome_follow_ups,omitempty"`
	OutcomeDeadlineAt   *time.Time                                 `json:"outcome_deadline_at,omitempty"`
	StartStateRevision  int64                                      `json:"start_state_revision,omitempty"`
	BusinessOutcome     *OperationWorkflowPlaybookBusinessOutcome  `json:"business_outcome,omitempty"`
	Disposition         string                                     `json:"disposition,omitempty"`
	LatestReview        *OperationWorkflowPlaybookReview           `json:"latest_review,omitempty"`
	Reviews             []OperationWorkflowPlaybookReview          `json:"reviews,omitempty"`
	ReviewsTruncated    bool                                       `json:"reviews_truncated,omitempty"`
	ReplacesProgressIDs []string                                   `json:"replaces_progress_ids,omitempty"`

	Applicability        *OperationWorkflowPlaybookApplicability   `json:"applicability,omitempty"`
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

// OperationWorkflowPlaybookApplicability is an ephemeral exact-case check, never a checklist mutation.
type OperationWorkflowPlaybookApplicability struct {
	Status                 string    `json:"status"`
	Summary                string    `json:"summary"`
	SuggestedAction        string    `json:"suggested_action"`
	CheckedAt              time.Time `json:"checked_at"`
	CurrentContractVersion int       `json:"current_contract_version,omitempty"`
	StateRevision          int64     `json:"state_revision,omitempty"`
	ReportingOperationID   string    `json:"reporting_operation_id,omitempty"`
	DefinitionID           string    `json:"definition_id,omitempty"`
	DefinitionChanged      bool      `json:"definition_changed"`
	TriggerMatched         bool      `json:"trigger_matched"`
	EvidenceComplete       bool      `json:"evidence_complete"`
	RemainingItemCount     int       `json:"remaining_item_count"`
}

// OperationWorkflowPlaybookReviewRequest records a human coordination decision.
type OperationWorkflowPlaybookReviewRequest struct {
	Revision              int64  `json:"revision"`
	Decision              string `json:"decision"`
	Reason                string `json:"reason"`
	ReplacementProgressID string `json:"replacement_progress_id,omitempty"`
	ReplacementRevision   int64  `json:"replacement_revision,omitempty"`
}
type OperationWorkflowPlaybookReview struct {
	Revision              int64                                  `json:"revision"`
	Decision              string                                 `json:"decision"`
	Reason                string                                 `json:"reason"`
	Actor                 string                                 `json:"actor"`
	At                    time.Time                              `json:"at"`
	Applicability         OperationWorkflowPlaybookApplicability `json:"applicability"`
	ReplacementProgressID string                                 `json:"replacement_progress_id,omitempty"`
	ReplacementRevision   int64                                  `json:"replacement_revision,omitempty"`
}

// OperationWorkflowPlaybookEvidenceWatchRequest explicitly enrolls or stops one checklist in an inbox.
type OperationWorkflowPlaybookEvidenceWatchRequest struct {
	WatchID          string `json:"watch_id,omitempty"`
	SubscriptionID   string `json:"subscription_id"`
	ProgressRevision int64  `json:"progress_revision"`
	Revision         int64  `json:"revision"`
	Enabled          bool   `json:"enabled"`
	Owner            string `json:"owner"`
}

// Watch revisions fence configuration changes; polling does not advance them.
type OperationWorkflowPlaybookEvidenceWatch struct {
	ReviewAssignments    []OperationWorkflowPlaybookStepAssignmentReference    `json:"review_assignments,omitempty"`
	DependencyStates     []OperationWorkflowPlaybookDependencyState            `json:"dependency_states,omitempty"`
	StepAssignments      []OperationWorkflowPlaybookStepAssignmentReference    `json:"step_assignments,omitempty"`
	OutcomeDeadline      *OperationWorkflowPlaybookOutcomeDeadlinePolicy       `json:"outcome_deadline,omitempty"`
	DeadlineAt           *time.Time                                            `json:"deadline_at,omitempty"`
	DeadlineCheckedAt    *time.Time                                            `json:"deadline_checked_at,omitempty"`
	Escalation           *OperationWorkflowReadinessOutcomeEscalationReference `json:"escalation,omitempty"`
	OutcomeStateRevision int64                                                 `json:"outcome_state_revision,omitempty"`
	BusinessOutcome      *OperationWorkflowPlaybookBusinessOutcome             `json:"business_outcome,omitempty"`
	ID                   string                                                `json:"id"`
	ProgressID           string                                                `json:"progress_id"`
	SubscriptionID       string                                                `json:"subscription_id"`
	Revision             int64                                                 `json:"revision"`
	Status               string                                                `json:"status"`
	StopReason           string                                                `json:"stop_reason,omitempty"`
	Owner                string                                                `json:"owner"`
	PlatformTenantID     string                                                `json:"platform_tenant_id,omitempty"`
	Subject              OperationSubject                                      `json:"subject"`
	Workflow             string                                                `json:"workflow"`
	InstanceID           string                                                `json:"instance_id"`
	ContractVersion      int                                                   `json:"contract_version"`
	PlaybookID           string                                                `json:"playbook_id"`
	PlaybookVersion      int                                                   `json:"playbook_version"`
	CreatedAt            time.Time                                             `json:"created_at"`
	UpdatedAt            time.Time                                             `json:"updated_at"`
	UpdatedBy            string                                                `json:"updated_by"`
	EvaluatedAt          time.Time                                             `json:"evaluated_at"`
	Items                []OperationWorkflowPlaybookEvidenceCheck              `json:"items"`
}
type OperationWorkflowPlaybookEvidenceChange struct {
	BeforeStatus string                                 `json:"before_status"`
	BeforeReason string                                 `json:"before_reason"`
	After        OperationWorkflowPlaybookEvidenceCheck `json:"after"`
}
type OperationWorkflowPlaybookEvidenceEvent struct {
	DeadlineAt       *time.Time                                `json:"deadline_at,omitempty"`
	BusinessOutcome  *OperationWorkflowPlaybookBusinessOutcome `json:"business_outcome,omitempty"`
	OutcomeChange    *OperationWorkflowPlaybookOutcomeChange   `json:"outcome_change,omitempty"`
	WatchID          string                                    `json:"watch_id"`
	ProgressID       string                                    `json:"progress_id"`
	PlatformTenantID string                                    `json:"platform_tenant_id,omitempty"`
	Subject          OperationSubject                          `json:"subject"`
	Workflow         string                                    `json:"workflow"`
	InstanceID       string                                    `json:"instance_id"`
	ContractVersion  int                                       `json:"contract_version"`
	PlaybookID       string                                    `json:"playbook_id"`
	PlaybookVersion  int                                       `json:"playbook_version"`
	CheckedAt        time.Time                                 `json:"checked_at"`
	Changes          []OperationWorkflowPlaybookEvidenceChange `json:"changes"`
}

// Business results are observed separately from human progress and do not establish causation.
type OperationWorkflowPlaybookBusinessOutcome struct {
	OutcomeGuidance  []OperationWorkflowPlaybookOutcomeGuidance        `json:"outcome_guidance,omitempty"`
	Guidance         *OperationWorkflowPlaybookOutcomeGuidance         `json:"guidance,omitempty"`
	AcceptancePolicy *OperationWorkflowPlaybookOutcomeAcceptancePolicy `json:"acceptance_policy,omitempty"`
	Acceptance       string                                            `json:"acceptance,omitempty"`
	DeadlineAt       *time.Time                                        `json:"deadline_at,omitempty"`
	DeadlineReached  bool                                              `json:"deadline_reached,omitempty"`
	Status           string                                            `json:"status"`
	Reason           string                                            `json:"reason"`
	Evidence         OperationWorkflowReadinessBusinessOutcome         `json:"evidence"`
}
type OperationWorkflowPlaybookOutcomeChange struct {
	BeforeStatus              string                                   `json:"before_status"`
	BeforeReason              string                                   `json:"before_reason"`
	BeforeReportedOutcomeCode string                                   `json:"before_reported_outcome_code,omitempty"`
	After                     OperationWorkflowPlaybookBusinessOutcome `json:"after"`
}

// Outcome deadlines are attention policies, not business failure assertions or execution instructions.
type OperationWorkflowPlaybookOutcomeDeadlinePolicy struct {
	AcknowledgeAfterSeconds int64  `json:"acknowledge_after_seconds,omitempty" yaml:"acknowledge_after_seconds,omitempty" toml:"acknowledge_after_seconds"`
	FallbackOwner           string `json:"fallback_owner,omitempty" yaml:"fallback_owner,omitempty" toml:"fallback_owner"`
	AfterSeconds            int64  `json:"after_seconds" yaml:"after_seconds" toml:"after_seconds"`
	Owner                   string `json:"owner" yaml:"owner" toml:"owner"`
	FollowUpAfterSeconds    int64  `json:"follow_up_after_seconds" yaml:"follow_up_after_seconds" toml:"follow_up_after_seconds"`
	Note                    string `json:"note,omitempty" yaml:"note,omitempty" toml:"note"`
}

// Current evidence may refresh; delivery evidence and human coordination remain immutable to the worker.
type OperationWorkflowPlaybookOutcomeEscalation struct {
	Response          *OperationWorkflowReadinessEscalationResponse `json:"response,omitempty"`
	WatchID           string                                        `json:"watch_id"`
	ProgressID        string                                        `json:"progress_id"`
	DeadlineAt        time.Time                                     `json:"deadline_at"`
	Current           OperationWorkflowPlaybookBusinessOutcome      `json:"current"`
	SuccessObservedAt *time.Time                                    `json:"success_observed_at,omitempty"`
}

// Completion captures a human disposition and an advisory pre-write business observation.
type OperationWorkflowPlaybookCompletionOutcome struct {
	Resolution  string                                   `json:"resolution"`
	Alignment   string                                   `json:"alignment"`
	Explanation string                                   `json:"explanation"`
	Observation OperationWorkflowPlaybookBusinessOutcome `json:"observation"`
}

// Review windows monitor retained completion evidence without reopening human coordination.
type OperationWorkflowPlaybookCompletionReview struct {
	ID            string                                   `json:"id"`
	Revision      int64                                    `json:"revision"`
	Owner         string                                   `json:"owner"`
	Status        string                                   `json:"status"`
	ExpiresAt     time.Time                                `json:"expires_at"`
	StateRevision int64                                    `json:"state_revision"`
	Current       OperationWorkflowPlaybookBusinessOutcome `json:"current"`
}
type OperationWorkflowPlaybookCompletionReviewEvent struct {
	ReviewID             string                                   `json:"review_id"`
	SourceNotificationID string                                   `json:"source_notification_id"`
	SourceSequence       int64                                    `json:"source_sequence"`
	Resolution           string                                   `json:"resolution"`
	Previous             OperationWorkflowPlaybookBusinessOutcome `json:"previous"`
	Current              OperationWorkflowPlaybookBusinessOutcome `json:"current"`
}

// Explicit empty alternatives means only the expected terminal outcome is acceptable.
type OperationWorkflowPlaybookOutcomeAcceptancePolicy struct {
	AlternativeOutcomeCodes []string `json:"alternative_outcome_codes" yaml:"alternative_outcome_codes" toml:"alternative_outcome_codes"`
}

// Plain-text application guidance conveys meaning and suggested human follow-up, never execution authority.
type OperationWorkflowPlaybookOutcomeGuidance struct {
	FollowUpDependencies []OperationWorkflowPlaybookStepDependency       `json:"follow_up_dependencies,omitempty" yaml:"follow_up_dependencies,omitempty" toml:"follow_up_dependencies"`
	FollowUpAssignments  []OperationWorkflowPlaybookStepAssignmentPolicy `json:"follow_up_assignments,omitempty" yaml:"follow_up_assignments,omitempty" toml:"follow_up_assignments"`
	FollowUpSteps        []string                                        `json:"follow_up_steps,omitempty" yaml:"follow_up_steps,omitempty" toml:"follow_up_steps"`
	OutcomeCode          string                                          `json:"outcome_code" yaml:"outcome_code" toml:"outcome_code"`
	Description          string                                          `json:"description" yaml:"description" toml:"description"`
	SuggestedFollowUp    string                                          `json:"suggested_follow_up,omitempty" yaml:"suggested_follow_up,omitempty" toml:"suggested_follow_up"`
}

// Outcome follow-up tracks human work against the immutable reported outcome captured on first review.
type OperationWorkflowPlaybookOutcomeFollowUp struct {
	OutcomeCode       string                                   `json:"outcome_code"`
	Observation       OperationWorkflowPlaybookBusinessOutcome `json:"observation"`
	Items             []OperationWorkflowPlaybookProgressItem  `json:"items"`
	CompletedCount    int                                      `json:"completed_count"`
	ChecklistComplete bool                                     `json:"checklist_complete"`
}

// OperationWorkflowPlaybookReviewImpact explains the business result protected by a human review.
type OperationWorkflowPlaybookReviewImpact struct {
	Kind        string `json:"kind" yaml:"kind" toml:"kind"`
	Code        string `json:"code" yaml:"code" toml:"code"`
	Description string `json:"description" yaml:"description" toml:"description"`
	Priority    string `json:"priority" yaml:"priority" toml:"priority"`
}

type OperationWorkflowPlaybookStepAssignmentPolicy struct {
	ReviewImpact            *OperationWorkflowPlaybookReviewImpact `json:"review_impact,omitempty" yaml:"review_impact,omitempty" toml:"review_impact"`
	ReviewAfterSeconds      int64                                  `json:"review_after_seconds,omitempty" yaml:"review_after_seconds,omitempty" toml:"review_after_seconds"`
	ReviewFallbackOwner     string                                 `json:"review_fallback_owner,omitempty" yaml:"review_fallback_owner,omitempty" toml:"review_fallback_owner"`
	AcknowledgeAfterSeconds int64                                  `json:"acknowledge_after_seconds,omitempty" yaml:"acknowledge_after_seconds,omitempty" toml:"acknowledge_after_seconds"`
	FallbackOwner           string                                 `json:"fallback_owner,omitempty" yaml:"fallback_owner,omitempty" toml:"fallback_owner"`
	Label                   string                                 `json:"label" yaml:"label" toml:"label"`
	Owner                   string                                 `json:"owner" yaml:"owner" toml:"owner"`
	CompleteAfterSeconds    int64                                  `json:"complete_after_seconds" yaml:"complete_after_seconds" toml:"complete_after_seconds"`
}
type OperationWorkflowPlaybookStepAssignmentReference struct {
	DependencyReviewRevision int64                                                `json:"dependency_review_revision,omitempty"`
	OutcomeCode              string                                               `json:"outcome_code"`
	ItemIndex                int                                                  `json:"item_index"`
	Notification             OperationWorkflowReadinessOutcomeEscalationReference `json:"notification"`
}
type OperationWorkflowPlaybookStepAssignment struct {
	ReviewImpact                 *OperationWorkflowPlaybookReviewImpact        `json:"review_impact,omitempty"`
	DependencyReviewStatus       string                                        `json:"dependency_review_status,omitempty"`
	DependencyReviewPrerequisite string                                        `json:"dependency_review_prerequisite,omitempty"`
	DependencyReview             *OperationWorkflowPlaybookProgressItem        `json:"dependency_review,omitempty"`
	Readiness                    *OperationWorkflowPlaybookStepReadiness       `json:"readiness,omitempty"`
	SourceRevision               int64                                         `json:"source_revision,omitempty"`
	Response                     *OperationWorkflowReadinessEscalationResponse `json:"response,omitempty"`
	ProgressID                   string                                        `json:"progress_id"`
	OutcomeCode                  string                                        `json:"outcome_code"`
	ItemIndex                    int                                           `json:"item_index"`
	Label                        string                                        `json:"label"`
	Status                       string                                        `json:"status"`
	DueAt                        time.Time                                     `json:"due_at"`
	LastStepOwner                string                                        `json:"last_step_owner"`
	ManualOwner                  bool                                          `json:"manual_owner"`
	History                      []OperationWorkflowReadinessAssignmentChange  `json:"history"`
	HistoryTruncated             bool                                          `json:"history_truncated"`
}

type OperationWorkflowPlaybookStepDependency struct {
	Label         string   `json:"label" yaml:"label" toml:"label"`
	Prerequisites []string `json:"prerequisites" yaml:"prerequisites" toml:"prerequisites"`
}
type OperationWorkflowPlaybookStepReadiness struct {
	Status          string   `json:"status"`
	WaitingFor      []string `json:"waiting_for"`
	ReviewSuggested bool     `json:"review_suggested"`
}

type OperationWorkflowPlaybookDependencyState struct {
	Regression  *OperationWorkflowReadinessOutcomeEscalationReference `json:"regression,omitempty"`
	OutcomeCode string                                                `json:"outcome_code"`
	ItemIndex   int                                                   `json:"item_index"`
	Label       string                                                `json:"label"`
	Status      string                                                `json:"status"`
	Owner       string                                                `json:"owner"`
	Readiness   OperationWorkflowPlaybookStepReadiness                `json:"readiness"`
}
type OperationWorkflowPlaybookDependencyRegression struct {
	CheckedAt             time.Time                                  `json:"checked_at"`
	ReopenedPrerequisites []OperationWorkflowPlaybookDependencyState `json:"reopened_prerequisites"`
	AffectedSteps         []OperationWorkflowPlaybookDependencyState `json:"affected_steps"`
}

type OperationWorkflowPlaybookDependencyRecovery struct {
	CheckedAt  time.Time                                            `json:"checked_at"`
	Regression OperationWorkflowReadinessOutcomeEscalationReference `json:"regression"`
	ReadySteps []OperationWorkflowPlaybookDependencyState           `json:"ready_steps"`
}
