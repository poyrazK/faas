package api

import "time"

// Readiness subscriptions discover bounded current edges and compare saved observations.
// They produce advisory inbox notifications, never execution authority.
type OperationWorkflowReadinessSubscriptionRequest struct {
	BusinessImpactWatches []OperationWorkflowReadinessImpactWatchRequest `json:"business_impact_watches,omitempty"`
	AppID                 string                                         `json:"app_id,omitempty"`
	TenantID              string                                         `json:"tenant_id,omitempty"`
	Scope                 string                                         `json:"scope"`
	Name                  string                                         `json:"name"`
	Workflow              string                                         `json:"workflow,omitempty"`
	TargetOperation       string                                         `json:"target_operation,omitempty"`
	Owner                 string                                         `json:"owner,omitempty"`
	Unassigned            bool                                           `json:"unassigned,omitempty"`
	Events                []string                                       `json:"events,omitempty"`
}
type OperationWorkflowReadinessSubscription struct {
	BusinessImpactWatches   []OperationWorkflowReadinessImpactWatch  `json:"business_impact_watches,omitempty"`
	PlaybookEvidenceWatches []OperationWorkflowPlaybookEvidenceWatch `json:"playbook_evidence_watches,omitempty"`
	ID                      string                                   `json:"id"`
	AppID                   string                                   `json:"app_id"`
	TenantID                string                                   `json:"tenant_id,omitempty"`
	Scope                   string                                   `json:"scope"`
	Name                    string                                   `json:"name"`
	Workflow                string                                   `json:"workflow,omitempty"`
	TargetOperation         string                                   `json:"target_operation,omitempty"`
	Owner                   string                                   `json:"owner,omitempty"`
	Unassigned              bool                                     `json:"unassigned,omitempty"`
	Events                  []string                                 `json:"events"`
	CreatedAt               time.Time                                `json:"created_at"`
	EvaluatedAt             time.Time                                `json:"evaluated_at"`
	Status                  string                                   `json:"status"`
	WatchedTransitionCount  int                                      `json:"watched_transition_count"`
	CoverageIncomplete      bool                                     `json:"coverage_incomplete"`
	LatestSequence          int64                                    `json:"latest_sequence"`
}
type OperationWorkflowReadinessSubscriptionsResponse struct {
	Items []OperationWorkflowReadinessSubscription `json:"items"`
}
type OperationWorkflowReadinessNotification struct {
	ReviewBlockage       *OperationWorkflowReviewResponseBlockageEvent      `json:"review_blockage,omitempty"`
	ReviewCheckIn        *OperationWorkflowReviewResponseCheckInEvent       `json:"review_check_in,omitempty"`
	ReviewResponse       *OperationWorkflowReviewResponse                   `json:"review_response,omitempty"`
	BusinessImpact       *OperationWorkflowReadinessImpactEvent             `json:"business_impact,omitempty"`
	DependencyRecovery   *OperationWorkflowPlaybookDependencyRecovery       `json:"dependency_recovery,omitempty"`
	DependencyRegression *OperationWorkflowPlaybookDependencyRegression     `json:"dependency_regression,omitempty"`
	PlaybookStep         *OperationWorkflowPlaybookStepAssignment           `json:"playbook_step,omitempty"`
	PlaybookReview       *OperationWorkflowPlaybookCompletionReview         `json:"playbook_review,omitempty"`
	PlaybookReviewEvent  *OperationWorkflowPlaybookCompletionReviewEvent    `json:"playbook_review_event,omitempty"`
	PlaybookEscalation   *OperationWorkflowPlaybookOutcomeEscalation        `json:"playbook_escalation,omitempty"`
	PlaybookEvidence     *OperationWorkflowPlaybookEvidenceEvent            `json:"playbook_evidence,omitempty"`
	EscalationResponse   *OperationWorkflowReadinessEscalationResponseEvent `json:"escalation_response,omitempty"`
	OutcomeEscalation    *OperationWorkflowReadinessOutcomeEscalation       `json:"outcome_escalation,omitempty"`
	BusinessOutcomeWatch *OperationWorkflowReadinessBusinessOutcomeWatch    `json:"business_outcome_watch,omitempty"`
	BusinessOutcome      *OperationWorkflowReadinessBusinessOutcomeEvent    `json:"business_outcome,omitempty"`
	VerificationWatch    *OperationWorkflowReadinessVerificationWatch       `json:"verification_watch,omitempty"`
	Verification         *OperationWorkflowReadinessVerificationEvent       `json:"verification,omitempty"`
	Reminder             *OperationWorkflowReadinessDueReminder             `json:"reminder,omitempty"`
	Coordination         *OperationWorkflowReadinessCoordination            `json:"coordination,omitempty"`
	ID                   string                                             `json:"id"`
	Sequence             int64                                              `json:"sequence"`
	Kind                 string                                             `json:"kind"`
	CreatedAt            time.Time                                          `json:"created_at"`
	Explanation          string                                             `json:"explanation"`
	Change               *OperationWorkflowReadinessChange                  `json:"change,omitempty"`
}
type OperationWorkflowReadinessInboxResponse struct {
	Subscription   OperationWorkflowReadinessSubscription   `json:"subscription"`
	Items          []OperationWorkflowReadinessNotification `json:"items"`
	LatestSequence int64                                    `json:"latest_sequence"`
	NextSequence   int64                                    `json:"next_sequence"`
	ResyncRequired bool                                     `json:"resync_required"`
	HasMore        bool                                     `json:"has_more"`
}

