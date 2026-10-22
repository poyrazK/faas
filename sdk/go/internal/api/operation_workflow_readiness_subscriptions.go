package api

import "time"

// Readiness subscriptions discover bounded current edges and compare saved observations.
// They produce advisory inbox notifications, never execution authority.
type OperationWorkflowReadinessSubscriptionRequest struct {
	NextActionDigest      *OperationWorkflowNextActionDigestPolicy       `json:"next_action_digest,omitempty"`
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
	DigestNextAt            *time.Time                               `json:"digest_next_at,omitempty"`
	DigestEvaluatedAt       *time.Time                               `json:"digest_evaluated_at,omitempty"`
	DigestDeliveredAt       *time.Time                               `json:"digest_delivered_at,omitempty"`
	NextActionDigest        *OperationWorkflowNextActionDigestPolicy `json:"next_action_digest,omitempty"`
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
	FulfillmentReviewEscalation *OperationWorkflowFulfillmentReviewEscalation            `json:"fulfillment_review_escalation,omitempty"`
	FulfillmentReviewReminder   *OperationWorkflowFulfillmentReviewReminder              `json:"fulfillment_review_reminder,omitempty"`
	FulfillmentReview           *OperationWorkflowObligationFulfillmentReview            `json:"fulfillment_review,omitempty"`
	ObligationReopening         *OperationWorkflowObligationReopening                    `json:"obligation_reopening,omitempty"`
	TargetAssessment            *OperationWorkflowObligationAssessment                   `json:"target_assessment,omitempty"`
	ObligationReceipts          []OperationWorkflowObligationEvent                       `json:"obligation_receipts,omitempty"`
	ObligationReceiptsTruncated bool                                                     `json:"obligation_receipts_truncated,omitempty"`
	NextActionDigest            *OperationWorkflowNextActionDigest                       `json:"next_action_digest,omitempty"`
	HandoffReassignment         *OperationWorkflowReviewResponseHandoffReassignmentEvent `json:"handoff_reassignment,omitempty"`
	HandoffDecline              *OperationWorkflowReviewResponseHandoffDeclineEvent      `json:"handoff_decline,omitempty"`
	DecisionReview              *OperationWorkflowReviewResponseDecisionReviewEvent      `json:"decision_review,omitempty"`
	ReviewBlockage              *OperationWorkflowReviewResponseBlockageEvent            `json:"review_blockage,omitempty"`
	ReviewCheckIn               *OperationWorkflowReviewResponseCheckInEvent             `json:"review_check_in,omitempty"`
	ReviewResponse              *OperationWorkflowReviewResponse                         `json:"review_response,omitempty"`
	BusinessImpact              *OperationWorkflowReadinessImpactEvent                   `json:"business_impact,omitempty"`
	DependencyRecovery          *OperationWorkflowPlaybookDependencyRecovery             `json:"dependency_recovery,omitempty"`
	DependencyRegression        *OperationWorkflowPlaybookDependencyRegression           `json:"dependency_regression,omitempty"`
	PlaybookStep                *OperationWorkflowPlaybookStepAssignment                 `json:"playbook_step,omitempty"`
	PlaybookReview              *OperationWorkflowPlaybookCompletionReview               `json:"playbook_review,omitempty"`
	PlaybookReviewEvent         *OperationWorkflowPlaybookCompletionReviewEvent          `json:"playbook_review_event,omitempty"`
	PlaybookEscalation          *OperationWorkflowPlaybookOutcomeEscalation              `json:"playbook_escalation,omitempty"`
	PlaybookEvidence            *OperationWorkflowPlaybookEvidenceEvent                  `json:"playbook_evidence,omitempty"`
	EscalationResponse          *OperationWorkflowReadinessEscalationResponseEvent       `json:"escalation_response,omitempty"`
	OutcomeEscalation           *OperationWorkflowReadinessOutcomeEscalation             `json:"outcome_escalation,omitempty"`
	BusinessOutcomeWatch        *OperationWorkflowReadinessBusinessOutcomeWatch          `json:"business_outcome_watch,omitempty"`
	BusinessOutcome             *OperationWorkflowReadinessBusinessOutcomeEvent          `json:"business_outcome,omitempty"`
	VerificationWatch           *OperationWorkflowReadinessVerificationWatch             `json:"verification_watch,omitempty"`
	Verification                *OperationWorkflowReadinessVerificationEvent             `json:"verification,omitempty"`
	Reminder                    *OperationWorkflowReadinessDueReminder                   `json:"reminder,omitempty"`
	Coordination                *OperationWorkflowReadinessCoordination                  `json:"coordination,omitempty"`
	ID                          string                                                   `json:"id"`
	Sequence                    int64                                                    `json:"sequence"`
	Kind                        string                                                   `json:"kind"`
	CreatedAt                   time.Time                                                `json:"created_at"`
	Explanation                 string                                                   `json:"explanation"`
	Change                      *OperationWorkflowReadinessChange                        `json:"change,omitempty"`
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
	FulfillmentReviewEscalations []OperationWorkflowFulfillmentReviewEscalation         `json:"fulfillment_review_escalations,omitempty"`
	FulfillmentReviewReminders   []OperationWorkflowFulfillmentReviewReminder           `json:"fulfillment_review_reminders,omitempty"`
	ReviewCoordinationChanges    []OperationWorkflowFulfillmentReviewCoordinationChange `json:"review_coordination_changes,omitempty"`
	FulfillmentReviews           []OperationWorkflowObligationFulfillmentReview         `json:"fulfillment_reviews,omitempty"`
	Reaffirmations               []OperationWorkflowObligationReaffirmation             `json:"reaffirmations,omitempty"`
	Reopenings                   []OperationWorkflowObligationReopening                 `json:"reopenings,omitempty"`
	Fulfillments                 []OperationWorkflowObligationFulfillment               `json:"fulfillments,omitempty"`
	TargetBindings               []OperationWorkflowObligationTargetBinding             `json:"target_bindings,omitempty"`
	Completion                   *OperationWorkflowReadinessCompletion                  `json:"completion,omitempty"`
	Revision                     int64                                                  `json:"revision"`
	Status                       string                                                 `json:"status"`
	Owner                        string                                                 `json:"owner"`
	Note                         string                                                 `json:"note"`
	FollowUpAt                   *time.Time                                             `json:"follow_up_at,omitempty"`
	UpdatedAt                    time.Time                                              `json:"updated_at"`
	UpdatedBy                    string                                                 `json:"updated_by"`
	AcknowledgedAt               *time.Time                                             `json:"acknowledged_at,omitempty"`
	AcknowledgedBy               string                                                 `json:"acknowledged_by,omitempty"`
}
type OperationWorkflowReadinessCoordinationRequest struct {
	ReviewCoordination *OperationWorkflowFulfillmentReviewCoordinationRequest `json:"review_coordination,omitempty"`
	Reaffirmation      *OperationWorkflowObligationReaffirmationRequest       `json:"reaffirmation,omitempty"`
	Reopening          *OperationWorkflowObligationReopeningRequest           `json:"reopening,omitempty"`
	Fulfillment        *OperationWorkflowObligationFulfillmentRequest         `json:"fulfillment,omitempty"`
	TargetBinding      *OperationWorkflowObligationTargetRequest              `json:"target_binding,omitempty"`
	ReviewResponse     *OperationWorkflowReviewResponseRequest                `json:"review_response,omitempty"`
	Completion         *OperationWorkflowReadinessCompletionRequest           `json:"completion,omitempty"`
	Revision           int64                                                  `json:"revision"`
	Status             string                                                 `json:"status"`
	Owner              string                                                 `json:"owner"`
	Note               string                                                 `json:"note"`
	FollowUpAt         *time.Time                                             `json:"follow_up_at,omitempty"`
}

