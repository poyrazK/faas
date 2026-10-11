package api

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"time"
)

// Scope comes from authenticated account/tenant routes, not this request.
type ExclusiveOperationRequest struct {
	Policy         string          `json:"policy"`
	Key            json.RawMessage `json:"key"`
	EquivalenceKey string          `json:"equivalence_key,omitempty"`
	Invocation     InvokeRequest   `json:"invocation"`
}

// ExclusiveJobOperationRequest submits a JobRun through the same ownership
// lane used by app operations. Scope is derived from the authenticated
// account and selected Job ID, never from the key value.
type ExclusiveJobOperationRequest struct {
	Policy         string              `json:"policy"`
	Key            json.RawMessage     `json:"key"`
	EquivalenceKey string              `json:"equivalence_key,omitempty"`
	Run            CreateJobRunRequest `json:"run"`
}

// ExclusiveAppTaskOperationRequest submits a deployment-attached command
// through the same app ownership lane used by managed invocations.
type ExclusiveAppTaskOperationRequest struct {
	Policy         string               `json:"policy"`
	Key            json.RawMessage      `json:"key"`
	EquivalenceKey string               `json:"equivalence_key,omitempty"`
	Task           CreateAppTaskRequest `json:"task"`
}

type ExclusiveTriggerBindingRequest struct {
	Policy           string          `json:"policy"`
	Key              json.RawMessage `json:"key"`
	PlatformTenantID string          `json:"platform_tenant_id,omitempty"`
	EquivalenceKey   string          `json:"equivalence_key,omitempty"`
}

type ExclusiveTriggerBindingRecord struct {
	Source           string          `json:"source"`
	TriggerID        string          `json:"trigger_id"`
	AppID            string          `json:"app_id,omitempty"`
	JobID            string          `json:"job_id,omitempty"`
	Policy           string          `json:"policy"`
	PlatformTenantID string          `json:"platform_tenant_id,omitempty"`
	Key              json.RawMessage `json:"key"`
	EquivalenceKey   string          `json:"equivalence_key,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type ExclusiveOperationAccepted struct {
	ID        string `json:"id"`
	Joined    bool   `json:"joined"`
	StatusURL string `json:"status_url"`
}

type ExclusiveOperationPolicy struct {
	Name              string   `json:"name,omitempty"`
	Scope             string   `json:"scope"`
	EnvironmentID     string   `json:"environment_id,omitempty"`
	MemberAppIDs      []string `json:"member_app_ids,omitempty"`
	MemberJobIDs      []string `json:"member_job_ids,omitempty"`
	Contention        string   `json:"contention"`
	LeaseSeconds      int      `json:"lease_seconds"`
	MaxAttemptSeconds int      `json:"max_attempt_seconds"`
	MaxAttempts       int      `json:"max_attempts,omitempty"`
	RetryAfterSeconds int      `json:"retry_after_seconds,omitempty"`
}

type ExclusiveWorkPolicyRecord struct {
	ID        string               `json:"id"`
	Revision  int64                `json:"revision"`
	Policy    exclusivework.Policy `json:"policy"`
	Retired   bool                 `json:"retired"`
	CreatedAt time.Time            `json:"created_at"`
	UpdatedAt time.Time            `json:"updated_at"`
}

type ExclusiveWorkPolicyList struct {
	Policies []ExclusiveWorkPolicyRecord `json:"policies"`
}

type ExclusiveOperationRecord struct {
	ID               string                  `json:"id"`
	AppID            string                  `json:"app_id,omitempty"`
	JobID            string                  `json:"job_id,omitempty"`
	PlatformTenantID string                  `json:"platform_tenant_id,omitempty"`
	Sequence         int64                   `json:"sequence"`
	State            string                  `json:"state"`
	PolicyRevision   int64                   `json:"policy_revision"`
	Generation       int64                   `json:"generation"`
	LeaseExpiresAt   *time.Time              `json:"lease_expires_at,omitempty"`
	AttemptDeadline  *time.Time              `json:"attempt_deadline,omitempty"`
	Result           json.RawMessage         `json:"result,omitempty"`
	Effects          []OperationEffectRecord `json:"effects,omitempty"`
	LastError        string                  `json:"last_error,omitempty"`
	CreatedAt        time.Time               `json:"created_at"`
	CompletedAt      *time.Time              `json:"completed_at,omitempty"`
}

// ManagedOperationResultVersion identifies the supported handler response protocol.
const ManagedOperationResultVersion = 1

// ManagedOperationResult is an opt-in HTTP handler response. Only managed
// request operations interpret this envelope; authority stays with schedd.
// The entire encoded envelope must fit MaxExclusiveResultBytes.
type ManagedOperationResult struct {
	Version int                      `json:"gregale_operation_result"`
	Result  json.RawMessage          `json:"result"`
	Effects []ManagedOperationEffect `json:"effects"`
}

// ManagedOperationEffect names a business webhook delivery proposed by a handler.
type ManagedOperationEffect struct {
	Name      string          `json:"name"`
	Payload   json.RawMessage `json:"payload"`
	WebhookID string          `json:"webhook_id"`
	Type      string          `json:"type"`
}

// OperationEffectRecord correlates immutable effect identity with the existing
// webhook ledger. Unavailable means the delivery history was deleted or pruned.
type OperationEffectRecord struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Generation int64  `json:"generation"`
	WebhookID  string `json:"webhook_id,omitempty"`
	DeliveryID string `json:"delivery_id,omitempty"`
	Type       string `json:"type,omitempty"`
	Status     string `json:"status"`
	Attempt    int    `json:"attempt"`
	LastError  string `json:"last_error,omitempty"`
}

// OperationEffectPayload is the data in an operation.effect webhook. Scope and
// identifiers are supplied by the control plane, not the handler response.
type OperationEffectPayload struct {
	OperationID      string          `json:"operation_id"`
	AppID            string          `json:"app_id"`
	PlatformTenantID string          `json:"platform_tenant_id,omitempty"`
	Generation       int64           `json:"generation"`
	Name             string          `json:"name"`
	Type             string          `json:"type"`
	Data             json.RawMessage `json:"data"`
}

type ExclusivePolicyRequest = ExclusiveOperationPolicy
