package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

var (
	ErrOperationRecoveryReceiptUnavailable = errors.New("state: accepted recovery predates immutable decision receipts")
	ErrOperationIdentityConflict           = errors.New("state: operation authenticated identity changed")
	ErrOperationInputConflict              = errors.New("state: operation key conflicts with input")
	ErrOperationExpired                    = errors.New("state: operation result expired within deduplication window")
	ErrOperationStaleAttempt               = errors.New("state: operation report belongs to a stale execution attempt")
	ErrOperationQuota                      = errors.New("state: operation plan limit exceeded")
)

type OperationDefinition struct {
	api.OperationDefinitionResponse
	AccountID string `json:"account_id"`
}

// Operation is the internal durable projection. Customer responses omit owner,
// execution authority and source input; callers use the scoped read boundary.
type Operation struct {
	api.OperationResponse
	AccountID                 string                                      `json:"account_id"`
	AppID                     string                                      `json:"app_id"`
	Scope                     string                                      `json:"scope"`
	PlatformTenantID          string                                      `json:"platform_tenant_id"`
	DefinitionID              string                                      `json:"definition_id"`
	DefinitionRevision        string                                      `json:"definition_revision"`
	DeploymentID              string                                      `json:"deployment_id"`
	ReleaseID                 string                                      `json:"release_id,omitempty"`
	CurrentInvocationID       string                                      `json:"current_invocation_id"`
	JobRunID                  string                                      `json:"job_run_id,omitempty"`
	JobSnapshot               *JobRun                                     `json:"job_snapshot,omitempty"`
	JobInput                  json.RawMessage                             `json:"job_input,omitempty"`
	JobResultReceipt          json.RawMessage                             `json:"job_result_receipt,omitempty"`
	JobReports                map[string]string                           `json:"job_reports,omitempty"`
	WorkflowRunID             string                                      `json:"workflow_run_id,omitempty"`
	ExecutionAttempt          int                                         `json:"execution_attempt"`
	ExecutionCapabilityDigest string                                      `json:"execution_capability_digest,omitempty"`
	ReportCount               int                                         `json:"report_count"`
	RecoveryCount             int                                         `json:"recovery_count"`
	PlanLimits                api.OperationPlanLimits                     `json:"plan_limits"`
	ValueMaxBytes             int                                         `json:"value_max_bytes"`
	EventExpiresAt            time.Time                                   `json:"event_expires_at"`
	ArtifactStorageKeys       map[string]string                           `json:"artifact_storage_keys,omitempty"`
	WorkflowArtifactReceipts  map[string]OperationWorkflowArtifactReceipt `json:"workflow_artifact_receipts,omitempty"`
	JobArtifactReceipts       map[string]OperationJobArtifactReceipt      `json:"job_artifact_receipts,omitempty"`
	MilestoneCount            int                                         `json:"milestone_count,omitempty"`
}

// operationRecordJSON adds the normalized backend identity consumed by the
// SQL ownership constraints. Keep it derived from the existing execution
// fields so every projection update carries the same identity as admission.
func operationRecordJSON(op Operation) ([]byte, error) {
	var executionID, executionKind string
	switch {
	case op.CurrentInvocationID != "" && op.WorkflowRunID == "" && op.JobRunID == "":
		executionID, executionKind = op.CurrentInvocationID, "http"
	case op.CurrentInvocationID == "" && op.WorkflowRunID != "" && op.JobRunID == "":
		executionID, executionKind = op.WorkflowRunID, "workflow"
	case op.CurrentInvocationID == "" && op.WorkflowRunID == "" && op.JobRunID != "":
		executionID, executionKind = op.JobRunID, "job"
	default:
		return nil, fmt.Errorf("state: operation %s has ambiguous backend identity", op.ID)
	}
	type operationRecord Operation
	return json.Marshal(struct {
		operationRecord
		CurrentExecutionID string `json:"current_execution_id"`
		ExecutionKind      string `json:"execution_kind"`
	}{operationRecord: operationRecord(op), CurrentExecutionID: executionID, ExecutionKind: executionKind})
}