// Coordination is shared inbox bookkeeping, never business workflow evidence.
type OperationWorkflowReadinessCoordination struct {
	Completion     *OperationWorkflowReadinessCompletion `json:"completion,omitempty"`
	Revision       int64                                 `json:"revision"`
	Status         string                                `json:"status"`
	Owner          string                                `json:"owner"`
	Note           string                                `json:"note"`
	FollowUpAt     *time.Time                            `json:"follow_up_at,omitempty"`
	UpdatedAt      time.Time                             `json:"updated_at"`
	UpdatedBy      string                                `json:"updated_by"`
	AcknowledgedAt *time.Time                            `json:"acknowledged_at,omitempty"`
	AcknowledgedBy string                                `json:"acknowledged_by,omitempty"`
}
type OperationWorkflowReadinessCoordinationRequest struct {
	ReviewResponse *OperationWorkflowReviewResponseRequest      `json:"review_response,omitempty"`
	Completion     *OperationWorkflowReadinessCompletionRequest `json:"completion,omitempty"`
	Revision       int64                                        `json:"revision"`
	Status         string                                       `json:"status"`
	Owner          string                                       `json:"owner"`
	Note           string                                       `json:"note"`
	FollowUpAt     *time.Time                                   `json:"follow_up_at,omitempty"`
}

// Handoff summarizes retained application evidence; it never authorizes execution.
type OperationWorkflowReadinessHandoff struct {
	Summary             string     `json:"summary"`
	BaselineEvaluatedAt *time.Time `json:"baseline_evaluated_at,omitempty"`
	Comparable          bool       `json:"comparable"`
	Limitations         []string   `json:"limitations"`
}
type OperationWorkflowReadinessNotificationCheck struct {
	BusinessOutcome *OperationWorkflowReadinessBusinessOutcome `json:"business_outcome,omitempty"`
	Handoff         OperationWorkflowReadinessHandoff          `json:"handoff"`
	Notification    OperationWorkflowReadinessNotification     `json:"notification"`
	CheckedAt       time.Time                                  `json:"checked_at"`
	Status          string                                     `json:"status"`
	Change          *OperationWorkflowReadinessChange          `json:"change,omitempty"`
}

