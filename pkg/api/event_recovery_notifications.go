package api

import "time"

// EventRecoveryFinishedWebhookPayload describes admission completion, not handler completion.
type EventRecoveryFinishedWebhookPayload struct {
	EventID        string    `json:"event_id"`
	JobID          string    `json:"job_id"`
	AppID          string    `json:"app_id"`
	Mode           string    `json:"mode"`
	State          string    `json:"state"`
	Outcome        string    `json:"outcome"`
	SelectedCount  int64     `json:"selected_count"`
	PendingCount   int64     `json:"pending_count"`
	QueuedCount    int64     `json:"queued_count"`
	SkippedCount   int64     `json:"skipped_count"`
	CancelledCount int64     `json:"cancelled_count"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	CompletedAt    time.Time `json:"completed_at"`
}

// EventRecoveryExecutionFinishedWebhookPayload contains confirmed terminal results,
// independently of admission completion and webhook acknowledgement.
type EventRecoveryExecutionFinishedWebhookPayload struct {
	EventRecoveryFinishedWebhookPayload
	Execution           EventRecoveryExecutionSummary `json:"execution"`
	ExecutionFinishedAt time.Time                     `json:"execution_finished_at"`
	ItemsURL            string                        `json:"items_url,omitempty"`
	UnresolvedCount     int64                         `json:"unresolved_count"`
}

// WorkflowRecoveryExecutionAttention contains generation-fenced advisory monitoring.
type WorkflowRecoveryExecutionAttention struct {
	ObservedAt            time.Time                                  `json:"observed_at"`
	DeadlineSeconds       int                                        `json:"deadline_seconds"`
	StallSeconds          int                                        `json:"stall_seconds"`
	StalledCount          int                                        `json:"stalled_count"`
	DeadlineExceededCount int                                        `json:"deadline_exceeded_count"`
	CapacityWaitingCount  int                                        `json:"capacity_waiting_count"`
	ScheduledWaitingCount int                                        `json:"scheduled_waiting_count"`
	UnknownCount          int                                        `json:"unknown_count"`
	NeedsAttention        bool                                       `json:"needs_attention"`
	NotificationCaptured  bool                                       `json:"notification_captured"`
	Targets               []WorkflowRecoveryExecutionAttentionTarget `json:"targets"`
}
type WorkflowRecoveryExecutionAttentionTarget struct {
	Position       int64      `json:"position"`
	RunID          string     `json:"run_id"`
	ResumeCount    int        `json:"resume_count"`
	Status         string     `json:"status"`
	WaitReason     string     `json:"wait_reason,omitempty"`
	AdmittedAt     *time.Time `json:"admitted_at,omitempty"`
	LastProgressAt *time.Time `json:"last_progress_at,omitempty"`
	DeadlineAt     *time.Time `json:"deadline_at,omitempty"`
	RunURL         string     `json:"run_url"`
	DiagnosticsURL string     `json:"diagnostics_url"`
}
type WorkflowRecoveryExecutionAttentionWebhookPayload struct {
	EventID    string                             `json:"event_id"`
	JobID      string                             `json:"job_id"`
	AppID      string                             `json:"app_id"`
	ObservedAt time.Time                          `json:"observed_at"`
	Attention  WorkflowRecoveryExecutionAttention `json:"attention"`
	ItemsURL   string                             `json:"items_url"`
}
