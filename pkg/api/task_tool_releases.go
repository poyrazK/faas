package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// TaskToolReleaseApp pins a stack role to a native default-environment review.
type TaskToolReleaseApp struct {
	Role   string           `json:"role"`
	App    string           `json:"app"`
	Review AppReleaseReview `json:"review"`
}

// TaskToolReleaseIntent contains review metadata, never local paths, executable
// arguments, database URLs, keys, or credential references.
type TaskToolReleaseIntent struct {
	ReleaseFingerprint string               `json:"release_fingerprint"`
	WorkerRuntime      string               `json:"worker_runtime"`
	Protocol           string               `json:"protocol"`
	ObserverMetricApp  string               `json:"observer_metric_app"`
	Endpoint           string               `json:"endpoint"`
	AccessMode         string               `json:"access_mode"`
	ToolScopes         map[string][]string  `json:"tool_scopes"`
	Apps               []TaskToolReleaseApp `json:"apps"`
	RequiredSetup      []string             `json:"required_setup"`
	Reason             string               `json:"reason"`
}

// TaskToolReleaseIntentRecord retains review approval and optional separately
// authorized local execution. Root readiness flags are never execution authority.
type TaskToolReleaseIntentRecord struct {
	Version          int                       `json:"version"`
	ID               string                    `json:"id"`
	AccountID        string                    `json:"account_id"`
	RequesterKeyID   string                    `json:"requester_key_id"`
	IntentSHA256     string                    `json:"intent_sha256"`
	Intent           TaskToolReleaseIntent     `json:"intent"`
	Status           string                    `json:"status"`
	CreatedAt        time.Time                 `json:"created_at"`
	ExpiresAt        time.Time                 `json:"expires_at"`
	DecidedAt        *time.Time                `json:"decided_at,omitempty"`
	BindingsVerified bool                      `json:"bindings_verified"`
	ReadyToApply     bool                      `json:"ready_to_apply"`
	Execution        *TaskToolReleaseExecution `json:"execution,omitempty"`
}

// TaskToolReleaseExecution is separate, single-use owner authority for the
// trusted local Task adapter. A running claim is never automatically taken over.
type TaskToolReleaseExecution struct {
	OperationID      string                    `json:"operation_id"`
	BindingsSHA256   string                    `json:"bindings_sha256"`
	AuthorizerKeyID  string                    `json:"authorizer_key_id"`
	State            string                    `json:"state"`
	AuthorizedAt     time.Time                 `json:"authorized_at"`
	ClaimSHA256      string                    `json:"claim_sha256,omitempty"`
	Stage            string                    `json:"stage,omitempty"`
	Receipt          *TaskToolReleaseReceipt   `json:"receipt,omitempty"`
	CompletedStages  []string                  `json:"completed_stages,omitempty"`
	FenceVersion     int                       `json:"fence_version,omitempty"`
	ExecutorExitedAt *time.Time                `json:"executor_exited_at,omitempty"`
	Recovery         *TaskToolRecoveryHandover `json:"recovery,omitempty"`
}

// TaskToolReleaseReceipt is a claim-holder checkpoint, not a health verdict.
// Recovery inspection independently verifies these identities against native facts.
type TaskToolReleaseReceipt struct {
	Revision            uint64            `json:"revision"`
	ReleaseID           string            `json:"release_id"`
	Stage               string            `json:"stage"`
	ServingCaptured     bool              `json:"serving_captured"`
	ServingDeploymentID string            `json:"serving_deployment_id,omitempty"`
	WebDeploymentID     string            `json:"web_deployment_id,omitempty"`
	WorkerDeploymentID  string            `json:"worker_deployment_id,omitempty"`
	PendingSubmission   string            `json:"pending_submission,omitempty"`
	PreviousDeployments map[string]string `json:"previous_deployments"`
	WorkerIDs           []string          `json:"worker_ids"`
	Promoted            bool              `json:"promoted"`
	Parked              map[string]bool   `json:"parked"`
}

// TaskToolReleaseExecutionRequest names bounded adapter operations; ClaimToken
// is private local executor material and is never returned in a receipt.
type TaskToolReleaseExecutionRequest struct {
	Action         string                  `json:"action"`
	FenceVersion   int                     `json:"fence_version,omitempty"`
	OperationID    string                  `json:"operation_id"`
	BindingsSHA256 string                  `json:"bindings_sha256"`
	ClaimToken     string                  `json:"claim_token,omitempty"`
	Stage          string                  `json:"stage,omitempty"`
	Receipt        *TaskToolReleaseReceipt `json:"receipt,omitempty"`
}

