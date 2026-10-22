// adr: 20261018161833
package api

import (
	"encoding/json"
	"time"
)

// An Operation schedule starts ordinary Operations for one customer at chosen
// times: once at a fixed time, or on a cron timetable.
const (
	OperationScheduleOnce = "once"
	OperationScheduleCron = "cron"

	OperationScheduleActive    = "active"
	OperationSchedulePaused    = "paused"
	OperationScheduleCompleted = "completed"
	OperationScheduleFailed    = "failed"
	OperationScheduleCancelled = "cancelled"

	OperationScheduleBodyMaxBytes   = OperationSubmissionMaxBytes + 4096
	OperationScheduleLastErrorBytes = 512
)

// OperationScheduleRequest creates a schedule. Exactly one of At and Cron is
// set; Timezone applies to Cron and defaults to UTC.
type OperationScheduleRequest struct {
	DefinitionID string          `json:"definition_id"`
	Input        json.RawMessage `json:"input"`
	At           *time.Time      `json:"at,omitempty"`
	Cron         string          `json:"cron,omitempty"`
	Timezone     string          `json:"timezone,omitempty"`
}

// OperationScheduleResponse is one schedule. LastOperationID names the
// Operation its latest fire admitted; LastError explains a refused fire.
type OperationScheduleResponse struct {
	ID               string     `json:"id"`
	AppID            string     `json:"app_id"`
	PlatformTenantID string     `json:"platform_tenant_id"`
	DefinitionID     string     `json:"definition_id"`
	Kind             string     `json:"kind"`
	At               *time.Time `json:"at,omitempty"`
	Cron             string     `json:"cron,omitempty"`
	Timezone         string     `json:"timezone,omitempty"`
	State            string     `json:"state"`
	NextFireAt       *time.Time `json:"next_fire_at,omitempty"`
	Fires            int64      `json:"fires"`
	LastFiredAt      *time.Time `json:"last_fired_at,omitempty"`
	LastOperationID  string     `json:"last_operation_id,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type OperationScheduleListResponse struct {
	Schedules []OperationScheduleResponse `json:"schedules"`
}
