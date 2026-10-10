package api

import "time"

// Inspection selectors identify one exact scope, never a bucket path or caller-
// supplied account/app identity.
type DurableEntityInspectRequest struct {
	Namespace        string `json:"namespace"`
	Key              string `json:"key"`
	Environment      string `json:"environment,omitempty"`
	PlatformTenantID string `json:"platform_tenant_id,omitempty"`
}

type DurableEntityInspectResponse struct {
	Entity           DurableEntityScope            `json:"entity"`
	RecoveryRevision string                        `json:"recovery_revision"`
	Version          uint64                        `json:"version"`
	StateCommitted   bool                          `json:"state_committed"`
	Alarm            DurableEntityAlarmInspection  `json:"alarm"`
	Outbox           DurableEntityOutboxInspection `json:"outbox"`
}

type DurableEntityScope struct {
	AccountID     string `json:"account_id"`
	AppID         string `json:"app_id"`
	EnvironmentID string `json:"environment_id"`
	TenantID      string `json:"tenant_id,omitempty"`
	Namespace     string `json:"namespace"`
	Key           string `json:"key"`
}

type DurableEntityAlarmInspection struct {
	AlarmAt       *time.Time `json:"alarm_at,omitempty"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	Exhausted     bool       `json:"exhausted"`
}

type DurableEntityOutboxInspection struct {
	Pending       int                        `json:"pending"`
	HeadID        string                     `json:"head_id,omitempty"`
	Attempts      int                        `json:"attempts"`
	NextAttemptAt *time.Time                 `json:"next_attempt_at,omitempty"`
	Exhausted     bool                       `json:"exhausted"`
	HeadDelivery  *DurableEntityHeadDelivery `json:"head_delivery,omitempty"`
}

// A separate, later transport observation. Unknown includes absent/pruned
// history and read failures; it never means never accepted.
type DurableEntityHeadDelivery struct {
	Status string `json:"status"` // unknown, pending, in_flight, succeeded, failed, dead
}