// Handoff summarizes retained application evidence; it never authorizes execution.
type OperationWorkflowReadinessHandoff struct {
	Summary             string     `json:"summary"`
	BaselineEvaluatedAt *time.Time `json:"baseline_evaluated_at,omitempty"`
	Comparable          bool       `json:"comparable"`
	Limitations         []string   `json:"limitations"`
}
type OperationWorkflowReadinessNotificationCheck struct {
	NextActionDigest *OperationWorkflowNextActionDigestCheck    `json:"next_action_digest,omitempty"`
	BusinessOutcome  *OperationWorkflowReadinessBusinessOutcome `json:"business_outcome,omitempty"`
	Handoff          OperationWorkflowReadinessHandoff          `json:"handoff"`
	Notification     OperationWorkflowReadinessNotification     `json:"notification"`
	CheckedAt        time.Time                                  `json:"checked_at"`
	Status           string                                     `json:"status"`
	Change           *OperationWorkflowReadinessChange          `json:"change,omitempty"`
}

// Follow-up queues aggregate retained coordination, not application execution tasks.
type OperationWorkflowReadinessFollowUpOptions struct {
	View           string `json:"view,omitempty"`
	ActionKind     string `json:"action_kind,omitempty"`
	ActionTrigger  string `json:"action_trigger,omitempty"`
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
	AttentionDueAt       *time.Time                                   `json:"attention_due_at,omitempty"`
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
	View                  string                                    `json:"view,omitempty"`
	NextActions           []OperationWorkflowNextActionQueueItem    `json:"next_actions,omitempty"`
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
	ObligationContext          *OperationWorkflowObligationContext          `json:"obligation_context,omitempty"`
	ObligationAssessments      []OperationWorkflowObligationAssessment      `json:"obligation_assessments"`
	ObligationHistory          []OperationWorkflowCaseObligationHistoryItem `json:"obligation_history"`
	ObligationHistoryCount     int                                          `json:"obligation_history_count"`
	ObligationHistoryTruncated bool                                         `json:"obligation_history_truncated"`
	ObligationHistoryLimited   bool                                         `json:"obligation_history_limited"`
	ObligationHistoryCheckedAt time.Time                                    `json:"obligation_history_checked_at"`
	NextActions                []OperationWorkflowCaseNextAction            `json:"next_actions"`
	NextActionCount            int                                          `json:"next_action_count"`
	NextActionsTruncated       bool                                         `json:"next_actions_truncated"`
	PlaybookProgress           []OperationWorkflowPlaybookProgress          `json:"playbook_progress"`
	Guidance                   []OperationWorkflowPlaybookGuidance          `json:"guidance"`
	GuidanceStatus             string                                       `json:"guidance_status"`
	GuidanceTruncated          bool                                         `json:"guidance_truncated"`
	Status                     string                                       `json:"status"`
	Summary                    string                                       `json:"summary"`
	Instance                   *OperationWorkflowInstanceSnapshot           `json:"instance,omitempty"`
	FollowUps                  []OperationWorkflowReadinessFollowUp         `json:"follow_ups"`
	FollowUpCount              int                                          `json:"follow_up_count"`
	OverdueFollowUpCount       int                                          `json:"overdue_follow_up_count"`
	FollowUpsTruncated         bool                                         `json:"follow_ups_truncated"`
	Actions                    []OperationWorkflowCaseAction                `json:"actions"`
	ActionsTruncated           bool                                         `json:"actions_truncated"`
	Timeline                   OperationWorkflowCoordinationTimeline        `json:"timeline"`
	EvidenceCheckedAt          time.Time                                    `json:"evidence_checked_at"`
	CoordinationCheckedAt      time.Time                                    `json:"coordination_checked_at"`
	EvaluatedAt                time.Time                                    `json:"evaluated_at"`
	Limitations                []string                                     `json:"limitations"`
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
	ScheduledReviewReschedule        *OperationWorkflowReviewResponseScheduledReviewRescheduleRequest   `json:"scheduled_review_reschedule,omitempty"`
	ScheduledReviewCancellation      *OperationWorkflowReviewResponseScheduledReviewCancellationRequest `json:"scheduled_review_cancellation,omitempty"`
	EscalationActionReview           *OperationWorkflowReviewResponseEscalationActionReviewRequest      `json:"escalation_action_review,omitempty"`
	EscalationRevision               *OperationWorkflowReviewResponseEscalationRevisionRequest          `json:"escalation_revision,omitempty"`
	EscalationCompletion             *OperationWorkflowReviewResponseEscalationCompletionRequest        `json:"escalation_completion,omitempty"`
	EscalationAcknowledgment         *OperationWorkflowReviewResponseEscalationAcknowledgmentRequest    `json:"escalation_acknowledgment,omitempty"`
	HandoffAcceptanceEscalationOwner *string                                                            `json:"handoff_acceptance_escalation_owner,omitempty"`
	HandoffReassignment              *OperationWorkflowReviewResponseHandoffReassignmentRequest         `json:"handoff_reassignment,omitempty"`
	HandoffDecline                   *OperationWorkflowReviewResponseHandoffDeclineRequest              `json:"handoff_decline,omitempty"`
	HandoffAcceptAfterSeconds        *int64                                                             `json:"handoff_accept_after_seconds,omitempty"`
	HandoffAcceptance                *OperationWorkflowReviewResponseHandoffAcceptanceRequest           `json:"handoff_acceptance,omitempty"`
	DecisionReviewFallbackOwner      *string                                                            `json:"decision_review_fallback_owner,omitempty"`
	DecisionReviewAfterSeconds       *int64                                                             `json:"decision_review_after_seconds,omitempty"`
	Decision                         *OperationWorkflowReviewResponseDecisionRequest                    `json:"decision,omitempty"`
	FallbackOwner                    *string                                                            `json:"fallback_owner,omitempty"`
	Owner                            string                                                             `json:"owner"`
	Plan                             string                                                             `json:"plan"`
	Status                           string                                                             `json:"status"`
	CheckInAt                        *time.Time                                                         `json:"check_in_at,omitempty"`
	Reviews                          []OperationWorkflowReviewResponseLinkRequest                       `json:"reviews,omitempty"`
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
	HandoffAcceptanceEscalationOwner string                                         `json:"handoff_acceptance_escalation_owner,omitempty"`
	HandoffAcceptAfterSeconds        int64                                          `json:"handoff_accept_after_seconds,omitempty"`
	DecisionReviewFallbackOwner      string                                         `json:"decision_review_fallback_owner,omitempty"`
	DecisionReviewAfterSeconds       int64                                          `json:"decision_review_after_seconds,omitempty"`
	DecisionReview                   *OperationWorkflowReviewResponseDecisionReview `json:"decision_review,omitempty"`
	Decision                         *OperationWorkflowReviewResponseDecision       `json:"decision,omitempty"`
	Health                           *OperationWorkflowReviewResponseHealth         `json:"health,omitempty"`
	FallbackOwner                    string                                         `json:"fallback_owner,omitempty"`
	CheckInInitialOwner              string                                         `json:"check_in_initial_owner,omitempty"`
	CheckInOwnerChanged              bool                                           `json:"check_in_owner_changed,omitempty"`
	CheckInNotifiedAt                *time.Time                                     `json:"check_in_notified_at,omitempty"`
	Owner                            string                                         `json:"owner"`
	Plan                             string                                         `json:"plan"`
	Status                           string                                         `json:"status"`
	CheckInAt                        *time.Time                                     `json:"check_in_at,omitempty"`
	Reviews                          []OperationWorkflowReviewResponseLink          `json:"reviews"`
	CreatedAt                        time.Time                                      `json:"created_at"`
	CreatedBy                        string                                         `json:"created_by"`
	UpdatedAt                        time.Time                                      `json:"updated_at"`
	UpdatedBy                        string                                         `json:"updated_by"`
	ResolutionCheckedAt              *time.Time                                     `json:"resolution_checked_at,omitempty"`
	History                          []OperationWorkflowReviewResponseChange        `json:"history"`
	HistoryTruncated                 bool                                           `json:"history_truncated"`
}
type OperationWorkflowReviewResponseChange struct {
	ScheduledReviewReschedule        *OperationWorkflowReviewResponseScheduledReviewReschedule   `json:"scheduled_review_reschedule,omitempty"`
	ScheduledReviewCancellation      *OperationWorkflowReviewResponseScheduledReviewCancellation `json:"scheduled_review_cancellation,omitempty"`
	EscalationActionReview           *OperationWorkflowReviewResponseEscalationActionReview      `json:"escalation_action_review,omitempty"`
	EscalationCompletion             *OperationWorkflowReviewResponseEscalationCompletion        `json:"escalation_completion,omitempty"`
	EscalationAcknowledgment         *OperationWorkflowReviewResponseEscalationAcknowledgment    `json:"escalation_acknowledgment,omitempty"`
	HandoffAcceptanceEscalationOwner string                                                      `json:"handoff_acceptance_escalation_owner,omitempty"`
	HandoffReassignment              *OperationWorkflowReviewResponseHandoffReassignment         `json:"handoff_reassignment,omitempty"`
	HandoffDecline                   *OperationWorkflowReviewResponseHandoffDecline              `json:"handoff_decline,omitempty"`
	HandoffAcceptAfterSeconds        int64                                                       `json:"handoff_accept_after_seconds,omitempty"`
	HandoffAcceptance                *OperationWorkflowReviewResponseHandoffAcceptance           `json:"handoff_acceptance,omitempty"`
	DecisionReviewFallbackOwner      string                                                      `json:"decision_review_fallback_owner,omitempty"`
	DecisionReviewAfterSeconds       int64                                                       `json:"decision_review_after_seconds,omitempty"`
	Decision                         *OperationWorkflowReviewResponseDecision                    `json:"decision,omitempty"`
	FallbackOwner                    string                                                      `json:"fallback_owner,omitempty"`
	Reason                           string                                                      `json:"reason,omitempty"`
	Owner                            string                                                      `json:"owner"`
	Plan                             string                                                      `json:"plan"`
	Status                           string                                                      `json:"status"`
	CheckInAt                        *time.Time                                                  `json:"check_in_at,omitempty"`
	At                               time.Time                                                   `json:"at"`
	Actor                            string                                                      `json:"actor"`
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
	ClearedBlockers      []string  `json:"cleared_blockers,omitempty"`
}

