package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// OperationState describes business work, independently of execution and delivery.
type OperationState string

const (
	OperationAccepted               OperationState = "accepted"
	OperationRunning                OperationState = "running"
	OperationSucceeded              OperationState = "succeeded"
	OperationFailed                 OperationState = "failed"
	OperationCancelled              OperationState = "cancelled"
	OperationRequiresReconciliation OperationState = "requires_reconciliation"
	OperationRecoveryReconcile                     = "reconcile_on_unknown"
	OperationRecoverySafeRetry                     = "safe_retry"
	OperationOwnerPlatformTenant                   = "platform_tenant"
	OperationIDHeader                              = "X-Gregale-Customer-Operation-Id"
	OperationAttemptHeader                         = "X-Gregale-Operation-Attempt"
	OperationCapabilityHeader                      = "X-Gregale-Operation-Capability"
)

func (s OperationState) Terminal() bool {
	return s == OperationSucceeded || s == OperationFailed || s == OperationCancelled
}

// OperationDefinitionSpec is a resolved immutable contract. Schema documents
// are bundled with deployment; runtime validation never loads external URLs.
type OperationDefinitionSpec struct {
	Name                string          `json:"name" yaml:"name"`
	Method              string          `json:"method" yaml:"method"`
	Path                string          `json:"path" yaml:"path"`
	Owner               string          `json:"owner" yaml:"owner"`
	InputSchema         json.RawMessage `json:"input_schema"`
	OutputSchema        json.RawMessage `json:"output_schema"`
	ProgressStages      []string        `json:"progress_stages" yaml:"progress_stages"`
	CompletionWebhookID string          `json:"completion_webhook_id,omitempty" yaml:"completion_webhook_id,omitempty"`
	Recovery            string          `json:"recovery" yaml:"recovery"`
}

type OperationDefinitionResponse struct {
	ID           string                  `json:"id"`
	AppID        string                  `json:"app_id"`
	Scope        string                  `json:"scope"`
	Revision     string                  `json:"revision"`
	DeploymentID string                  `json:"deployment_id"`
	ReleaseID    string                  `json:"release_id,omitempty"`
	Spec         OperationDefinitionSpec `json:"spec"`
	CreatedAt    time.Time               `json:"created_at"`
}

type OperationDefinitionsResponse struct {
	Definitions []OperationDefinitionSummary `json:"definitions"`
}

// Fetch schemas through the single-definition read to keep maximum-plan
// collection responses within the existing SDK response bound.
type OperationDefinitionSummary struct {
	ID                  string    `json:"id"`
	AppID               string    `json:"app_id"`
	Scope               string    `json:"scope"`
	Revision            string    `json:"revision"`
	DeploymentID        string    `json:"deployment_id"`
	ReleaseID           string    `json:"release_id,omitempty"`
	Name                string    `json:"name"`
	Method              string    `json:"method"`
	Path                string    `json:"path"`
	Owner               string    `json:"owner"`
	ProgressStages      []string  `json:"progress_stages"`
	CompletionWebhookID string    `json:"completion_webhook_id,omitempty"`
	Recovery            string    `json:"recovery"`
	CreatedAt           time.Time `json:"created_at"`
}

// OperationTenantIdentity binds local submission receipts to the authenticated
// tenant without persisting a credential or relying on a caller-supplied owner.
type OperationTenantIdentity struct {
	AccountID        string `json:"account_id"`
	PlatformTenantID string `json:"platform_tenant_id"`
}

type OperationProgress struct {
	Stage     string    `json:"stage"`
	Completed int64     `json:"completed"`
	Total     int64     `json:"total"`
	Attempt   int       `json:"attempt"`
	UpdatedAt time.Time `json:"updated_at"`
}

type OperationReportRequest struct {
	ReportID  string `json:"report_id"`
	Stage     string `json:"stage"`
	Completed int64  `json:"completed"`
	Total     int64  `json:"total"`
}

type OperationResultArtifact struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	URI       string     `json:"uri"`
	SizeBytes int64      `json:"size_bytes"`
	SHA256    string     `json:"sha256"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// A handler attaches an existing managed object after Gregale verifies its
// bytes. The reference retains the expected digest, never a signed URL.
type OperationArtifactRequest struct {
	ReportID  string `json:"report_id"`
	Name      string `json:"name"`
	URI       string `json:"uri"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

