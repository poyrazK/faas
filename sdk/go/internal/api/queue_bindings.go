package api

import "time"

// QueueBindingResponse is the durable app-scoped mapping between a logical
// queue and a worker/job workload. It is intentionally independent of queue
// messages so push consumers and autoscaling can reconcile configuration.
type QueueBindingResponse struct {
	Environment    string          `json:"environment,omitempty"`
	EnvironmentID  string          `json:"environment_id,omitempty"`
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
	RetiredAt      *time.Time      `json:"retired_at,omitempty"`
}

// QueueBindingStatusResponse is the read-only control-plane projection for a
// queue binding. ConsumerState describes the durable push-consumer projection;
// ConsumerLiveness and the timestamps are the scheduler's last-known poll
// health. Queue counters come from the same lease-aware queue view used by the
// autoscaler.
type QueueBindingStatusResponse struct {
	Environment             string     `json:"environment,omitempty"`
	EnvironmentID           string     `json:"environment_id,omitempty"`
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
	// Environment selects a registered project environment; omission retains the shared legacy binding.
	Environment    string          `json:"environment,omitempty"`
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