// Health is a sampled advisory view; clear observations do not establish business success.
type OperationWorkflowReviewResponseHealth struct {
	Fingerprint         string                                        `json:"fingerprint,omitempty"`
	Status              string                                        `json:"status"`
	CheckedAt           time.Time                                     `json:"checked_at"`
	LastCompleteCheckAt *time.Time                                    `json:"last_complete_check_at,omitempty"`
	RemainingBlockers   []string                                      `json:"remaining_blockers"`
	RecoveredBlockers   []string                                      `json:"recovered_blockers"`
	RecoveredAt         *time.Time                                    `json:"recovered_at,omitempty"`
	Sources             []OperationWorkflowReviewResponseSourceHealth `json:"sources"`
}
type OperationWorkflowReviewResponseSourceHealth struct {
	ProgressRevision      int64      `json:"progress_revision,omitempty"`
	StateRevision         int64      `json:"state_revision,omitempty"`
	ReviewIndex           int        `json:"review_index"`
	Status                string     `json:"status"`
	CheckedAt             time.Time  `json:"checked_at"`
	LastSuccessfulCheckAt *time.Time `json:"last_successful_check_at,omitempty"`
	ReviewRequired        bool       `json:"review_required"`
	Blockers              []string   `json:"blockers"`
	Explanation           string     `json:"explanation"`
}