// Follow-up queues aggregate retained coordination, not application execution tasks.
type OperationWorkflowReadinessFollowUpOptions struct {
	ImpactPriority string `json:"impact_priority,omitempty"`
	ImpactKind     string `json:"impact_kind,omitempty"`
	ImpactCode     string `json:"impact_code,omitempty"`
	Sort           string `json:"sort,omitempty"`
	ReminderOnly   bool   `json:"reminder_only,omitempty"`
	AppID          string `json:"app_id,omitempty"`
	TenantID       string `json:"tenant_id,omitempty"`
	Scope          string `json:"scope"`
	Owner          string `json:"owner,omitempty"`
	Workflow       string `json:"workflow,omitempty"`
	Due            string `json:"due,omitempty"`
	Offset         int    `json:"offset,omitempty"`
	Limit          int    `json:"limit,omitempty"`
}
type OperationWorkflowReadinessFollowUp struct {
	ReviewResponse       *OperationWorkflowReviewResponse             `json:"review_response,omitempty"`
	BusinessImpact       *OperationWorkflowReadinessImpactEvent       `json:"business_impact,omitempty"`
	PlaybookStep         *OperationWorkflowPlaybookStepAssignment     `json:"playbook_step,omitempty"`
	PlaybookEscalation   *OperationWorkflowPlaybookOutcomeEscalation  `json:"playbook_escalation,omitempty"`
	OutcomeEscalation    *OperationWorkflowReadinessOutcomeEscalation `json:"outcome_escalation,omitempty"`
	Reminder             *OperationWorkflowReadinessDueReminder       `json:"reminder,omitempty"`
	SubscriptionID       string                                       `json:"subscription_id"`
	SubscriptionName     string                                       `json:"subscription_name"`
	SubscriptionTenantID string                                       `json:"subscription_tenant_id,omitempty"`
	PlatformTenantID     string                                       `json:"platform_tenant_id,omitempty"`
	Scope                string                                       `json:"scope"`
	NotificationID       string                                       `json:"notification_id"`
	Sequence             int64                                        `json:"sequence"`
	Kind                 string                                       `json:"kind"`
	CreatedAt            time.Time                                    `json:"created_at"`
	Coordination         OperationWorkflowReadinessCoordination       `json:"coordination"`
	Workflow             string                                       `json:"workflow,omitempty"`
	InstanceID           string                                       `json:"instance_id,omitempty"`
	Subject              *OperationSubject                            `json:"subject,omitempty"`
	Operation            string                                       `json:"operation,omitempty"`
	FromState            string                                       `json:"from_state,omitempty"`
	ToState              string                                       `json:"to_state,omitempty"`
	Overdue              bool                                         `json:"overdue"`
}
type OperationWorkflowReadinessFollowUpsResponse struct {
	BusinessImpactSummary []OperationWorkflowReadinessImpactSummary `json:"business_impact_summary,omitempty"`
	DueReminderCount      int                                       `json:"due_reminder_count"`
	Items                 []OperationWorkflowReadinessFollowUp      `json:"items"`
	EvaluatedAt           time.Time                                 `json:"evaluated_at"`
	TotalCount            int                                       `json:"total_count"`
	OverdueCount          int                                       `json:"overdue_count"`
	NextOffset            int                                       `json:"next_offset"`
	HasMore               bool                                      `json:"has_more"`
	RetentionLimited      bool                                      `json:"retention_limited"`
}

// A reminder is one durable delivery marker for the original notification/deadline.
type OperationWorkflowReadinessDueReminder struct {
	ID        string    `json:"id"`
	DueAt     time.Time `json:"due_at"`
	CreatedAt time.Time `json:"created_at"`
}

