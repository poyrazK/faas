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
	Scope        string    `json:"scope"`
	DeploymentID string    `json:"deployment_id,omitempty"`
	ObservedAt   time.Time `json:"observed_at"`
	Stale        bool      `json:"stale"`
	Desired      int       `json:"desired"`
	Running      int       `json:"running"`
	Starting     int       `json:"starting"`
	Draining     int       `json:"draining"`
}