// Decisions are human intent, not application execution commands.
type OperationWorkflowReviewResponseDecisionRequest struct {
	Kind              string `json:"kind"`
	Reason            string `json:"reason"`
	HealthFingerprint string `json:"health_fingerprint"`
}
type OperationWorkflowReviewResponseDecision struct {
	Kind   string                                `json:"kind"`
	Reason string                                `json:"reason"`
	Owner  string                                `json:"owner"`
	At     time.Time                             `json:"at"`
	Actor  string                                `json:"actor"`
	Health OperationWorkflowReviewResponseHealth `json:"health"`
}

// Review requirements remain latched until an explicit decision replaces the reviewed intent.
type OperationWorkflowReviewResponseDecisionReview struct {
	EscalationAcknowledgment     *OperationWorkflowReviewResponseEscalationAcknowledgment `json:"escalation_acknowledgment,omitempty"`
	AcceptanceEscalationOwner    string                                                   `json:"acceptance_escalation_owner,omitempty"`
	AcceptanceEscalatedAt        *time.Time                                               `json:"acceptance_escalated_at,omitempty"`
	HandedOffAt                  *time.Time                                               `json:"handed_off_at,omitempty"`
	Reassignment                 *OperationWorkflowReviewResponseHandoffReassignment      `json:"reassignment,omitempty"`
	Decline                      *OperationWorkflowReviewResponseHandoffDecline           `json:"decline,omitempty"`
	ReassignmentRequired         bool                                                     `json:"reassignment_required,omitempty"`
	AcceptanceDueAt              *time.Time                                               `json:"acceptance_due_at,omitempty"`
	AcceptanceDeadlineNotifiedAt *time.Time                                               `json:"acceptance_deadline_notified_at,omitempty"`
	Acceptance                   *OperationWorkflowReviewResponseHandoffAcceptance        `json:"acceptance,omitempty"`
	InitialOwner                 string                                                   `json:"initial_owner,omitempty"`
	OwnerChanged                 bool                                                     `json:"owner_changed,omitempty"`
	FallbackOwner                string                                                   `json:"fallback_owner,omitempty"`
	PreviousOwner                string                                                   `json:"previous_owner,omitempty"`
	Owner                        string                                                   `json:"owner,omitempty"`
	Reassigned                   bool                                                     `json:"reassigned,omitempty"`
	DueAt                        *time.Time                                               `json:"due_at,omitempty"`
	DeadlineNotifiedAt           *time.Time                                               `json:"deadline_notified_at,omitempty"`
	DecisionAt                   time.Time                                                `json:"decision_at"`
	RequiredAt                   time.Time                                                `json:"required_at"`
	Changes                      []string                                                 `json:"changes"`
	Health                       OperationWorkflowReviewResponseHealth                    `json:"health"`
}
type OperationWorkflowReviewResponseDecisionReviewEvent struct {
	SourceNotificationID string                                        `json:"source_notification_id"`
	SourceSequence       int64                                         `json:"source_sequence"`
	Decision             OperationWorkflowReviewResponseDecision       `json:"decision"`
	Review               OperationWorkflowReviewResponseDecisionReview `json:"review"`
}

const OperationWorkflowReviewResponseDecisionReviewDefaultSeconds = 86400

type OperationWorkflowReviewResponseHandoffAcceptanceRequest struct {
	HandedOffAt *time.Time `json:"handed_off_at,omitempty"`
	DecisionAt  time.Time  `json:"decision_at"`
	RequiredAt  time.Time  `json:"required_at"`
	Note        string     `json:"note"`
}
type OperationWorkflowReviewResponseHandoffAcceptance struct {
	Owner     string    `json:"owner"`
	Actor     string    `json:"actor"`
	At        time.Time `json:"at"`
	Note      string    `json:"note"`
	CheckInAt time.Time `json:"check_in_at"`
}

const OperationWorkflowReviewResponseHandoffAcceptDefaultSeconds = 3600