// Completion closes coordination only; outcome is an operator/customer assertion.
type OperationWorkflowReadinessCompletionRequest struct {
	PlaybookReviewAfterSeconds int64                                              `json:"playbook_review_after_seconds,omitempty"`
	PlaybookResolution         string                                             `json:"playbook_resolution,omitempty"`
	EscalationPolicy           *OperationWorkflowReadinessOutcomeEscalationPolicy `json:"escalation_policy,omitempty"`
	WatchOutcome               bool                                               `json:"watch_outcome,omitempty"`
	OutcomeDeadlineAt          *time.Time                                         `json:"outcome_deadline_at,omitempty"`
	ExpectedOutcomeCode        string                                             `json:"expected_outcome_code,omitempty"`
	WatchVerification          bool                                               `json:"watch_verification,omitempty"`
	Outcome                    string                                             `json:"outcome"`
	Note                       string                                             `json:"note"`
	EvidenceOperationIDs       []string                                           `json:"evidence_operation_ids"`
}
type OperationWorkflowReadinessCompletion struct {
	PlaybookReviewAfterSeconds int64                                              `json:"playbook_review_after_seconds,omitempty"`
	PlaybookOutcome            *OperationWorkflowPlaybookCompletionOutcome        `json:"playbook_outcome,omitempty"`
	EscalationPolicy           *OperationWorkflowReadinessOutcomeEscalationPolicy `json:"escalation_policy,omitempty"`
	WatchOutcome               bool                                               `json:"watch_outcome,omitempty"`
	OutcomeDeadlineAt          *time.Time                                         `json:"outcome_deadline_at,omitempty"`
	ExpectedOutcomeCode        string                                             `json:"expected_outcome_code,omitempty"`
	BusinessOutcome            *OperationWorkflowReadinessBusinessOutcome         `json:"business_outcome,omitempty"`
	WatchVerification          bool                                               `json:"watch_verification,omitempty"`
	Outcome                    string                                             `json:"outcome"`
	Note                       string                                             `json:"note"`
	EvidenceOperationIDs       []string                                           `json:"evidence_operation_ids"`
	CompletedAt                time.Time                                          `json:"completed_at"`
	CompletedBy                string                                             `json:"completed_by"`
	Evidence                   OperationWorkflowReadinessCompletionEvidence       `json:"evidence"`
}

// Evidence records the advisory pre-write observation, not execution authority.
type OperationWorkflowReadinessCompletionEvidence struct {
	CheckedAt                time.Time `json:"checked_at"`
	Status                   string    `json:"status"`
	Explanation              string    `json:"explanation"`
	ReportingOperationID     string    `json:"reporting_operation_id,omitempty"`
	StateRevision            int64     `json:"state_revision,omitempty"`
	EvidenceComplete         bool      `json:"evidence_complete"`
	RemainingReasons         []string  `json:"remaining_reasons"`
	PendingVerificationCount int64     `json:"pending_verification_count"`
	Warnings                 []string  `json:"warnings"`
}

// Watches follow captured resolution identities within the source inbox retention window.
type OperationWorkflowReadinessVerificationWatch struct {
	ID                  string                                            `json:"id"`
	Revision            int64                                             `json:"revision"`
	StartedAt           time.Time                                         `json:"started_at"`
	EvaluatedAt         time.Time                                         `json:"evaluated_at"`
	StateRevision       int64                                             `json:"state_revision"`
	ContractVersion     int                                               `json:"contract_version"`
	PendingCountAtStart int64                                             `json:"pending_count_at_start"`
	CoverageIncomplete  bool                                              `json:"coverage_incomplete"`
	Items               []OperationWorkflowReadinessVerificationWatchItem `json:"items"`
}
type OperationWorkflowReadinessVerificationWatchItem struct {
	BlockerActive        bool                                    `json:"blocker_active"`
	Key                  string                                  `json:"key"`
	Status               string                                  `json:"status"`
	Explanation          string                                  `json:"explanation"`
	ReportingRevision    int64                                   `json:"reporting_revision,omitempty"`
	ReportingOperationID string                                  `json:"reporting_operation_id,omitempty"`
	Proof                OperationWorkflowResolutionVerification `json:"proof"`
}
type OperationWorkflowReadinessVerificationChange struct {
	BeforeStatus string                                          `json:"before_status"`
	After        OperationWorkflowReadinessVerificationWatchItem `json:"after"`
}
type OperationWorkflowReadinessVerificationEvent struct {
	WatchID              string                                         `json:"watch_id"`
	SourceNotificationID string                                         `json:"source_notification_id"`
	SourceSequence       int64                                          `json:"source_sequence"`
	CheckedAt            time.Time                                      `json:"checked_at"`
	Source               OperationWorkflowReadinessSnapshotEntry        `json:"source"`
	Changes              []OperationWorkflowReadinessVerificationChange `json:"changes"`
}

