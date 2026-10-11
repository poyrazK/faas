package state

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

// Workflow proofs are deliberately separate from ordinary HTTP invocation claims.
type OperationWorkflowAuthority struct {
	AccountID, AppID, InstanceID, RunID, StepName string
	Capability                                    string `json:"-"`
	Generation, Attempt                           int
}

func (OperationWorkflowAuthority) String() string         { return "workflow operation authority (private)" }
func (a OperationWorkflowAuthority) GoString() string     { return a.String() }
func (a OperationWorkflowAuthority) LogValue() slog.Value { return slog.StringValue(a.String()) }

type OperationWorkflowArtifactReceipt struct {
	Artifact            api.OperationResultArtifact `json:"artifact"`
	Fingerprint         string                      `json:"fingerprint"`
	BlobID              string                      `json:"blob_id"`
	StepName            string                      `json:"step_name"`
	Generation, Attempt int
	Published           bool `json:"published"`
}

type OperationWorkflowArtifactStore interface {
	ReuseWorkflowOperationArtifact(context.Context, string, OperationWorkflowAuthority, api.OperationArtifactRequest) (api.OperationWorkflowArtifactResponse, error)
	ReserveWorkflowOperationArtifact(context.Context, string, OperationWorkflowAuthority, api.OperationArtifactRequest) (OperationResultBlob, Operation, error)
	PrepareVerifiedWorkflowOperationArtifact(context.Context, string, OperationWorkflowAuthority, api.OperationArtifactRequest, string) (api.OperationWorkflowArtifactResponse, error)
}

func validateOperationWorkflowAuthority(op Operation, run WorkflowRun, authority OperationWorkflowAuthority, nonce string) error {
	if err := validateOperationWorkflowExecutionAuthority(op, run, authority, nonce, false); err != nil {
		return err
	}
	var spec api.WorkflowSpec
	if err := json.Unmarshal(run.DefinitionSnapshot, &spec); err != nil {
		return err
	}
	// First slice: only the final action can prepare customer result files.
	if len(spec.Steps) == 0 || spec.Steps[len(spec.Steps)-1].Name != authority.StepName {
		return ErrInvalidArgument
	}
	return nil
}

func validateOperationWorkflowExecutionAuthority(op Operation, run WorkflowRun, authority OperationWorkflowAuthority, nonce string, observeCancellation bool) error {
	if op.AccountID != authority.AccountID || op.AppID != authority.AppID || op.WorkflowRunID == "" || op.WorkflowRunID != authority.RunID || op.CurrentInvocationID != "" {
		return ErrNotFound
	}
	if authority.Attempt > math.MaxInt32 {
		return ErrInvalidArgument
	}
	if op.State != api.OperationRunning || op.CancellationRequested && !observeCancellation || op.Generation != authority.Generation || run.ResumeCount+1 != authority.Generation || run.Status != WorkflowRunStatusRunning || authority.Attempt < 1 || nonce == "" || subtle.ConstantTimeCompare([]byte(nonce), []byte(authority.Capability)) != 1 {
		return ErrOperationStaleAttempt
	}
	return nil
}

