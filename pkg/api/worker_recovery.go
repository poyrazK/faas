package api

import "time"

// AppWorkerRecovery separates the app-wide retry ledger from observations of
// individual environment pools. Reading it never schedules or wakes workers.
type AppWorkerRecovery struct {
	Available     bool                       `json:"available"`
	Eligible      bool                       `json:"eligible"`
	Scope         string                     `json:"scope"`
	Status        string                     `json:"status"`
	Reason        string                     `json:"reason,omitempty"`
	Failures      int                        `json:"failures"`
	NextAttemptAt *time.Time                 `json:"next_attempt_at,omitempty"`
	LeaseUntil    *time.Time                 `json:"lease_until,omitempty"`
	UpdatedAt     *time.Time                 `json:"updated_at,omitempty"`
	Pools         []AppWorkerPoolObservation `json:"pools"`
}

type AppWorkerPoolObservation struct {
	Scope        string                   `json:"scope"`
	DeploymentID string                   `json:"deployment_id,omitempty"`
	ObservedAt   time.Time                `json:"observed_at"`
	Stale        bool                     `json:"stale"`
	Desired      int                      `json:"desired"`
	Running      int                      `json:"running"`
	Starting     int                      `json:"starting"`
	Draining     int                      `json:"draining"`
	Blockers     []WorkerAdmissionBlocker `json:"blockers,omitempty"`
}

// WorkerAdmissionBlocker records an actual admission refusal, never a forecast.
// Node constraints disclose the requested resource, without fleet usage or IDs.
type WorkerAdmissionBlocker struct {
	Code       string     `json:"code"`
	ObservedAt time.Time  `json:"observed_at"`
	Stale      bool       `json:"stale,omitempty"`
	Resource   string     `json:"resource,omitempty"`
	Requested  *int64     `json:"requested,omitempty"`
	Used       *int64     `json:"used,omitempty"`
	Limit      *int64     `json:"limit,omitempty"`
	RetryAt    *time.Time `json:"retry_at,omitempty"`
}

// Clone keeps persisted observations and watch comparisons independent of readers.
func (b WorkerAdmissionBlocker) Clone() WorkerAdmissionBlocker {
	if b.Requested != nil {
		v := *b.Requested
		b.Requested = &v
	}
	if b.Used != nil {
		v := *b.Used
		b.Used = &v
	}
	if b.Limit != nil {
		v := *b.Limit
		b.Limit = &v
	}
	if b.RetryAt != nil {
		v := *b.RetryAt
		b.RetryAt = &v
	}
	return b
}