type OperationAdmission struct {
	AccountID        string
	DefinitionID     string
	PlatformTenantID string
	IdempotencyKey   string
	ReleaseID        string
	Input            json.RawMessage
	ExpectedScope    *api.OperationSubmissionScope
	ExpectedIdentity *api.OperationTenantIdentity
}

type operationIdentityReceipt struct {
	AccountID   string
	AppID       string
	OperationID string
	Fingerprint string
	ExpiresAt   time.Time
}

// OperationStore is a narrow transactional seam, separate from execution
// Store. Implementations commit invocation creation and its operation together.
type OperationStore interface {
	PutOperationDefinition(context.Context, OperationDefinition) (OperationDefinition, error)
	OperationDefinitionByID(context.Context, string, string) (OperationDefinition, error)
	OperationDefinitionForDeployment(context.Context, string, string, string, string) (OperationDefinition, error)
	OperationDefinitionForRoute(context.Context, string, string, string, string, string) (OperationDefinition, error)
	OperationDefinitionsForDeployment(context.Context, string, string, string) ([]OperationDefinition, error)
	AdmitOperation(context.Context, OperationAdmission) (Operation, bool, error)
	OperationByID(context.Context, string, string, string) (Operation, error)
	ListPlatformTenantOperations(context.Context, string, string, api.OperationListOptions) (api.OperationListResponse, error)
	ListAccountOperations(context.Context, string, api.OperationListOptions) (api.OperationListResponse, error)
	OperationExecutions(context.Context, string, string, int, int) (api.OperationExecutionsResponse, error)
	OperationEvents(context.Context, string, string, string, int64, int) (api.OperationEventsResponse, error)
	ReportOperationProgress(context.Context, string, OperationExecutionAuthority, api.OperationReportRequest) (Operation, error)
	RecoverOperation(context.Context, string, string, string, api.OperationRecoveryRequest) (Operation, error)
	CancelOperation(context.Context, string, string, string, int) (Operation, error)
}