// BusinessOutcome compares an asserted target with scoped application reports, never coordination status.
type OperationWorkflowReadinessBusinessOutcome struct {
	ExpectedOutcomeCode        string     `json:"expected_outcome_code"`
	CheckedAt                  time.Time  `json:"checked_at"`
	Status                     string     `json:"status"`
	Explanation                string     `json:"explanation"`
	ReportingOperationID       string     `json:"reporting_operation_id,omitempty"`
	ReportID                   string     `json:"report_id,omitempty"`
	StateRevision              int64      `json:"state_revision,omitempty"`
	ContractVersion            int        `json:"contract_version,omitempty"`
	State                      string     `json:"state,omitempty"`
	ReportedOutcomeCode        string     `json:"reported_outcome_code,omitempty"`
	ReportedOutcomeDescription string     `json:"reported_outcome_description,omitempty"`
	OccurredAt                 *time.Time `json:"occurred_at,omitempty"`
}

// Outcome watches are bounded by the source inbox retention and never execute business operations.
type OperationWorkflowReadinessBusinessOutcomeWatch struct {
	Escalation         *OperationWorkflowReadinessOutcomeEscalationReference `json:"escalation,omitempty"`
	EscalationPolicy   *OperationWorkflowReadinessOutcomeEscalationPolicy    `json:"escalation_policy,omitempty"`
	StateRevision      int64                                                 `json:"state_revision"`
	ID                 string                                                `json:"id"`
	Revision           int64                                                 `json:"revision"`
	StartedAt          time.Time                                             `json:"started_at"`
	DeadlineAt         *time.Time                                            `json:"deadline_at,omitempty"`
	DeadlineNotified   bool                                                  `json:"deadline_notified"`
	LastNotifiedStatus string                                                `json:"last_notified_status,omitempty"`
	Current            OperationWorkflowReadinessBusinessOutcome             `json:"current"`
}
type OperationWorkflowReadinessBusinessOutcomeEvent struct {
	Escalation           *OperationWorkflowReadinessOutcomeEscalationReference `json:"escalation,omitempty"`
	WatchID              string                                                `json:"watch_id"`
	SourceNotificationID string                                                `json:"source_notification_id"`
	SourceSequence       int64                                                 `json:"source_sequence"`
	Source               OperationWorkflowReadinessSnapshotEntry               `json:"source"`
	PreviousStatus       string                                                `json:"previous_status"`
	DeadlineAt           *time.Time                                            `json:"deadline_at,omitempty"`
	Current              OperationWorkflowReadinessBusinessOutcome             `json:"current"`
}

// Escalation policy schedules coordination only; owner is an existing team/person label.
type OperationWorkflowReadinessOutcomeEscalationPolicy struct {
	AcknowledgeAfterSeconds int64  `json:"acknowledge_after_seconds,omitempty"`
	FallbackOwner           string `json:"fallback_owner,omitempty"`
	Owner                   string `json:"owner"`
	Note                    string `json:"note,omitempty"`
	FollowUpAfterSeconds    int64  `json:"follow_up_after_seconds"`
}
type OperationWorkflowReadinessOutcomeEscalationReference struct {
	NotificationID string    `json:"notification_id"`
	Sequence       int64     `json:"sequence"`
	CreatedAt      time.Time `json:"created_at"`
}

// Current is watch evidence, separate from immutable delivery evidence and human coordination.
type OperationWorkflowReadinessOutcomeEscalation struct {
	Response             *OperationWorkflowReadinessEscalationResponse `json:"response,omitempty"`
	WatchID              string                                        `json:"watch_id"`
	SourceNotificationID string                                        `json:"source_notification_id"`
	SourceSequence       int64                                         `json:"source_sequence"`
	Current              OperationWorkflowReadinessBusinessOutcome     `json:"current"`
	SuccessObservedAt    *time.Time                                    `json:"success_observed_at,omitempty"`
}

