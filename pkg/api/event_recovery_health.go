package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type EventRecoveryCapacityWait struct {
	Scope       string    `json:"scope"`
	Gate        string    `json:"gate"`
	Explanation string    `json:"explanation"`
	StartedAt   time.Time `json:"started_at"`
	ObservedAt  time.Time `json:"observed_at"`
	AgeSeconds  float64   `json:"age_seconds"`
}
type EventRecoveryJobHealth struct {
	CapacityWait       *EventRecoveryCapacityWait `json:"capacity_wait,omitempty"`
	JobID              string                     `json:"job_id"`
	Mode               string                     `json:"mode"`
	State              string                     `json:"state"`
	Status             string                     `json:"status"`
	PendingCount       int64                      `json:"pending_count"`
	RatePerSecond      int                        `json:"rate_per_second"`
	LastProgressAt     *time.Time                 `json:"last_progress_at,omitempty"`
	ProgressAgeSeconds float64                    `json:"progress_age_seconds"`
	ProgressKnown      bool                       `json:"progress_known"`
	NextAttemptAt      time.Time                  `json:"next_attempt_at"`
	EligibleAt         time.Time                  `json:"eligible_at"`
	OverdueSeconds     float64                    `json:"overdue_seconds"`
	ExpiresAt          time.Time                  `json:"expires_at"`
	Expiring           bool                       `json:"expiring"`
	WaitReason         string                     `json:"wait_reason,omitempty"`
}

// Execution counts describe the oldest retained unresolved jobs. Partial counts
// are lower bounds and must not be used to conclude that recovery is healthy.
type EventRecoveryExecutionHealth struct {
	Coverage                string                            `json:"coverage"`
	ObservedJobs            int64                             `json:"observed_jobs"`
	WaitingJobs             int64                             `json:"waiting_jobs"`
	ProlongedWaitJobs       int64                             `json:"prolonged_wait_jobs"`
	UnknownJobs             int64                             `json:"unknown_jobs"`
	RetentionRiskJobs       int64                             `json:"retention_risk_jobs"`
	CountsComplete          bool                              `json:"counts_complete"`
	JobLimit                int                               `json:"job_limit"`
	WaitWarningSeconds      int64                             `json:"wait_warning_seconds"`
	RetentionWarningSeconds int64                             `json:"retention_warning_seconds"`
	Jobs                    []EventRecoveryExecutionJobHealth `json:"jobs"`
}
type EventRecoveryExecutionJobHealth struct {
	JobID                     string                        `json:"job_id"`
	ParentJobID               string                        `json:"parent_job_id,omitempty"`
	State                     string                        `json:"state"`
	Status                    string                        `json:"status"`
	CompletedAt               time.Time                     `json:"completed_at"`
	WaitAgeSeconds            float64                       `json:"wait_age_seconds"`
	RetainUntil               time.Time                     `json:"retain_until"`
	ProlongedWait             bool                          `json:"prolonged_wait"`
	RetentionRisk             bool                          `json:"retention_risk"`
	NotificationPending       bool                          `json:"notification_pending"`
	QueuedCount               int64                         `json:"queued_count"`
	UntrackedCount            int64                         `json:"untracked_count"`
	UnknownCount              int64                         `json:"unknown_count"`
	UnresolvedCount           int64                         `json:"unresolved_count"`
	AwaitingSavedResultsCount int64                         `json:"awaiting_saved_results_count"`
	Execution                 EventRecoveryExecutionSummary `json:"execution"`
}
type EventRecoveryHealth struct {
	Notifications              *EventRecoveryNotificationsHealth `json:"notifications,omitempty"`
	Execution                  *EventRecoveryExecutionHealth     `json:"execution,omitempty"`
	CapacityWaitWarningSeconds int64                             `json:"capacity_wait_warning_seconds"`
	CapacityWaitingJobs        int64                             `json:"capacity_waiting_jobs"`
	ProlongedCapacityWaitJobs  int64                             `json:"prolonged_capacity_wait_jobs"`
	AppID                      string                            `json:"app_id"`
	ObservedAt                 time.Time                         `json:"observed_at"`
	StallGraceSeconds          int64                             `json:"stall_grace_seconds"`
	ExpiryWarningSeconds       int64                             `json:"expiry_warning_seconds"`
	RunningJobs                int64                             `json:"running_jobs"`
	PausedJobs                 int64                             `json:"paused_jobs"`
	StalledJobs                int64                             `json:"stalled_jobs"`
	ExpiringJobs               int64                             `json:"expiring_jobs"`
	PausedExpiringJobs         int64                             `json:"paused_expiring_jobs"`
	Jobs                       []EventRecoveryJobHealth          `json:"jobs"`
}

func (c *Client) GetEventRecoveryHealth(ctx context.Context, app string) (EventRecoveryHealth, error) {
	var out EventRecoveryHealth
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/event-recoveries/health", nil, &out)
	return out, err
}
func IsEventRecoveryAlertMetric(metric string) bool {
	return IsEventRecoveryNotificationAlertMetric(metric) || IsEventRecoveryExecutionAlertMetric(metric) || metric == "event_recovery_capacity_wait_jobs" || metric == "event_recovery_stalled_jobs" || metric == "event_recovery_expiring_jobs"
}

func IsEventRecoveryExecutionAlertMetric(metric string) bool {
	switch metric {
	case "event_recovery_execution_waiting_jobs", "event_recovery_execution_prolonged_wait_jobs", "event_recovery_execution_unknown_jobs", "event_recovery_execution_retention_risk_jobs":
		return true
	}
	return false
}