type OperationWorkflowReviewResponseHandoffDeclineRequest struct {
	HandedOffAt *time.Time `json:"handed_off_at,omitempty"`
	DecisionAt  time.Time  `json:"decision_at"`
	RequiredAt  time.Time  `json:"required_at"`
	Reason      string     `json:"reason"`
}
type OperationWorkflowReviewResponseHandoffDecline struct {
	DecisionAt    time.Time  `json:"decision_at"`
	RequiredAt    time.Time  `json:"required_at"`
	Owner         string     `json:"owner"`
	PreviousOwner string     `json:"previous_owner"`
	Actor         string     `json:"actor"`
	At            time.Time  `json:"at"`
	Reason        string     `json:"reason"`
	NotifiedAt    *time.Time `json:"notified_at,omitempty"`
}
type OperationWorkflowReviewResponseHandoffDeclineEvent struct {
	SourceNotificationID string                                        `json:"source_notification_id"`
	SourceSequence       int64                                         `json:"source_sequence"`
	Decline              OperationWorkflowReviewResponseHandoffDecline `json:"decline"`
}

type OperationWorkflowReviewResponseHandoffReassignmentRequest struct {
	DecisionAt time.Time `json:"decision_at"`
	RequiredAt time.Time `json:"required_at"`
	DeclinedAt time.Time `json:"declined_at"`
	Reason     string    `json:"reason"`
}
type OperationWorkflowReviewResponseHandoffReassignment struct {
	DecisionAt    time.Time                                     `json:"decision_at"`
	RequiredAt    time.Time                                     `json:"required_at"`
	Owner         string                                        `json:"owner"`
	PreviousOwner string                                        `json:"previous_owner"`
	Actor         string                                        `json:"actor"`
	At            time.Time                                     `json:"at"`
	Reason        string                                        `json:"reason"`
	Decline       OperationWorkflowReviewResponseHandoffDecline `json:"decline"`
	NotifiedAt    *time.Time                                    `json:"notified_at,omitempty"`
}
type OperationWorkflowReviewResponseHandoffReassignmentEvent struct {
	SourceNotificationID string                                             `json:"source_notification_id"`
	SourceSequence       int64                                              `json:"source_sequence"`
	Reassignment         OperationWorkflowReviewResponseHandoffReassignment `json:"reassignment"`
}

// Escalation coordination does not accept ownership or satisfy decision revalidation.
type OperationWorkflowReviewResponseEscalationAcknowledgmentRequest struct {
	DecisionAt  time.Time `json:"decision_at"`
	RequiredAt  time.Time `json:"required_at"`
	HandedOffAt time.Time `json:"handed_off_at"`
	EscalatedAt time.Time `json:"escalated_at"`
	Coordinator string    `json:"coordinator"`
	Plan        string    `json:"plan"`
	DueAt       time.Time `json:"due_at"`
}
type OperationWorkflowReviewResponseEscalationAcknowledgment struct {
	PreviousCompletedAt *time.Time                                             `json:"previous_completed_at,omitempty"`
	Basis               *OperationWorkflowReviewResponseEscalationActionReview `json:"basis,omitempty"`
	Completion          *OperationWorkflowReviewResponseEscalationCompletion   `json:"completion,omitempty"`
	Coordinator         string                                                 `json:"coordinator"`
	Plan                string                                                 `json:"plan"`
	DueAt               time.Time                                              `json:"due_at"`
	At                  time.Time                                              `json:"at"`
	Actor               string                                                 `json:"actor"`
	OverdueNotifiedAt   *time.Time                                             `json:"overdue_notified_at,omitempty"`
}

// Completion records human coordination work, not an application outcome.
type OperationWorkflowReviewResponseEscalationCompletionRequest struct {
	ReviewAfterSeconds *int64    `json:"review_after_seconds,omitempty"`
	DecisionAt         time.Time `json:"decision_at"`
	RequiredAt         time.Time `json:"required_at"`
	HandedOffAt        time.Time `json:"handed_off_at"`
	EscalatedAt        time.Time `json:"escalated_at"`
	AcknowledgedAt     time.Time `json:"acknowledged_at"`
	Summary            string    `json:"summary"`
	ReviewIndices      []int     `json:"review_indices"`
}
type OperationWorkflowReviewResponseEscalationCompletion struct {
	OriginalReviewDueAt *time.Time                                                  `json:"original_review_due_at,omitempty"`
	ReviewReschedule    *OperationWorkflowReviewResponseScheduledReviewReschedule   `json:"review_reschedule,omitempty"`
	ReviewCancellation  *OperationWorkflowReviewResponseScheduledReviewCancellation `json:"review_cancellation,omitempty"`
	ReviewDueAt         *time.Time                                                  `json:"review_due_at,omitempty"`
	ScheduledReviewAt   *time.Time                                                  `json:"scheduled_review_at,omitempty"`
	Review              *OperationWorkflowReviewResponseEscalationActionReview      `json:"review,omitempty"`
	AcknowledgedAt      time.Time                                                   `json:"acknowledged_at"`
	Summary             string                                                      `json:"summary"`
	Evidence            []OperationWorkflowReviewResponseLink                       `json:"evidence"`
	At                  time.Time                                                   `json:"at"`
	Actor               string                                                      `json:"actor"`
}