// Response targets govern inbox coordination independently of business evidence availability.
type OperationWorkflowReadinessEscalationResponse struct {
	DeadlineAt          time.Time                                    `json:"deadline_at"`
	InitialOwner        string                                       `json:"initial_owner"`
	FallbackOwner       string                                       `json:"fallback_owner,omitempty"`
	Status              string                                       `json:"status"`
	FirstAcknowledgedAt *time.Time                                   `json:"first_acknowledged_at,omitempty"`
	DeadlineNotifiedAt  *time.Time                                   `json:"deadline_notified_at,omitempty"`
	History             []OperationWorkflowReadinessAssignmentChange `json:"history"`
	HistoryTruncated    bool                                         `json:"history_truncated"`
}
type OperationWorkflowReadinessAssignmentChange struct {
	FromOwner string    `json:"from_owner"`
	ToOwner   string    `json:"to_owner"`
	At        time.Time `json:"at"`
	Actor     string    `json:"actor"`
	Reason    string    `json:"reason"`
}
type OperationWorkflowReadinessEscalationResponseEvent struct {
	BusinessImpact           *OperationWorkflowReadinessImpactEvent   `json:"business_impact,omitempty"`
	EscalationNotificationID string                                   `json:"escalation_notification_id"`
	EscalationSequence       int64                                    `json:"escalation_sequence"`
	Source                   *OperationWorkflowReadinessSnapshotEntry `json:"source,omitempty"`
	Playbook                 *OperationWorkflowPlaybookEvidenceEvent  `json:"playbook,omitempty"`
	CheckedAt                time.Time                                `json:"checked_at"`
	DeadlineAt               time.Time                                `json:"deadline_at"`
	Owner                    string                                   `json:"owner"`
	Reassigned               bool                                     `json:"reassigned"`
}

// Timeline is a live projection of retained inbox observations, not a complete workflow audit.
type OperationWorkflowCoordinationTimelineOptions struct {
	AppID, Scope, TenantID, SubjectType, SubjectID, Workflow, InstanceID string
	Offset, Limit                                                        int
}
type OperationWorkflowCoordinationTimelineEvent struct {
	PlaybookProgressID    string     `json:"playbook_progress_id,omitempty"`
	ID                    string     `json:"id"`
	At                    time.Time  `json:"at"`
	Kind                  string     `json:"kind"`
	Summary               string     `json:"summary"`
	SubscriptionID        string     `json:"subscription_id,omitempty"`
	SubscriptionName      string     `json:"subscription_name,omitempty"`
	SubscriptionTenantID  string     `json:"subscription_tenant_id,omitempty"`
	NotificationID        string     `json:"notification_id,omitempty"`
	Sequence              int64      `json:"sequence,omitempty"`
	Actor                 string     `json:"actor,omitempty"`
	Owner                 string     `json:"owner,omitempty"`
	Status                string     `json:"status,omitempty"`
	Note                  string     `json:"note,omitempty"`
	FollowUpAt            *time.Time `json:"follow_up_at,omitempty"`
	ExpectedOutcomeCode   string     `json:"expected_outcome_code,omitempty"`
	ReportedOutcomeCode   string     `json:"reported_outcome_code,omitempty"`
	StateRevision         int64      `json:"state_revision,omitempty"`
	ReportingOperationIDs []string   `json:"reporting_operation_ids"`
}
type OperationWorkflowCoordinationTimelineSource struct {
	SubscriptionID    string `json:"subscription_id"`
	Name              string `json:"name"`
	EarliestSequence  int64  `json:"earliest_sequence"`
	LatestSequence    int64  `json:"latest_sequence"`
	RetainedCount     int    `json:"retained_count"`
	GapBefore         bool   `json:"gap_before"`
	MissingDuringRead bool   `json:"missing_during_read"`
}
type OperationWorkflowCoordinationTimeline struct {
	Items            []OperationWorkflowCoordinationTimelineEvent  `json:"items"`
	Sources          []OperationWorkflowCoordinationTimelineSource `json:"sources"`
	Subject          OperationSubject                              `json:"subject"`
	Workflow         string                                        `json:"workflow"`
	InstanceID       string                                        `json:"instance_id"`
	Scope            string                                        `json:"scope"`
	PlatformTenantID string                                        `json:"platform_tenant_id,omitempty"`
	EvaluatedAt      time.Time                                     `json:"evaluated_at"`
	TotalCount       int                                           `json:"total_count"`
	NextOffset       int                                           `json:"next_offset"`
	HasMore          bool                                          `json:"has_more"`
	RetentionLimited bool                                          `json:"retention_limited"`
	SourcesTruncated bool                                          `json:"sources_truncated"`
	HistoryTruncated bool                                          `json:"history_truncated"`
	Limitations      []string                                      `json:"limitations"`
}

