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