// TaskToolReleaseIntentCheck supplies a freshly recomputed local fingerprint.
type TaskToolReleaseIntentCheck struct {
	ReleaseFingerprint string                           `json:"release_fingerprint"`
	Execution          *TaskToolReleaseExecutionRequest `json:"execution,omitempty"`
}

// TaskToolReleaseIntentDecision binds an owner decision to the complete intent.
type TaskToolReleaseIntentDecision struct {
	IntentSHA256       string                           `json:"intent_sha256"`
	ReleaseFingerprint string                           `json:"release_fingerprint"`
	Decision           string                           `json:"decision"`
	Execution          *TaskToolReleaseExecutionRequest `json:"execution,omitempty"`
	Recovery           *TaskToolRecoveryRequest         `json:"recovery,omitempty"`
	// AuthorizerKeyID is supplied only by the authenticated native handler.
	AuthorizerKeyID string `json:"-"`
}

func (c *Client) PrepareTaskToolReleaseIntent(ctx context.Context, id string, in TaskToolReleaseIntent) (TaskToolReleaseIntentRecord, error) {
	var out TaskToolReleaseIntentRecord
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPut, "/v1/task-tool-release-intents/"+url.PathEscape(id), in, &out)
	return out, err
}

func (c *Client) GetTaskToolReleaseIntent(ctx context.Context, id string) (TaskToolReleaseIntentRecord, error) {
	var out TaskToolReleaseIntentRecord
	err := c.do(ctx, http.MethodGet, "/v1/task-tool-release-intents/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) CheckTaskToolReleaseIntent(ctx context.Context, id string, in TaskToolReleaseIntentCheck) (TaskToolReleaseIntentRecord, error) {
	var out TaskToolReleaseIntentRecord
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, "/v1/task-tool-release-intents/"+url.PathEscape(id)+"/check", in, &out)
	return out, err
}

func (c *Client) DecideTaskToolReleaseIntent(ctx context.Context, id string, in TaskToolReleaseIntentDecision) (TaskToolReleaseIntentRecord, error) {
	var out TaskToolReleaseIntentRecord
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPut, "/v1/task-tool-release-intents/"+url.PathEscape(id)+"/decision", in, &out)
	return out, err
}

// TaskToolRecoveryHandover permanently fences the original executor. Settlement
// requires both the native write barrier and the trusted executor exit receipt.
// Approval is separate from release approval and does not expose an apply path.
type TaskToolRecoveryHandover struct {
	ID              string                         `json:"id"`
	OperationID     string                         `json:"operation_id"`
	Revision        uint64                         `json:"revision"`
	State           string                         `json:"state"`
	RequestedAt     time.Time                      `json:"requested_at"`
	SettledAt       *time.Time                     `json:"settled_at,omitempty"`
	Qualification   *TaskToolRecoveryQualification `json:"qualification,omitempty"`
	Decision        string                         `json:"decision,omitempty"`
	AuthorizerKeyID string                         `json:"authorizer_key_id,omitempty"`
	DecidedAt       *time.Time                     `json:"decided_at,omitempty"`
}

// TaskToolRecoveryQualification records an owner's local checks and a fresh
// native snapshot. It cannot renew its deadline or change after recording.
type TaskToolRecoveryQualification struct {
	HandoverID                  string               `json:"handover_id"`
	OperationID                 string               `json:"operation_id"`
	Revision                    uint64               `json:"revision"`
	Action                      string               `json:"action"`
	TargetDeploymentID          string               `json:"target_deployment_id"`
	ExpectedServingDeploymentID string               `json:"expected_serving_deployment_id"`
	BindingsSHA256              string               `json:"bindings_sha256"`
	EvidenceSHA256              string               `json:"evidence_sha256"`
	NativeApps                  []TaskToolReleaseApp `json:"native_apps"`
	ObservedAt                  time.Time            `json:"observed_at"`
	ExpiresAt                   time.Time            `json:"expires_at"`
}

// TaskToolRecoveryRequest is owner-only. The server supplies the actor identity.
type TaskToolRecoveryRequest struct {
	HandoverID          string                         `json:"handover_id"`
	OperationID         string                         `json:"operation_id"`
	Revision            uint64                         `json:"revision"`
	Qualification       *TaskToolRecoveryQualification `json:"qualification,omitempty"`
	QualificationSHA256 string                         `json:"qualification_sha256,omitempty"`
}

// Private trusted-executor request headers; never included in response DTOs.
const (
	TaskExecutionIntentHeader    = "X-Gregale-Task-Intent"
	TaskExecutionOperationHeader = "X-Gregale-Task-Operation"
	TaskExecutionClaimHeader     = "X-Gregale-Task-Claim"
)
