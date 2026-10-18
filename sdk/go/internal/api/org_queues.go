package api

import (
	"encoding/json"
	"time"
)

type OrgQueueUsageResponse struct {
	OrganizationID      string `json:"organization_id"`
	Plan                Plan   `json:"plan"`
	Bindings            int64  `json:"bindings"`
	Pending             int64  `json:"pending"`
	Processing          int64  `json:"processing"`
	DeadLetter          int64  `json:"dead_letter"`
	ReservedAsync       int64  `json:"reserved_async"`
	MaxAsyncInvocations int    `json:"max_async_invocations"`
	MaxTriggers         int    `json:"max_triggers"`
}
type ProjectEnvironmentQueueBinding struct {
	Name           string         `json:"name"`
	QueueName      string         `json:"queue_name"`
	Mode           string         `json:"mode"`
	WorkloadClass  string         `json:"workload_class"`
	Enabled        bool           `json:"enabled"`
	MaxConcurrency int            `json:"max_concurrency"`
	RetryPolicy    RetryPolicyDTO `json:"retry_policy"`
}
type ProjectEnvironmentQueueBindingsResponse struct {
	Environment      string                           `json:"environment"`
	Workload         string                           `json:"workload"`
	Revision         int64                            `json:"revision"`
	WorkloadRevision int64                            `json:"workload_revision"`
	ConfigHash       string                           `json:"config_hash"`
	ActivationState  string                           `json:"activation_state"`
	Bindings         []ProjectEnvironmentQueueBinding `json:"bindings"`
}
type QueueDeadLetterMessage struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	FailedAt  time.Time `json:"failed_at"`
	Attempts  int       `json:"attempts"`
	LastError string    `json:"last_error"`
	Payload   string    `json:"payload"`
}
type QueueDeadLetterResponse struct {
	AppSlug    string                   `json:"app_slug"`
	Messages   []QueueDeadLetterMessage `json:"messages"`
	NextBefore string                   `json:"next_before,omitempty"`
}
type QueueLeaseMessage struct {
	ID             string            `json:"id"`
	Receipt        string            `json:"receipt"`
	Payload        json.RawMessage   `json:"payload"`
	Headers        map[string]string `json:"headers,omitempty"`
	Attempt        int               `json:"attempt"`
	EnqueuedAt     time.Time         `json:"enqueued_at"`
	LeaseExpiresAt time.Time         `json:"lease_expires_at"`
}
type QueueLeaseReceiptFailure struct {
	Receipt string `json:"receipt"`
	Reason  string `json:"reason"`
}
type QueueLeaseReceiptsRequest struct {
	Receipts                 []string `json:"receipts"`
	DelaySeconds             int      `json:"delay_seconds,omitempty"`
	Error                    string   `json:"error,omitempty"`
	VisibilityTimeoutSeconds int      `json:"visibility_timeout_seconds,omitempty"`
}
type QueueLeaseReceiptsResponse struct {
	Succeeded []string                   `json:"succeeded"`
	Failed    []QueueLeaseReceiptFailure `json:"failed"`
}
type QueueLeaseRequest struct {
	MaxMessages              int `json:"max_messages,omitempty"`
	VisibilityTimeoutSeconds int `json:"visibility_timeout_seconds,omitempty"`
	WaitSeconds              int `json:"wait_seconds,omitempty"`
}
type QueueLeaseResponse struct {
	Messages []QueueLeaseMessage `json:"messages"`
}
type QueuePeekMessage struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Attempts  int       `json:"attempts"`
	Payload   string    `json:"payload"`
	LastError string    `json:"last_error,omitempty"`
}
type QueuePeekResponse struct {
	AppSlug    string             `json:"app_slug"`
	Messages   []QueuePeekMessage `json:"messages"`
	NextBefore string             `json:"next_before,omitempty"`
}
type QueueStateResponse struct {
	AppSlug                 string     `json:"app_slug"`
	Plan                    string     `json:"plan"`
	PlanCap                 int        `json:"plan_cap"`
	Depth                   int        `json:"depth"`
	InFlight                int        `json:"in_flight"`
	OldestPendingAt         *time.Time `json:"oldest_pending_at,omitempty"`
	OldestPendingAgeSeconds *int64     `json:"oldest_pending_age_seconds,omitempty"`
	GeneratedAt             time.Time  `json:"generated_at"`
}
type ReplaceProjectEnvironmentQueueBindingsRequest struct {
	ExpectedRevision *int64                            `json:"expected_revision"`
	Bindings         *[]ProjectEnvironmentQueueBinding `json:"bindings"`
}