// Case suggestions are advisory; the application retains business interpretation and execution authority.
type OperationWorkflowCaseAction struct {
	Kind           string `json:"kind"`
	Summary        string `json:"summary"`
	Owner          string `json:"owner,omitempty"`
	OperationID    string `json:"operation_id,omitempty"`
	SubscriptionID string `json:"subscription_id,omitempty"`
	Sequence       int64  `json:"sequence,omitempty"`
}
type OperationWorkflowCase struct {
	PlaybookProgress      []OperationWorkflowPlaybookProgress   `json:"playbook_progress"`
	Guidance              []OperationWorkflowPlaybookGuidance   `json:"guidance"`
	GuidanceStatus        string                                `json:"guidance_status"`
	GuidanceTruncated     bool                                  `json:"guidance_truncated"`
	Status                string                                `json:"status"`
	Summary               string                                `json:"summary"`
	Instance              *OperationWorkflowInstanceSnapshot    `json:"instance,omitempty"`
	FollowUps             []OperationWorkflowReadinessFollowUp  `json:"follow_ups"`
	FollowUpCount         int                                   `json:"follow_up_count"`
	OverdueFollowUpCount  int                                   `json:"overdue_follow_up_count"`
	FollowUpsTruncated    bool                                  `json:"follow_ups_truncated"`
	Actions               []OperationWorkflowCaseAction         `json:"actions"`
	ActionsTruncated      bool                                  `json:"actions_truncated"`
	Timeline              OperationWorkflowCoordinationTimeline `json:"timeline"`
	EvidenceCheckedAt     time.Time                             `json:"evidence_checked_at"`
	CoordinationCheckedAt time.Time                             `json:"coordination_checked_at"`
	EvaluatedAt           time.Time                             `json:"evaluated_at"`
	Limitations           []string                              `json:"limitations"`
}

// Impact summaries describe retained unresolved human reviews, independently of business success.
type OperationWorkflowReadinessImpactSummary struct {
	Workflow             string                                  `json:"workflow"`
	Kind                 string                                  `json:"kind"`
	Code                 string                                  `json:"code"`
	ReviewCount          int                                     `json:"review_count"`
	TaskCount            int                                     `json:"task_count"`
	CaseCount            int                                     `json:"case_count"`
	CriticalCount        int                                     `json:"critical_count"`
	HighCount            int                                     `json:"high_count"`
	OverdueCount         int                                     `json:"overdue_count"`
	CriticalOverdueCount int                                     `json:"critical_overdue_count"`
	Owners               []OperationWorkflowReadinessImpactOwner `json:"owners"`
}
type OperationWorkflowReadinessImpactOwner struct {
	Owner        string `json:"owner"`
	TaskCount    int    `json:"task_count"`
	OverdueCount int    `json:"overdue_count"`
}

// Impact watches observe retained review queues; configuration is fixed until subscription deletion.
type OperationWorkflowReadinessImpactWatchRequest struct {
	AcknowledgeAfterSeconds int64  `json:"acknowledge_after_seconds,omitempty"`
	FallbackOwner           string `json:"fallback_owner,omitempty"`
	Workflow                string `json:"workflow"`
	Kind                    string `json:"kind"`
	Code                    string `json:"code"`
	Owner                   string `json:"owner,omitempty"`
}
type OperationWorkflowReadinessImpactWatch struct {
	AcknowledgeAfterSeconds int64                                    `json:"acknowledge_after_seconds,omitempty"`
	FallbackOwner           string                                   `json:"fallback_owner,omitempty"`
	Workflow                string                                   `json:"workflow"`
	Kind                    string                                   `json:"kind"`
	Code                    string                                   `json:"code"`
	Owner                   string                                   `json:"owner,omitempty"`
	Current                 *OperationWorkflowReadinessImpactSummary `json:"current,omitempty"`
	EvaluatedAt             time.Time                                `json:"evaluated_at"`
	CoverageIncomplete      bool                                     `json:"coverage_incomplete"`
}
type OperationWorkflowReadinessImpactEvent struct {
	Response  *OperationWorkflowReadinessEscalationResponse `json:"response,omitempty"`
	Workflow  string                                        `json:"workflow"`
	Kind      string                                        `json:"kind"`
	Code      string                                        `json:"code"`
	CheckedAt time.Time                                     `json:"checked_at"`
	Reasons   []string                                      `json:"reasons"`
	Previous  *OperationWorkflowReadinessImpactSummary      `json:"previous,omitempty"`
	Current   *OperationWorkflowReadinessImpactSummary      `json:"current,omitempty"`
}

