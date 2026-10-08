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
type EventRecoveryHealth struct {
	CapacityWaitWarningSeconds int64                    `json:"capacity_wait_warning_seconds"`
	CapacityWaitingJobs        int64                    `json:"capacity_waiting_jobs"`
	ProlongedCapacityWaitJobs  int64                    `json:"prolonged_capacity_wait_jobs"`
	AppID                      string                   `json:"app_id"`
	ObservedAt                 time.Time                `json:"observed_at"`
	StallGraceSeconds          int64                    `json:"stall_grace_seconds"`
	ExpiryWarningSeconds       int64                    `json:"expiry_warning_seconds"`
	RunningJobs                int64                    `json:"running_jobs"`
	PausedJobs                 int64                    `json:"paused_jobs"`
	StalledJobs                int64                    `json:"stalled_jobs"`
	ExpiringJobs               int64                    `json:"expiring_jobs"`
	PausedExpiringJobs         int64                    `json:"paused_expiring_jobs"`
	Jobs                       []EventRecoveryJobHealth `json:"jobs"`
}

func (c *Client) GetEventRecoveryHealth(ctx context.Context, app string) (EventRecoveryHealth, error) {
	var out EventRecoveryHealth
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/event-recoveries/health", nil, &out)
	return out, err
}
func IsEventRecoveryAlertMetric(metric string) bool {
	return metric == "event_recovery_capacity_wait_jobs" || metric == "event_recovery_stalled_jobs" || metric == "event_recovery_expiring_jobs"
}
