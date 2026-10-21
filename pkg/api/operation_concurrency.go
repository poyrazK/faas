// adr: 20261018210500
package api

import "time"

// An Operation concurrency policy bounds how many Operations of one name, in
// one app environment, a single customer has in flight. Work beyond it is
// queued and released in order.
const OperationConcurrencyMaxRunning = 1000

// OperationConcurrencyPolicyRequest sets a policy. Without PlatformTenantID
// it is the default for every customer; with one it overrides the default for
// that customer. Scope defaults to the default environment.
type OperationConcurrencyPolicyRequest struct {
	Scope            string `json:"scope,omitempty"`
	Operation        string `json:"operation"`
	PlatformTenantID string `json:"platform_tenant_id,omitempty"`
	MaxRunning       int    `json:"max_running"`
}

type OperationConcurrencyPolicy struct {
	ID               string    `json:"id"`
	AppID            string    `json:"app_id"`
	Scope            string    `json:"scope"`
	Operation        string    `json:"operation"`
	PlatformTenantID string    `json:"platform_tenant_id,omitempty"`
	MaxRunning       int       `json:"max_running"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// OperationConcurrencyUsage is one customer's queue for one Operation name.
// Limit is zero when no policy applies any more; the next release pass
// starts that work.
type OperationConcurrencyUsage struct {
	Scope            string `json:"scope"`
	Operation        string `json:"operation"`
	PlatformTenantID string `json:"platform_tenant_id"`
	Limit            int    `json:"limit"`
	InFlight         int64  `json:"in_flight"`
	Queued           int64  `json:"queued"`
}

type OperationConcurrencyResponse struct {
	Policies []OperationConcurrencyPolicy `json:"policies"`
	Queues   []OperationConcurrencyUsage  `json:"queues"`
}

// OperationQueueEntry is one queued Operation of a customer. Position counts
// from 1 within the customer's queue for that Operation name.
type OperationQueueEntry struct {
	OperationID string    `json:"operation_id"`
	AppID       string    `json:"app_id"`
	Scope       string    `json:"scope"`
	Operation   string    `json:"operation"`
	Position    int64     `json:"position"`
	QueuedAt    time.Time `json:"queued_at"`
}

type OperationQueueResponse struct {
	Queued []OperationQueueEntry `json:"queued"`
}