func prepareOperationAdmission(def OperationDefinition, admission OperationAdmission, limits api.Limits, now time.Time) (Operation, Invocation, string, string, error) {
	if err := validateOperationExpectedIdentity(admission.ExpectedIdentity, admission.AccountID, admission.PlatformTenantID); err != nil {
		return Operation{}, Invocation{}, "", "", err
	}
	if scope := admission.ExpectedScope; scope != nil && (!sameOperationHistoryIdentity(scope.AppID, def.AppID) || scope.Scope != def.Scope || scope.Name != def.Spec.Name) {
		return Operation{}, Invocation{}, "", "", ErrConflict
	}
	if admission.AccountID != def.AccountID || admission.PlatformTenantID == "" {
		return Operation{}, Invocation{}, "", "", ErrNotFound
	}
	if len(admission.IdempotencyKey) == 0 || len(admission.IdempotencyKey) > api.OperationIdempotencyKeyMaxBytes || strings.ContainsAny(admission.IdempotencyKey, "\x00\r\n") {
		return Operation{}, Invocation{}, "", "", ErrInvalidArgument
	}
	if len(admission.Input) > api.OperationSubmissionMaxBytes {
		return Operation{}, Invocation{}, "", "", NewOperationLimitError("submission_bytes", api.OperationSubmissionMaxBytes, int64(len(admission.Input)))
	}
	canonical, err := operations.CanonicalJSON(admission.Input)
	if err != nil {
		return Operation{}, Invocation{}, "", "", fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	fingerprint, err := operations.InputFingerprint(canonical)
	if err != nil {
		return Operation{}, Invocation{}, "", "", err
	}
	key := operations.IdentityScope(def.AccountID, def.AppID, def.Scope, admission.PlatformTenantID, def.Spec.Name, admission.IdempotencyKey)
	operationID, invocationID := newOperationID(), newOperationID()
	headers := map[string]string{"Content-Type": "application/json"}
	if def.Spec.TransactionReceipt == api.OperationTransactionPostgres {
		headers[api.OperationReceiptVersionHeader] = "1"
	}
	release := def.ReleaseID
	if admission.ReleaseID != "" {
		release = admission.ReleaseID
	}
	if release != "" {
		headers[api.ReleaseHeader] = release
	} else {
		headers[api.RevisionHeader] = def.DeploymentID
	}
	encodedHeaders, err := json.Marshal(headers)
	if err != nil {
		return Operation{}, Invocation{}, "", "", err
	}
	deadline := now.Add(time.Duration(limits.MaxAsyncInvocationDeadlineSeconds) * time.Second)
	retention := now.Add(time.Duration(limits.Operations.ResultRetentionSeconds) * time.Second)
	inv := Invocation{ID: invocationID, OperationID: operationID, AppID: def.AppID, AccountID: def.AccountID, PlatformTenantID: admission.PlatformTenantID,
		Source: InvocationAsyncInvoke, State: InvocationPending, Method: def.Spec.Method, Path: def.Spec.Path, Payload: canonical,
		Headers: encodedHeaders, DueAt: now, DeadlineAt: &deadline, ResultRetentionUntil: &retention, CreatedAt: now}
	op := Operation{OperationResponse: api.OperationResponse{ID: operationID, Name: def.Spec.Name, Generation: 1, State: api.OperationAccepted,
		CompletionDelivery: api.OperationDeliveryResponse{State: "not_requested"}, LatestSequence: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: retention},
		AccountID: def.AccountID, AppID: def.AppID, Scope: def.Scope, PlatformTenantID: admission.PlatformTenantID, DefinitionID: def.ID,
		DefinitionRevision: def.Revision, DeploymentID: def.DeploymentID, ReleaseID: release, CurrentInvocationID: invocationID, PlanLimits: limits.Operations, ValueMaxBytes: limits.MaxSourceBytesPerInvocation,
		EventExpiresAt: now.Add(time.Duration(limits.Operations.EventRetentionSeconds) * time.Second)}
	if def.Spec.CompletionWebhookID != "" {
		op.CompletionDelivery.State = "awaiting_outcome"
	}
	return op, inv, key, fingerprint, nil
}

func initialOperationEvent(op Operation) api.OperationEvent {
	return api.OperationEvent{OperationID: op.ID, Sequence: 1, Type: "accepted", ExecutionID: op.CurrentInvocationID,
		Data: json.RawMessage(`{"state":"accepted"}`), CreatedAt: op.CreatedAt}
}

func validateNewOperationInput(def OperationDefinition, input json.RawMessage, limits api.Limits) error {
	contract, err := operations.Compile(def.Spec, limits.Operations)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	if contract.Revision != def.Revision {
		return fmt.Errorf("%w: immutable definition revision mismatch", ErrConflict)
	}
	if err := contract.ValidateInput(input, limits.MaxSourceBytesPerInvocation); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	return nil
}

func cloneOperation(op Operation) Operation {
	encoded, _ := json.Marshal(op)
	var copy Operation
	_ = json.Unmarshal(encoded, &copy)
	return copy
}

func cloneOperationDefinition(def OperationDefinition) OperationDefinition {
	encoded, _ := json.Marshal(def)
	var copy OperationDefinition
	_ = json.Unmarshal(encoded, &copy)
	return copy
}

func newOperationID() string { return uuid.NewString() }

// OperationExecutionAuthority is constructed by the workload-authenticated
// boundary, never from customer identity fields in a report body.
type OperationExecutionAuthority struct {
	AccountID    string
	AppID        string
	InstanceID   string
	InvocationID string
	Attempt      int
	Capability   string
}

// OperationLimitError preserves the sentinel contract while exposing bounded
// public quota values for RFC 9457 responses.
type OperationLimitError struct {
	Kind            string
	Limit, Observed int64
}

func (e *OperationLimitError) Error() string {
	return fmt.Sprintf("%s: %s (limit %d, observed %d)", ErrOperationQuota, e.Kind, e.Limit, e.Observed)
}
func (e *OperationLimitError) Unwrap() error { return ErrOperationQuota }
func (e *OperationLimitError) OperationLimit() (string, int64, int64) {
	return e.Kind, e.Limit, e.Observed
}
func NewOperationLimitError(kind string, limit, observed int64) error {
	return &OperationLimitError{Kind: kind, Limit: limit, Observed: observed}
}