func workflowArtifactReceipt(op Operation, a OperationWorkflowAuthority, req api.OperationArtifactRequest) (OperationWorkflowArtifactReceipt, bool, error) {
	upload := api.OperationArtifactUploadRequest{ReportID: req.ReportID, Name: req.Name, SizeBytes: req.SizeBytes, SHA256: req.SHA256}
	var err error
	if req.URI == operations.WorkflowUploadArtifactDeclaration(op.ID, a.RunID, a.StepName, upload).URI {
		err = operations.ValidateArtifactUpload(upload, op.PlanLimits)
	} else {
		err = operations.ValidateArtifact(req, op.PlanLimits)
		app, _, _, _ := operations.ParseArtifactURI(req.URI)
		if err == nil && app != op.AppID {
			return OperationWorkflowArtifactReceipt{}, false, ErrNotFound
		}
	}
	if err != nil {
		return OperationWorkflowArtifactReceipt{}, false, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	receipt, exists := op.WorkflowArtifactReceipts[req.ReportID]
	if exists && (receipt.Fingerprint != artifactFingerprint(req) || receipt.StepName != a.StepName) {
		return receipt, false, ErrOperationInputConflict
	}
	return receipt, exists, nil
}

func prepareWorkflowArtifact(op *Operation, a OperationWorkflowAuthority, req api.OperationArtifactRequest, blob OperationResultBlob, now time.Time) (api.OperationResultArtifact, error) {
	if _, exists, err := workflowArtifactReceipt(*op, a, req); err != nil {
		return api.OperationResultArtifact{}, err
	} else if exists {
		return api.OperationResultArtifact{}, ErrConflict
	}
	if len(op.WorkflowArtifactReceipts) >= op.PlanLimits.ArtifactsPerOperation {
		return api.OperationResultArtifact{}, NewOperationLimitError("artifacts_per_operation", int64(op.PlanLimits.ArtifactsPerOperation), int64(len(op.WorkflowArtifactReceipts)+1))
	}
	if op.ReportCount >= op.PlanLimits.ReportsPerOperation {
		return api.OperationResultArtifact{}, NewOperationLimitError("reports_per_operation", int64(op.PlanLimits.ReportsPerOperation), int64(op.ReportCount+1))
	}
	total := req.SizeBytes
	for _, receipt := range op.WorkflowArtifactReceipts {
		if receipt.Artifact.Name == req.Name {
			return api.OperationResultArtifact{}, ErrConflict
		}
		if receipt.Artifact.SizeBytes > op.PlanLimits.ArtifactTotalMaxBytes-total {
			return api.OperationResultArtifact{}, NewOperationLimitError("artifact_total_bytes", op.PlanLimits.ArtifactTotalMaxBytes, op.PlanLimits.ArtifactTotalMaxBytes+1)
		}
		total += receipt.Artifact.SizeBytes
	}
	if total > op.PlanLimits.ArtifactTotalMaxBytes {
		return api.OperationResultArtifact{}, NewOperationLimitError("artifact_total_bytes", op.PlanLimits.ArtifactTotalMaxBytes, total)
	}
	id := operations.WorkflowArtifactIdentity(op.ID, op.WorkflowRunID, a.StepName, req.ReportID)
	expiry := op.ExpiresAt
	artifact := api.OperationResultArtifact{ID: id, Name: req.Name, URI: req.URI, SizeBytes: req.SizeBytes, SHA256: req.SHA256, ExpiresAt: &expiry}
	if op.WorkflowArtifactReceipts == nil {
		op.WorkflowArtifactReceipts = map[string]OperationWorkflowArtifactReceipt{}
	}
	op.WorkflowArtifactReceipts[req.ReportID] = OperationWorkflowArtifactReceipt{Artifact: artifact, Fingerprint: artifactFingerprint(req), BlobID: blob.ID, StepName: a.StepName, Generation: a.Generation, Attempt: a.Attempt}
	bindOperationResultBlob(op, id, blob)
	op.ReportCount++
	return artifact, nil
}

func validateWorkflowArtifactBlob(blob OperationResultBlob, op Operation, a OperationWorkflowAuthority, req api.OperationArtifactRequest, now time.Time) error {
	if blob.OperationID != op.ID || blob.AccountID != op.AccountID || blob.WorkflowRunID != a.RunID || blob.WorkflowStep != a.StepName || blob.ExecutionID != "" || blob.Generation != a.Generation || blob.Attempt != a.Attempt || blob.ReportID != req.ReportID || blob.Fingerprint != artifactFingerprint(req) {
		return ErrConflict
	}
	if blob.State != "staging" || !blob.ExpiresAt.After(now) {
		return ErrOperationStaleAttempt
	}
	return nil
}

func rebindWorkflowArtifact(op *Operation, a OperationWorkflowAuthority, reportID string, receipt OperationWorkflowArtifactReceipt, blob OperationResultBlob) (api.OperationWorkflowArtifactResponse, error) {
	if blob.State != "retained" || blob.ID != receipt.BlobID || blob.OperationID != op.ID || blob.WorkflowRunID != a.RunID || blob.WorkflowStep != a.StepName || blob.Fingerprint != receipt.Fingerprint || op.ArtifactStorageKeys[receipt.Artifact.ID] != blob.StorageKey {
		return api.OperationWorkflowArtifactResponse{}, ErrConflict
	}
	receipt.Generation, receipt.Attempt = a.Generation, a.Attempt
	op.WorkflowArtifactReceipts[reportID] = receipt
	return api.OperationWorkflowArtifactResponse{Available: true, Artifact: &receipt.Artifact}, nil
}

func publishWorkflowArtifacts(op *Operation, run WorkflowRun, steps map[string]WorkflowStep, reconciled bool, now time.Time) []api.OperationEvent {
	var events []api.OperationEvent
	keys := make([]string, 0, len(op.WorkflowArtifactReceipts))
	for key := range op.WorkflowArtifactReceipts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		receipt := op.WorkflowArtifactReceipts[key]
		step := steps[receipt.StepName]
		confirmed := step.Status == WorkflowStepStatusSucceeded && step.Attempt == receipt.Attempt
		if receipt.Published || receipt.Generation != op.Generation || !reconciled && !confirmed {
			continue
		}
		receipt.Published = true
		op.Artifacts = append(op.Artifacts, receipt.Artifact)
		op.WorkflowArtifactReceipts[key] = receipt
		if !reconciled {
			events = append(events, operationWorkflowEvent(op, run, "artifact_attached", map[string]any{"artifact": receipt.Artifact, "workflow_step": receipt.StepName}, now))
		}
	}
	refreshOperationArtifactExpiry(op)
	return events
}
