// adr: 660
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// OperationRecoveryDecision is an immutable acknowledgement, not current status.
type OperationRecoveryDecision struct {
	OperationID                string         `json:"operation_id"`
	RecoveryID                 string         `json:"recovery_id"`
	RequestFingerprint         string         `json:"request_fingerprint"`
	ExpectedGeneration         int            `json:"expected_generation"`
	Generation                 int            `json:"generation"`
	ExpectedInspectionRevision string         `json:"expected_inspection_revision,omitempty"`
	Resolution                 string         `json:"resolution"`
	State                      OperationState `json:"state"`
	InvocationID               string         `json:"invocation_id,omitempty"`
	JobRunID                   string         `json:"job_run_id,omitempty"`
	WorkflowRunID              string         `json:"workflow_run_id,omitempty"`
	RecordedAt                 time.Time      `json:"recorded_at"`
	ExpiresAt                  time.Time      `json:"expires_at"`
}

func (c *Client) RecoverOperationWithReceipt(ctx context.Context, slug, id string, req OperationRecoveryRequest) (OperationRecoveryDecision, error) {
	var out OperationRecoveryDecision
	err := c.do(ctx, http.MethodPost, operationAppPath(slug, id)+"/recover-receipt", req, &out)
	return out, err
}

// Inspection omits request/output bytes, raw errors and execution/storage secrets.
type OperationRecoveryInspection struct {
	OperationID           string                      `json:"operation_id"`
	Generation            int                         `json:"generation"`
	State                 OperationState              `json:"state"`
	FailureCode           string                      `json:"failure_code,omitempty"`
	CancellationRequested bool                        `json:"cancellation_requested"`
	ExecutionKind         string                      `json:"execution_kind"`
	ExecutionState        string                      `json:"execution_state"`
	InvocationID          string                      `json:"invocation_id,omitempty"`
	JobRunID              string                      `json:"job_run_id,omitempty"`
	WorkflowRunID         string                      `json:"workflow_run_id,omitempty"`
	Attempt               int                         `json:"attempt"`
	DeploymentID          string                      `json:"deployment_id"`
	ReleaseID             string                      `json:"release_id,omitempty"`
	Steps                 []OperationRecoveryStep     `json:"steps"`
	Artifacts             []OperationRecoveryArtifact `json:"artifacts"`
	RetryBlockers         []string                    `json:"retry_blockers"`
	InspectionRevision    string                      `json:"inspection_revision"`
	ObservedAt            time.Time                   `json:"observed_at"`
}

type OperationRecoveryStep struct {
	Name           string `json:"name"`
	State          string `json:"state"`
	Attempt        int    `json:"attempt"`
	Confirmed      bool   `json:"confirmed"`
	OutcomeUnknown bool   `json:"outcome_unknown"`
}

type OperationRecoveryArtifact struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	WorkflowStep string    `json:"workflow_step,omitempty"`
	Generation   int       `json:"generation"`
	Attempt      int       `json:"attempt"`
	State        string    `json:"state"` // prepared or attached; no download authority.
	Retained     bool      `json:"retained"`
	SizeBytes    int64     `json:"size_bytes"`
	SHA256       string    `json:"sha256"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// A preview proposes a resolution, not provider evidence or a durable decision.
type OperationRecoveryPreviewRequest struct {
	ExpectedGeneration int             `json:"expected_generation"`
	Resolution         string          `json:"resolution"`
	Result             json.RawMessage `json:"result,omitempty"`
}

type OperationRecoveryPreview struct {
	Inspection               OperationRecoveryInspection `json:"inspection"`
	Resolution               string                      `json:"resolution"`
	Eligible                 bool                        `json:"eligible"`
	EvidenceRequired         bool                        `json:"evidence_required"`
	Blockers                 []string                    `json:"blockers"`
	ReusedSteps              []string                    `json:"reused_steps"`
	ReopenedSteps            []string                    `json:"reopened_steps"`
	ReusableArtifactIDs      []string                    `json:"reusable_artifact_ids"`
	PublishArtifactIDs       []string                    `json:"publish_artifact_ids"`
	StartsNewExecution       bool                        `json:"starts_new_execution"`
	ClearsArtifactReferences bool                        `json:"clears_artifact_references"`
}

func (c *Client) InspectOperationRecovery(ctx context.Context, slug, id string) (OperationRecoveryInspection, error) {
	var out OperationRecoveryInspection
	err := c.do(ctx, http.MethodGet, operationAppPath(slug, id)+"/recovery-inspection", nil, &out)
	return out, err
}

func (c *Client) PreviewOperationRecovery(ctx context.Context, slug, id string, req OperationRecoveryPreviewRequest) (OperationRecoveryPreview, error) {
	var out OperationRecoveryPreview
	err := c.do(ctx, http.MethodPost, operationAppPath(slug, id)+"/recovery-preview", req, &out)
	return out, err
}