type OperationDeliveryResponse struct {
	State         string     `json:"state"`
	DeliveryID    string     `json:"delivery_id,omitempty"`
	Attempts      int        `json:"attempts"`
	LastError     string     `json:"last_error,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
}

type OperationResponse struct {
	ID                    string                    `json:"id"`
	Name                  string                    `json:"name"`
	Generation            int                       `json:"generation"`
	State                 OperationState            `json:"state"`
	Progress              *OperationProgress        `json:"progress,omitempty"`
	Result                json.RawMessage           `json:"result,omitempty"`
	Artifacts             []OperationResultArtifact `json:"artifacts,omitempty"`
	CompletionDelivery    OperationDeliveryResponse `json:"completion_delivery"`
	CancellationRequested bool                      `json:"cancellation_requested"`
	FailureCode           string                    `json:"failure_code,omitempty"`
	LatestSequence        int64                     `json:"latest_sequence"`
	CreatedAt             time.Time                 `json:"created_at"`
	UpdatedAt             time.Time                 `json:"updated_at"`
	ExpiresAt             time.Time                 `json:"expires_at"`
}

type OperationAcceptedResponse struct {
	ID        string `json:"id"`
	StatusURL string `json:"status_url"`
	EventsURL string `json:"events_url"`
}

// OperationSummary deliberately excludes input, result bytes, artifact locations,
// delivery errors and execution authority. Fetch detail separately to reopen work.
type OperationSummary struct {
	PlatformTenantID      string                   `json:"platform_tenant_id,omitempty"` // Account operator listings only.
	ID                    string                   `json:"id"`
	Name                  string                   `json:"name"`
	Generation            int                      `json:"generation"`
	State                 OperationState           `json:"state"`
	Progress              *OperationProgress       `json:"progress,omitempty"`
	CompletionDelivery    OperationDeliverySummary `json:"completion_delivery"`
	CancellationRequested bool                     `json:"cancellation_requested"`
	LatestSequence        int64                    `json:"latest_sequence"`
	CreatedAt             time.Time                `json:"created_at"`
	UpdatedAt             time.Time                `json:"updated_at"`
	ExpiresAt             time.Time                `json:"expires_at"`
}

type OperationDeliverySummary struct {
	State         string     `json:"state"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
}

type OperationListResponse struct {
	Operations []OperationSummary `json:"operations"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

// AppID and Scope are explicit selectors, never sources of customer authority.
type OperationListOptions struct {
	TenantID string // Optional account operator filter; ignored by tenant-self clients.
	AppID    string
	Scope    string
	Name     string
	State    OperationState
	Limit    int
	Cursor   string
}

// OperationExecution summarizes a retained execution generation without payload,
// headers, instance credentials or runtime capability. Attempts is the execution
// ledger's attempt count, not a fabricated per-attempt outcome history.
type OperationExecution struct {
	Generation   int        `json:"generation"`
	InvocationID string     `json:"invocation_id"`
	State        string     `json:"state"`
	Attempts     int        `json:"attempts"`
	CreatedAt    time.Time  `json:"created_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

type OperationExecutionsResponse struct {
	Executions     []OperationExecution `json:"executions"`
	NextGeneration int                  `json:"next_generation,omitempty"`
}

type OperationEvent struct {
	OperationID string          `json:"operation_id"`
	Sequence    int64           `json:"sequence"`
	Type        string          `json:"type"`
	ExecutionID string          `json:"execution_id,omitempty"`
	Attempt     int             `json:"attempt,omitempty"`
	Data        json.RawMessage `json:"data"`
	CreatedAt   time.Time       `json:"created_at"`
}

type OperationEventsResponse struct {
	Events         []OperationEvent `json:"events"`
	LatestSequence int64            `json:"latest_sequence"`
	ResyncRequired bool             `json:"resync_required"`
}

// OperationRecoveryRequest requires an explicit resolution and evidence.
// safe_to_retry admits a fresh execution, retaining logical operation identity.
type OperationRecoveryRequest struct {
	RecoveryID         string          `json:"recovery_id"`
	ExpectedGeneration int             `json:"expected_generation"`
	Resolution         string          `json:"resolution"`
	Evidence           string          `json:"evidence"`
	Result             json.RawMessage `json:"result,omitempty"`
}

type OperationStartRequest struct {
	DefinitionID string          `json:"definition_id"`
	Input        json.RawMessage `json:"input"`
}

type OperationCancellationRequest struct {
	ExpectedGeneration int `json:"expected_generation"`
}

// OperationLimitProblem extracts public numeric bounds from a quota error
// without coupling the API package to an execution store.
func OperationLimitProblem(err error) *Problem {
	problem := NewProblem(http.StatusTooManyRequests, "operation_limit_exceeded", "Operation limit exceeded", "the operation plan or reporting limit was reached").WithDocs("https://gregale.dev/docs/operations#limits")
	var limit interface{ OperationLimit() (string, int64, int64) }
	if errors.As(err, &limit) {
		kind, maximum, observed := limit.OperationLimit()
		problem.Detail = "operation limit reached: " + kind
		problem = problem.WithLimit(maximum, observed)
	}
	return problem
}

// IsReservedOperationHeader covers customer and managed execution context.
// Public ingress strips the complete namespace, including future adapters.
func IsReservedOperationHeader(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "x-gregale-operation-") || strings.HasPrefix(lower, "x-gregale-customer-operation-")
}