// Review response ownership is explicit human follow-through, separate from alert acknowledgment.
type OperationWorkflowReviewResponseRequest struct {
	FallbackOwner *string                                      `json:"fallback_owner,omitempty"`
	Owner         string                                       `json:"owner"`
	Plan          string                                       `json:"plan"`
	Status        string                                       `json:"status"`
	CheckInAt     *time.Time                                   `json:"check_in_at,omitempty"`
	Reviews       []OperationWorkflowReviewResponseLinkRequest `json:"reviews,omitempty"`
}
type OperationWorkflowReviewResponseLinkRequest struct {
	SubscriptionID string `json:"subscription_id"`
	Sequence       int64  `json:"sequence"`
}
type OperationWorkflowReviewResponseLink struct {
	SubscriptionID           string           `json:"subscription_id"`
	Sequence                 int64            `json:"sequence"`
	NotificationID           string           `json:"notification_id"`
	ProgressID               string           `json:"progress_id"`
	OutcomeCode              string           `json:"outcome_code"`
	ItemIndex                int              `json:"item_index"`
	DependencyReviewRevision int64            `json:"dependency_review_revision"`
	PlatformTenantID         string           `json:"platform_tenant_id,omitempty"`
	Subject                  OperationSubject `json:"subject"`
	Workflow                 string           `json:"workflow"`
	InstanceID               string           `json:"instance_id"`
}
type OperationWorkflowReviewResponse struct {
	FallbackOwner       string                                  `json:"fallback_owner,omitempty"`
	CheckInInitialOwner string                                  `json:"check_in_initial_owner,omitempty"`
	CheckInOwnerChanged bool                                    `json:"check_in_owner_changed,omitempty"`
	CheckInNotifiedAt   *time.Time                              `json:"check_in_notified_at,omitempty"`
	Owner               string                                  `json:"owner"`
	Plan                string                                  `json:"plan"`
	Status              string                                  `json:"status"`
	CheckInAt           *time.Time                              `json:"check_in_at,omitempty"`
	Reviews             []OperationWorkflowReviewResponseLink   `json:"reviews"`
	CreatedAt           time.Time                               `json:"created_at"`
	CreatedBy           string                                  `json:"created_by"`
	UpdatedAt           time.Time                               `json:"updated_at"`
	UpdatedBy           string                                  `json:"updated_by"`
	ResolutionCheckedAt *time.Time                              `json:"resolution_checked_at,omitempty"`
	History             []OperationWorkflowReviewResponseChange `json:"history"`
	HistoryTruncated    bool                                    `json:"history_truncated"`
}
type OperationWorkflowReviewResponseChange struct {
	FallbackOwner string     `json:"fallback_owner,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	Owner         string     `json:"owner"`
	Plan          string     `json:"plan"`
	Status        string     `json:"status"`
	CheckInAt     *time.Time `json:"check_in_at,omitempty"`
	At            time.Time  `json:"at"`
	Actor         string     `json:"actor"`
}

// Check-in events preserve the original deadline and response snapshot at escalation.
type OperationWorkflowReviewResponseCheckInEvent struct {
	SourceNotificationID string                          `json:"source_notification_id"`
	SourceSequence       int64                           `json:"source_sequence"`
	CheckedAt            time.Time                       `json:"checked_at"`
	CheckInAt            time.Time                       `json:"check_in_at"`
	PreviousOwner        string                          `json:"previous_owner"`
	Owner                string                          `json:"owner"`
	Reassigned           bool                            `json:"reassigned"`
	Response             OperationWorkflowReviewResponse `json:"response"`
}

// Blockage events report sampled changes, without deciding business success.
type OperationWorkflowReviewResponseBlockageEvent struct {
	SourceNotificationID string    `json:"source_notification_id"`
	SourceSequence       int64     `json:"source_sequence"`
	CheckedAt            time.Time `json:"checked_at"`
	AddedBlockers        []string  `json:"added_blockers"`
}
