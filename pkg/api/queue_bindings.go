package api

import (
	"encoding/json"
	"time"
)

// QueueBindingResponse is the durable app-scoped mapping between a logical
// queue and a worker/job workload. It is intentionally independent of queue
// messages so push consumers and autoscaling can reconcile configuration.
type QueueBindingResponse struct {
	ID             string          `json:"id"`
	AppID          string          `json:"app_id"`
	AccountID      string          `json:"account_id"`
	Name           string          `json:"name"`
	QueueName      string          `json:"queue_name"`
	Mode           string          `json:"mode"`
	WorkloadClass  string          `json:"workload_class"`
	Enabled        bool            `json:"enabled"`
	MaxConcurrency int             `json:"max_concurrency"`
	RetryPolicy    *RetryPolicyDTO `json:"retry_policy,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// QueueBindingStatusResponse is the read-only control-plane projection for a
// queue binding. ConsumerState describes the durable push-consumer projection;
// ConsumerLiveness and the timestamps are the scheduler's last-known poll
// health. Queue counters come from the same lease-aware queue view used by the
// autoscaler.
type QueueBindingStatusResponse struct {
	BindingID               string     `json:"binding_id"`
	Name                    string     `json:"name"`
	QueueName               string     `json:"queue_name"`
	Mode                    string     `json:"mode"`
	WorkloadClass           string     `json:"workload_class"`
	Enabled                 bool       `json:"enabled"`
	ConsumerState           string     `json:"consumer_state"`
	ConsumerStateReason     string     `json:"consumer_state_reason,omitempty"`
	ConsumerLiveness        string     `json:"consumer_liveness"`
	TriggerID               string     `json:"trigger_id,omitempty"`
	LastPollAt              *time.Time `json:"last_poll_at,omitempty"`
	LastSuccessAt           *time.Time `json:"last_success_at,omitempty"`
	LastErrorAt             *time.Time `json:"last_error_at,omitempty"`
	LastError               string     `json:"last_error,omitempty"`
	LagMessages             *int64     `json:"lag_messages,omitempty"`
	LagAgeSeconds           *float64   `json:"lag_age_seconds,omitempty"`
	Depth                   int        `json:"depth"`
	InFlight                int        `json:"in_flight"`
	DeadLetter              int        `json:"dead_letter"`
	OldestPendingAt         *time.Time `json:"oldest_pending_at,omitempty"`
	OldestPendingAgeSeconds *int64     `json:"oldest_pending_age_seconds,omitempty"`
	GeneratedAt             time.Time  `json:"generated_at"`
}

type CreateQueueBindingRequest struct {
	Name           string          `json:"name"`
	QueueName      string          `json:"queue_name"`
	Mode           string          `json:"mode,omitempty"`
	WorkloadClass  string          `json:"workload_class,omitempty"`
	Enabled        *bool           `json:"enabled,omitempty"`
	MaxConcurrency int             `json:"max_concurrency,omitempty"`
	RetryPolicy    *RetryPolicyDTO `json:"retry_policy,omitempty"`
}

type UpdateQueueBindingRequest struct {
	QueueName      *string         `json:"queue_name,omitempty"`
	Mode           *string         `json:"mode,omitempty"`
	WorkloadClass  *string         `json:"workload_class,omitempty"`
	Enabled        *bool           `json:"enabled,omitempty"`
	MaxConcurrency *int            `json:"max_concurrency,omitempty"`
	RetryPolicy    *RetryPolicyDTO `json:"retry_policy,omitempty"`
}

// QueueWorkloadProfileRequest configures the common queue worker profile in
// one idempotent control-plane operation. Zero-valued fields use platform
// defaults; advanced binding and scaling APIs remain available separately.
type QueueWorkloadProfileRequest struct {
	QueueName      string          `json:"queue_name,omitempty"`
	WorkloadClass  string          `json:"workload_class,omitempty"`
	MaxConcurrency int             `json:"max_concurrency,omitempty"`
	TargetDepth    float64         `json:"target_depth,omitempty"`
	RetryPolicy    *RetryPolicyDTO `json:"retry_policy,omitempty"`
	Force          bool            `json:"force,omitempty"`
}

// QueueWorkloadProfileResponse is the converged queue binding and scaling
// policy returned by the simple queue workload endpoint.
type QueueWorkloadProfileResponse struct {
	App           AppResponse          `json:"app"`
	Binding       QueueBindingResponse `json:"binding"`
	ScalingPolicy *ScalingPolicy       `json:"scaling_policy"`
	Created       bool                 `json:"created"`
}

// QueueBindingResponseFromRow maps the state row without importing pkg/state
// into pkg/api. Invalid persisted retry JSON is treated as an empty policy;
// the write path and database CHECK keep production rows object-shaped.
type QueueBindingRow struct {
	ID              string
	AppID           string
	AccountID       string
	Name            string
	QueueName       string
	Mode            string
	WorkloadClass   string
	Enabled         bool
	MaxConcurrency  int
	RetryPolicyJSON json.RawMessage
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func QueueBindingResponseFromRow(row QueueBindingRow) QueueBindingResponse {
	var policy RetryPolicyDTO
	var policyPtr *RetryPolicyDTO
	if len(row.RetryPolicyJSON) > 0 && json.Unmarshal(row.RetryPolicyJSON, &policy) == nil {
		policyPtr = &policy
	}
	return QueueBindingResponse{
		ID: row.ID, AppID: row.AppID, AccountID: row.AccountID,
		Name: row.Name, QueueName: row.QueueName, Mode: row.Mode,
		WorkloadClass: row.WorkloadClass, Enabled: row.Enabled,
		MaxConcurrency: row.MaxConcurrency, RetryPolicy: policyPtr,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
