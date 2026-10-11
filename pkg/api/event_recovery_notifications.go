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
	UnresolvedCount     int64                         `json:"unresolved_count"`
}