type OperationWorkflowReviewResponseEscalationActionReviewRequest struct {
	DecisionAt     time.Time `json:"decision_at"`
	RequiredAt     time.Time `json:"required_at"`
	HandedOffAt    time.Time `json:"handed_off_at"`
	EscalatedAt    time.Time `json:"escalated_at"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
	CompletedAt    time.Time `json:"completed_at"`
}
type OperationWorkflowReviewResponseEscalationRevisionRequest struct {
	OperationWorkflowReviewResponseEscalationActionReviewRequest
	ReviewAt    time.Time `json:"review_at"`
	Coordinator string    `json:"coordinator"`
	Plan        string    `json:"plan"`
	DueAt       time.Time `json:"due_at"`
}
type OperationWorkflowReviewResponseEscalationActionReview struct {
	CompletedAt time.Time                             `json:"completed_at"`
	At          time.Time                             `json:"at"`
	Actor       string                                `json:"actor"`
	Health      OperationWorkflowReviewResponseHealth `json:"health"`
}

type OperationWorkflowReviewResponseScheduledReviewCancellationRequest struct {
	OperationWorkflowReviewResponseEscalationActionReviewRequest
	DueAt  time.Time `json:"due_at"`
	Reason string    `json:"reason"`
}
type OperationWorkflowReviewResponseScheduledReviewCancellation struct {
	CompletedAt time.Time `json:"completed_at"`
	DueAt       time.Time `json:"due_at"`
	Reason      string    `json:"reason"`
	Actor       string    `json:"actor"`
	At          time.Time `json:"at"`
}

type OperationWorkflowReviewResponseScheduledReviewRescheduleRequest struct {
	OperationWorkflowReviewResponseEscalationActionReviewRequest
	DueAt    time.Time `json:"due_at"`
	NewDueAt time.Time `json:"new_due_at"`
	Reason   string    `json:"reason"`
}
type OperationWorkflowReviewResponseScheduledReviewReschedule struct {
	CompletedAt   time.Time `json:"completed_at"`
	PreviousDueAt time.Time `json:"previous_due_at"`
	DueAt         time.Time `json:"due_at"`
	Reason        string    `json:"reason"`
	Actor         string    `json:"actor"`
	At            time.Time `json:"at"`
}

// Current coordination obligations for an exact workflow case, separate from execution permission.
type OperationWorkflowCaseNextAction struct {
	FulfillmentReview *OperationWorkflowObligationFulfillmentReview `json:"fulfillment_review,omitempty"`
	Obligation        *OperationWorkflowActionObligation            `json:"obligation,omitempty"`
	Kind              string                                        `json:"kind"`
	Summary           string                                        `json:"summary"`
	Owner             string                                        `json:"owner,omitempty"`
	Trigger           string                                        `json:"trigger"`
	DueAt             *time.Time                                    `json:"due_at,omitempty"`
	Overdue           bool                                          `json:"overdue"`
	SubscriptionID    string                                        `json:"subscription_id"`
	Sequence          int64                                         `json:"sequence"`
	Evidence          []OperationWorkflowReviewResponseLink         `json:"evidence,omitempty"`
	SharedPlan        bool                                          `json:"shared_plan,omitempty"`
}

type OperationWorkflowNextActionCase struct {
	Subject          OperationSubject `json:"subject"`
	Workflow         string           `json:"workflow"`
	InstanceID       string           `json:"instance_id"`
	PlatformTenantID string           `json:"platform_tenant_id,omitempty"`
}
type OperationWorkflowNextActionQueueItem struct {
	Action               OperationWorkflowCaseNextAction   `json:"action"`
	Cases                []OperationWorkflowNextActionCase `json:"cases"`
	Scope                string                            `json:"scope"`
	SubscriptionTenantID string                            `json:"subscription_tenant_id,omitempty"`
}

type OperationWorkflowNextActionDigestPolicy struct {
	Owner            string `json:"owner"`
	Workflow         string `json:"workflow,omitempty"`
	ActionKind       string `json:"action_kind,omitempty"`
	ActionTrigger    string `json:"action_trigger,omitempty"`
	CadenceSeconds   int64  `json:"cadence_seconds,omitempty"`
	SoonAfterSeconds int64  `json:"soon_after_seconds,omitempty"`
}
type OperationWorkflowNextActionDigestExplanation struct {
	ObligationID   string   `json:"obligation_id,omitempty"`
	SubscriptionID string   `json:"subscription_id"`
	Sequence       int64    `json:"sequence"`
	ActionKind     string   `json:"action_kind"`
	Reasons        []string `json:"reasons"`
	Comparison     string   `json:"comparison"`
	ChangedFields  []string `json:"changed_fields"`
	NextStep       string   `json:"next_step"`
}
type OperationWorkflowNextActionDigest struct {
	Explanations          []OperationWorkflowNextActionDigestExplanation `json:"explanations,omitempty"`
	Items                 []OperationWorkflowNextActionQueueItem         `json:"items"`
	Owner                 string                                         `json:"owner"`
	Scope                 string                                         `json:"scope"`
	EvaluatedAt           time.Time                                      `json:"evaluated_at"`
	SoonUntil             time.Time                                      `json:"soon_until"`
	MatchingCount         int                                            `json:"matching_count"`
	ChangedCount          int                                            `json:"changed_count"`
	RemainingChangedCount int                                            `json:"remaining_changed_count"`
	RetentionLimited      bool                                           `json:"retention_limited"`
}

type OperationWorkflowNextActionDigestCheck struct {
	Items            []OperationWorkflowNextActionDigestCheckItem `json:"items"`
	RetentionLimited bool                                         `json:"retention_limited"`
}
type OperationWorkflowNextActionDigestCheckItem struct {
	ObligationID   string                                `json:"obligation_id,omitempty"`
	Lifecycle      []OperationWorkflowObligationEvent    `json:"lifecycle,omitempty"`
	HistoryLimited bool                                  `json:"history_limited"`
	SubscriptionID string                                `json:"subscription_id"`
	Sequence       int64                                 `json:"sequence"`
	ActionKind     string                                `json:"action_kind"`
	Status         string                                `json:"status"`
	Explanation    string                                `json:"explanation"`
	ChangedFields  []string                              `json:"changed_fields"`
	Current        *OperationWorkflowNextActionQueueItem `json:"current,omitempty"`
	Completion     *OperationWorkflowReadinessCompletion `json:"completion,omitempty"`
}

type OperationWorkflowActionObligation struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
}
type OperationWorkflowObligationEvent struct {
	FulfillmentReviewEscalation *OperationWorkflowFulfillmentReviewEscalation         `json:"fulfillment_review_escalation,omitempty"`
	FulfillmentReviewReminder   *OperationWorkflowFulfillmentReviewReminder           `json:"fulfillment_review_reminder,omitempty"`
	ReviewCoordination          *OperationWorkflowFulfillmentReviewCoordinationChange `json:"review_coordination,omitempty"`
	FulfillmentReview           *OperationWorkflowObligationFulfillmentReview         `json:"fulfillment_review,omitempty"`
	Reaffirmation               *OperationWorkflowObligationReaffirmation             `json:"reaffirmation,omitempty"`
	Reopening                   *OperationWorkflowObligationReopening                 `json:"reopening,omitempty"`
	Fulfillment                 *OperationWorkflowObligationFulfillment               `json:"fulfillment,omitempty"`
	TargetBinding               *OperationWorkflowObligationTargetBinding             `json:"target_binding,omitempty"`
	Replacement                 *OperationWorkflowObligationReplacement               `json:"replacement,omitempty"`
	Cases                       []OperationWorkflowNextActionCase                     `json:"cases,omitempty"`
	ActionKind                  string                                                `json:"action_kind,omitempty"`
	SharedPlan                  bool                                                  `json:"shared_plan,omitempty"`
	ObligationID                string                                                `json:"obligation_id,omitempty"`
	Kind                        string                                                `json:"kind"`
	At                          time.Time                                             `json:"at"`
	Actor                       string                                                `json:"actor,omitempty"`
	Explanation                 string                                                `json:"explanation"`
}

type OperationWorkflowCaseObligationHistoryItem struct {
	FulfillmentReview    *OperationWorkflowObligationFulfillmentReview `json:"fulfillment_review,omitempty"`
	ObligationID         string                                        `json:"obligation_id"`
	ActionKind           string                                        `json:"action_kind"`
	SubscriptionID       string                                        `json:"subscription_id"`
	Sequence             int64                                         `json:"sequence"`
	Scope                string                                        `json:"scope"`
	SubscriptionTenantID string                                        `json:"subscription_tenant_id,omitempty"`
	SharedPlan           bool                                          `json:"shared_plan"`
	Events               []OperationWorkflowObligationEvent            `json:"events"`
	EventsTruncated      bool                                          `json:"events_truncated"`
	Current              *OperationWorkflowCaseNextAction              `json:"current,omitempty"`
}

type OperationWorkflowObligationReplacement struct {
	PredecessorID   string `json:"predecessor_id"`
	SuccessorID     string `json:"successor_id"`
	PredecessorKind string `json:"predecessor_kind"`
	SuccessorKind   string `json:"successor_kind"`
	Reason          string `json:"reason"`
	ReasonKind      string `json:"reason_kind"`
}

type OperationWorkflowObligationContext struct {
	Status                   string    `json:"status"`
	Summary                  string    `json:"summary"`
	CheckedAt                time.Time `json:"checked_at"`
	CoverageIncomplete       bool      `json:"coverage_incomplete"`
	TransitionCount          int       `json:"transition_count"`
	EvaluatedTransitionCount int       `json:"evaluated_transition_count"`
}
type OperationWorkflowObligationAssessment struct {
	Binding        *OperationWorkflowObligationTargetBinding `json:"binding,omitempty"`
	Readiness      *OperationWorkflowTransitionReadiness     `json:"readiness,omitempty"`
	SubscriptionID string                                    `json:"subscription_id"`
	Sequence       int64                                     `json:"sequence"`
	ObligationID   string                                    `json:"obligation_id,omitempty"`
	ActionKind     string                                    `json:"action_kind"`
	Status         string                                    `json:"status"`
	Summary        string                                    `json:"summary"`
	CheckedAt      time.Time                                 `json:"checked_at"`
}

type OperationWorkflowObligationTransitionTarget struct {
	Operation       string `json:"operation"`
	From            string `json:"from"`
	To              string `json:"to"`
	ContractVersion int    `json:"contract_version"`
}
type OperationWorkflowObligationTargetBinding struct {
	ObligationID string                                      `json:"obligation_id"`
	Case         OperationWorkflowNextActionCase             `json:"case"`
	Transition   OperationWorkflowObligationTransitionTarget `json:"transition"`
	Reason       string                                      `json:"reason"`
	BoundAt      time.Time                                   `json:"bound_at"`
	BoundBy      string                                      `json:"bound_by"`
}
type OperationWorkflowObligationTargetRequest struct {
	ObligationID string                                       `json:"obligation_id"`
	Case         OperationWorkflowNextActionCase              `json:"case"`
	Transition   *OperationWorkflowObligationTransitionTarget `json:"transition,omitempty"`
	Clear        bool                                         `json:"clear,omitempty"`
	Reason       string                                       `json:"reason"`
}

// Fulfillment is an explicit case/cycle receipt, independent of shared-plan closure.
type OperationWorkflowObligationFulfillmentRequest struct {
	Binding             OperationWorkflowObligationTargetBinding `json:"binding"`
	Reason              string                                   `json:"reason"`
	EvidenceOperationID string                                   `json:"evidence_operation_id"`
	EvidenceReportID    string                                   `json:"evidence_report_id"`
}
type OperationWorkflowObligationFulfillment struct {
	Binding           OperationWorkflowObligationTargetBinding `json:"binding"`
	Reason            string                                   `json:"reason"`
	ActionKind        string                                   `json:"action_kind"`
	Owner             string                                   `json:"owner"`
	Evidence          OperationWorkflowStateHistoryEntry       `json:"evidence"`
	EvidenceCheckedAt time.Time                                `json:"evidence_checked_at"`
	CompletedAt       time.Time                                `json:"completed_at"`
	CompletedBy       string                                   `json:"completed_by"`
}

type OperationWorkflowObligationReopeningRequest struct {
	Binding     OperationWorkflowObligationTargetBinding `json:"binding"`
	FulfilledAt time.Time                                `json:"fulfilled_at"`
	Reason      string                                   `json:"reason"`
}
type OperationWorkflowObligationReopening struct {
	SourceSequence int64                                    `json:"source_sequence"`
	Binding        OperationWorkflowObligationTargetBinding `json:"binding"`
	FulfilledAt    time.Time                                `json:"fulfilled_at"`
	Reason         string                                   `json:"reason"`
	ActionKind     string                                   `json:"action_kind"`
	Owner          string                                   `json:"owner"`
	ReopenedAt     time.Time                                `json:"reopened_at"`
	ReopenedBy     string                                   `json:"reopened_by"`
}

type OperationWorkflowObligationFulfillmentReview struct {
	EscalationOwner        string                                   `json:"escalation_owner,omitempty"`
	EscalateAfterSeconds   int                                      `json:"escalate_after_seconds,omitempty"`
	ManualOwner            bool                                     `json:"manual_owner,omitempty"`
	DueAt                  *time.Time                               `json:"due_at,omitempty"`
	ActionKind             string                                   `json:"action_kind"`
	Binding                OperationWorkflowObligationTargetBinding `json:"binding"`
	FulfilledAt            time.Time                                `json:"fulfilled_at"`
	BasisAt                time.Time                                `json:"basis_at"`
	SourceSequence         int64                                    `json:"source_sequence"`
	Owner                  string                                   `json:"owner"`
	ReviewRequired         bool                                     `json:"review_required"`
	Reasons                []string                                 `json:"reasons"`
	CurrentReasons         []string                                 `json:"current_reasons"`
	RequiredAt             *time.Time                               `json:"required_at,omitempty"`
	CheckedAt              time.Time                                `json:"checked_at"`
	Fingerprint            string                                   `json:"fingerprint"`
	CurrentContractVersion int                                      `json:"current_contract_version"`
	CurrentStateRevision   int64                                    `json:"current_state_revision"`
	CurrentOperationID     string                                   `json:"current_operation_id,omitempty"`
	CurrentReportID        string                                   `json:"current_report_id,omitempty"`
}
type OperationWorkflowObligationReaffirmationRequest struct {
	Binding             OperationWorkflowObligationTargetBinding `json:"binding"`
	FulfilledAt         time.Time                                `json:"fulfilled_at"`
	ReviewFingerprint   string                                   `json:"review_fingerprint"`
	Reason              string                                   `json:"reason"`
	EvidenceOperationID string                                   `json:"evidence_operation_id"`
	EvidenceReportID    string                                   `json:"evidence_report_id"`
}
type OperationWorkflowObligationReaffirmation struct {
	ActionKind        string                                   `json:"action_kind"`
	Binding           OperationWorkflowObligationTargetBinding `json:"binding"`
	FulfilledAt       time.Time                                `json:"fulfilled_at"`
	ReviewFingerprint string                                   `json:"review_fingerprint"`
	Reason            string                                   `json:"reason"`
	SourceSequence    int64                                    `json:"source_sequence"`
	Owner             string                                   `json:"owner"`
	Evidence          OperationWorkflowStateHistoryEntry       `json:"evidence"`
	EvidenceCheckedAt time.Time                                `json:"evidence_checked_at"`
	ReaffirmedAt      time.Time                                `json:"reaffirmed_at"`
	ReaffirmedBy      string                                   `json:"reaffirmed_by"`
}

type OperationWorkflowFulfillmentReviewCoordinationRequest struct {
	EscalationOwner      *string    `json:"escalation_owner,omitempty"`
	EscalateAfterSeconds *int       `json:"escalate_after_seconds,omitempty"`
	ReviewID             string     `json:"review_id"`
	ReviewFingerprint    string     `json:"review_fingerprint"`
	Owner                string     `json:"owner"`
	DueAt                *time.Time `json:"due_at,omitempty"`
	Reason               string     `json:"reason"`
}
type OperationWorkflowFulfillmentReviewCoordinationChange struct {
	FromEscalationOwner          string                                   `json:"from_escalation_owner,omitempty"`
	EscalationOwner              string                                   `json:"escalation_owner,omitempty"`
	PreviousEscalateAfterSeconds int                                      `json:"previous_escalate_after_seconds,omitempty"`
	EscalateAfterSeconds         int                                      `json:"escalate_after_seconds,omitempty"`
	ReviewID                     string                                   `json:"review_id"`
	Binding                      OperationWorkflowObligationTargetBinding `json:"binding"`
	FulfilledAt                  time.Time                                `json:"fulfilled_at"`
	FromOwner                    string                                   `json:"from_owner"`
	Owner                        string                                   `json:"owner"`
	PreviousDueAt                *time.Time                               `json:"previous_due_at,omitempty"`
	DueAt                        *time.Time                               `json:"due_at,omitempty"`
	Reason                       string                                   `json:"reason"`
	At                           time.Time                                `json:"at"`
	Actor                        string                                   `json:"actor"`
}

// OperationWorkflowFulfillmentReviewReminder records one durable delivery for an exact review deadline.
type OperationWorkflowFulfillmentReviewReminder struct {
	ReviewID       string                                       `json:"review_id"`
	NotificationID string                                       `json:"notification_id"`
	DueAt          time.Time                                    `json:"due_at"`
	NotifiedAt     time.Time                                    `json:"notified_at"`
	Review         OperationWorkflowObligationFulfillmentReview `json:"review"`
}

// OperationWorkflowFulfillmentReviewEscalation records delivery after an unanswered review reminder.
type OperationWorkflowFulfillmentReviewEscalation struct {
	ReviewID               string                                       `json:"review_id"`
	NotificationID         string                                       `json:"notification_id"`
	ReminderNotificationID string                                       `json:"reminder_notification_id"`
	DueAt                  time.Time                                    `json:"due_at"`
	EscalateAt             time.Time                                    `json:"escalate_at"`
	NotifiedAt             time.Time                                    `json:"notified_at"`
	EscalationOwner        string                                       `json:"escalation_owner"`
	Review                 OperationWorkflowObligationFulfillmentReview `json:"review"`
}
