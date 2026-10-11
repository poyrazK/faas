package api

import "time"

// Readiness subscriptions discover bounded current edges and compare saved observations.
// They produce advisory inbox notifications, never execution authority.
type OperationWorkflowReadinessSubscriptionRequest struct {
	AppID           string   `json:"app_id,omitempty"`
	TenantID        string   `json:"tenant_id,omitempty"`
	Scope           string   `json:"scope"`
	Name            string   `json:"name"`
	Workflow        string   `json:"workflow,omitempty"`
	TargetOperation string   `json:"target_operation,omitempty"`
	Owner           string   `json:"owner,omitempty"`
	Unassigned      bool     `json:"unassigned,omitempty"`
	Events          []string `json:"events,omitempty"`
}
type OperationWorkflowReadinessSubscription struct {
	ID                     string    `json:"id"`
	AppID                  string    `json:"app_id"`
	TenantID               string    `json:"tenant_id,omitempty"`
	Scope                  string    `json:"scope"`
	Name                   string    `json:"name"`
	Workflow               string    `json:"workflow,omitempty"`
	TargetOperation        string    `json:"target_operation,omitempty"`
	Owner                  string    `json:"owner,omitempty"`
	Unassigned             bool      `json:"unassigned,omitempty"`
	Events                 []string  `json:"events"`
	CreatedAt              time.Time `json:"created_at"`
	EvaluatedAt            time.Time `json:"evaluated_at"`
	Status                 string    `json:"status"`
	WatchedTransitionCount int       `json:"watched_transition_count"`
	CoverageIncomplete     bool      `json:"coverage_incomplete"`
	LatestSequence         int64     `json:"latest_sequence"`
}
type OperationWorkflowReadinessSubscriptionsResponse struct {
	Items []OperationWorkflowReadinessSubscription `json:"items"`
}
type OperationWorkflowReadinessNotification struct {
	Reminder     *OperationWorkflowReadinessDueReminder  `json:"reminder,omitempty"`
	Coordination *OperationWorkflowReadinessCoordination `json:"coordination,omitempty"`
	ID           string                                  `json:"id"`
	Sequence     int64                                   `json:"sequence"`
	Kind         string                                  `json:"kind"`
	CreatedAt    time.Time                               `json:"created_at"`
	Explanation  string                                  `json:"explanation"`
	Change       *OperationWorkflowReadinessChange       `json:"change,omitempty"`
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
	Completion *OperationWorkflowReadinessCompletionRequest `json:"completion,omitempty"`
	Revision   int64                                        `json:"revision"`
	Status     string                                       `json:"status"`
	Owner      string                                       `json:"owner"`
	Note       string                                       `json:"note"`
	FollowUpAt *time.Time                                   `json:"follow_up_at,omitempty"`
}

// Handoff summarizes retained application evidence; it never authorizes execution.
type OperationWorkflowReadinessHandoff struct {
	Summary             string     `json:"summary"`
	BaselineEvaluatedAt *time.Time `json:"baseline_evaluated_at,omitempty"`
	Comparable          bool       `json:"comparable"`
	Limitations         []string   `json:"limitations"`
}
type OperationWorkflowReadinessNotificationCheck struct {
	Handoff      OperationWorkflowReadinessHandoff      `json:"handoff"`
	Notification OperationWorkflowReadinessNotification `json:"notification"`
	CheckedAt    time.Time                              `json:"checked_at"`
	Status       string                                 `json:"status"`
	Change       *OperationWorkflowReadinessChange      `json:"change,omitempty"`
}

// Follow-up queues aggregate retained coordination, not application execution tasks.
type OperationWorkflowReadinessFollowUpOptions struct {
	ReminderOnly bool   `json:"reminder_only,omitempty"`
	AppID        string `json:"app_id,omitempty"`
	TenantID     string `json:"tenant_id,omitempty"`
	Scope        string `json:"scope"`
	Owner        string `json:"owner,omitempty"`
	Workflow     string `json:"workflow,omitempty"`
	Due          string `json:"due,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}
type OperationWorkflowReadinessFollowUp struct {
	Reminder             *OperationWorkflowReadinessDueReminder `json:"reminder,omitempty"`
	SubscriptionID       string                                 `json:"subscription_id"`
	SubscriptionName     string                                 `json:"subscription_name"`
	SubscriptionTenantID string                                 `json:"subscription_tenant_id,omitempty"`
	PlatformTenantID     string                                 `json:"platform_tenant_id,omitempty"`
	Scope                string                                 `json:"scope"`
	NotificationID       string                                 `json:"notification_id"`
	Sequence             int64                                  `json:"sequence"`
	Kind                 string                                 `json:"kind"`
	CreatedAt            time.Time                              `json:"created_at"`
	Coordination         OperationWorkflowReadinessCoordination `json:"coordination"`
	Workflow             string                                 `json:"workflow,omitempty"`
	InstanceID           string                                 `json:"instance_id,omitempty"`
	Subject              *OperationSubject                      `json:"subject,omitempty"`
	Operation            string                                 `json:"operation,omitempty"`
	FromState            string                                 `json:"from_state,omitempty"`
	ToState              string                                 `json:"to_state,omitempty"`
	Overdue              bool                                   `json:"overdue"`
}
type OperationWorkflowReadinessFollowUpsResponse struct {
	DueReminderCount int                                  `json:"due_reminder_count"`
	Items            []OperationWorkflowReadinessFollowUp `json:"items"`
	EvaluatedAt      time.Time                            `json:"evaluated_at"`
	TotalCount       int                                  `json:"total_count"`
	OverdueCount     int                                  `json:"overdue_count"`
	NextOffset       int                                  `json:"next_offset"`
	HasMore          bool                                 `json:"has_more"`
	RetentionLimited bool                                 `json:"retention_limited"`
}

// A reminder is one durable delivery marker for the original notification/deadline.
type OperationWorkflowReadinessDueReminder struct {
	ID        string    `json:"id"`
	DueAt     time.Time `json:"due_at"`
	CreatedAt time.Time `json:"created_at"`
}

// Completion closes coordination only; outcome is an operator/customer assertion.
type OperationWorkflowReadinessCompletionRequest struct {
	Outcome              string   `json:"outcome"`
	Note                 string   `json:"note"`
	EvidenceOperationIDs []string `json:"evidence_operation_ids"`
}
type OperationWorkflowReadinessCompletion struct {
	Outcome              string                                       `json:"outcome"`
	Note                 string                                       `json:"note"`
	EvidenceOperationIDs []string                                     `json:"evidence_operation_ids"`
	CompletedAt          time.Time                                    `json:"completed_at"`
	CompletedBy          string                                       `json:"completed_by"`
	Evidence             OperationWorkflowReadinessCompletionEvidence `json:"evidence"`
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
