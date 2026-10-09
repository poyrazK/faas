// adr: 665
package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

type OperationJobArtifactReceipt struct {
	Artifact    api.OperationResultArtifact `json:"artifact"`
	Fingerprint string                      `json:"fingerprint"`
	BlobID      string                      `json:"blob_id"`
	RunID       string                      `json:"run_id"`
	Generation  int                         `json:"generation"`
	Attempt     int                         `json:"attempt"`
	Published   bool                        `json:"published"`
}

type OperationJobArtifactStore interface {
	ReuseJobOperationArtifact(context.Context, string, JobOperationAuthority, api.OperationArtifactRequest) (api.OperationJobArtifactResponse, error)
	ReserveJobOperationArtifact(context.Context, string, JobOperationAuthority, api.OperationArtifactRequest) (OperationResultBlob, Operation, error)
	PrepareVerifiedJobOperationArtifact(context.Context, string, JobOperationAuthority, api.OperationArtifactRequest, string) (api.OperationJobArtifactResponse, error)
}

func jobArtifactReceipt(op Operation, a JobOperationAuthority, req api.OperationArtifactRequest) (OperationJobArtifactReceipt, bool, error) {
	upload := api.OperationArtifactUploadRequest{ReportID: req.ReportID, Name: req.Name, SizeBytes: req.SizeBytes, SHA256: req.SHA256}
	var err error
	if req.URI == operations.JobUploadArtifactDeclaration(op.ID, a.RunID, a.Attempt, upload).URI {
		err = operations.ValidateArtifactUpload(upload, op.PlanLimits)
	} else {
		err = operations.ValidateArtifact(req, op.PlanLimits)
		app, _, _, _ := operations.ParseArtifactURI(req.URI)
		if err == nil && app != op.AppID {
			return OperationJobArtifactReceipt{}, false, ErrNotFound
		}
	}
	if err != nil {
		return OperationJobArtifactReceipt{}, false, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	fingerprint := artifactFingerprint(req)
	if prior, ok := op.JobReports[req.ReportID]; ok && prior != "artifact:"+fingerprint {
		return OperationJobArtifactReceipt{}, false, ErrOperationInputConflict
	}
	receipt, exists := op.JobArtifactReceipts[req.ReportID]
	if exists && (receipt.Fingerprint != fingerprint || receipt.RunID != a.RunID || receipt.Generation != a.Generation || receipt.Attempt != a.Attempt) {
		return receipt, false, ErrOperationInputConflict
	}
	// Allow a committed replay after cancellation, but forbid a new write.
	if !exists && op.CancellationRequested {
		return receipt, false, ErrConflict
	}
	return receipt, exists, nil
}

func prepareJobArtifact(op *Operation, a JobOperationAuthority, req api.OperationArtifactRequest, blob OperationResultBlob) (api.OperationResultArtifact, error) {
	if _, exists, err := jobArtifactReceipt(*op, a, req); err != nil {
		return api.OperationResultArtifact{}, err
	} else if exists {
		return api.OperationResultArtifact{}, ErrConflict
	}
	if len(op.JobArtifactReceipts) >= op.PlanLimits.ArtifactsPerOperation {
		return api.OperationResultArtifact{}, NewOperationLimitError("artifacts_per_operation", int64(op.PlanLimits.ArtifactsPerOperation), int64(len(op.JobArtifactReceipts)+1))
	}
	if op.ReportCount >= op.PlanLimits.ReportsPerOperation {
		return api.OperationResultArtifact{}, NewOperationLimitError("reports_per_operation", int64(op.PlanLimits.ReportsPerOperation), int64(op.ReportCount+1))
	}
	total := req.SizeBytes
	for _, receipt := range op.JobArtifactReceipts {
		if receipt.Artifact.Name == req.Name {
			return api.OperationResultArtifact{}, ErrConflict
		}
		// Subtraction avoids overflow for corrupt or oversized declarations.
		if receipt.Artifact.SizeBytes > op.PlanLimits.ArtifactTotalMaxBytes-total {
			return api.OperationResultArtifact{}, NewOperationLimitError("artifact_total_bytes", op.PlanLimits.ArtifactTotalMaxBytes, op.PlanLimits.ArtifactTotalMaxBytes+1)
		}
		total += receipt.Artifact.SizeBytes
	}
	if total > op.PlanLimits.ArtifactTotalMaxBytes {
		return api.OperationResultArtifact{}, NewOperationLimitError("artifact_total_bytes", op.PlanLimits.ArtifactTotalMaxBytes, total)
	}
	id := operations.ArtifactIdentity(op.ID, a.RunID, a.Attempt, req.ReportID)
	expiry := op.ExpiresAt
	artifact := api.OperationResultArtifact{ID: id, Name: req.Name, URI: req.URI, SizeBytes: req.SizeBytes, SHA256: req.SHA256, ExpiresAt: &expiry}
	if op.JobArtifactReceipts == nil {
		op.JobArtifactReceipts = map[string]OperationJobArtifactReceipt{}
	}
	if op.JobReports == nil {
		op.JobReports = map[string]string{}
	}
	op.JobArtifactReceipts[req.ReportID] = OperationJobArtifactReceipt{Artifact: artifact, Fingerprint: artifactFingerprint(req), BlobID: blob.ID, RunID: a.RunID, Generation: a.Generation, Attempt: a.Attempt}
	op.JobReports[req.ReportID] = "artifact:" + artifactFingerprint(req)
	bindOperationResultBlob(op, id, blob)
	op.ReportCount++
	return artifact, nil
}

func validateJobArtifactBlob(blob OperationResultBlob, op Operation, a JobOperationAuthority, req api.OperationArtifactRequest, now time.Time) error {
	if blob.OperationID != op.ID || blob.AccountID != op.AccountID || blob.JobRunID != a.RunID || blob.ExecutionID != "" || blob.WorkflowRunID != "" || blob.WorkflowStep != "" || blob.Generation != a.Generation || blob.Attempt != a.Attempt || blob.ReportID != req.ReportID || blob.Fingerprint != artifactFingerprint(req) || blob.SizeBytes != req.SizeBytes {
		return ErrConflict
	}
	if blob.State != "staging" || !blob.ExpiresAt.After(now) {
		return ErrOperationStaleAttempt
	}
	return nil
}

func reuseJobArtifact(op Operation, a JobOperationAuthority, receipt OperationJobArtifactReceipt, blob OperationResultBlob) (api.OperationJobArtifactResponse, error) {
	if blob.State != "retained" || blob.ID != receipt.BlobID || blob.OperationID != op.ID || blob.AccountID != op.AccountID || blob.JobRunID != a.RunID || blob.ExecutionID != "" || blob.WorkflowRunID != "" || blob.WorkflowStep != "" || blob.Generation != a.Generation || blob.Attempt != a.Attempt || blob.Fingerprint != receipt.Fingerprint || blob.SizeBytes != receipt.Artifact.SizeBytes || op.ArtifactStorageKeys[receipt.Artifact.ID] != blob.StorageKey {
		return api.OperationJobArtifactResponse{}, ErrConflict
	}
	return api.OperationJobArtifactResponse{Available: true, Artifact: &receipt.Artifact}, nil
}

func newJobArtifactBlob(op Operation, a JobOperationAuthority, req api.OperationArtifactRequest, now time.Time) OperationResultBlob {
	// Only the shared storage intent factory is reused; Job authority remains native.
	blob := newOperationResultBlob(op, Invocation{Attempts: a.Attempt}, req, now)
	blob.JobRunID = a.RunID
	return blob
}

func publishJobArtifacts(op *Operation, task JobTask, reconciled bool, now time.Time) []api.OperationEvent {
	keys := make([]string, 0, len(op.JobArtifactReceipts))
	for key := range op.JobArtifactReceipts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var events []api.OperationEvent
	for _, key := range keys {
		receipt := op.JobArtifactReceipts[key]
		if receipt.Published || receipt.RunID != task.RunID || receipt.Generation != op.Generation || receipt.Attempt != task.Attempt {
			continue
		}
		receipt.Published = true
		op.JobArtifactReceipts[key] = receipt
		op.Artifacts = append(op.Artifacts, receipt.Artifact)
		if !reconciled {
			events = append(events, operationJobEvent(op, task, "artifact_attached", map[string]any{"artifact": receipt.Artifact}, now))
		}
	}
	refreshOperationArtifactExpiry(op)
	return events
}
