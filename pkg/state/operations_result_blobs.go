package state

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// OperationResultBlob is a private storage receipt. It survives deletion of
// its customer operation so abandoned uploads and account deletion can be GC'd.
type OperationResultBlob struct {
	ID, OperationID, AccountID, ExecutionID string
	WorkflowRunID, WorkflowStep             string
	JobRunID                                string
	Generation, Attempt                     int
	ReportID, Fingerprint, StorageKey       string
	SizeBytes                               int64
	State                                   string
	ExpiresAt, NextAttemptAt                time.Time
	LeaseToken                              string
	LeaseUntil                              *time.Time
}

type OperationResultBlobStore interface {
	ReserveOperationArtifact(context.Context, string, OperationExecutionAuthority, api.OperationArtifactRequest) (OperationResultBlob, error)
	ClaimOperationArtifactCleanup(context.Context, string, time.Time) (OperationResultBlob, error)
	RetryOperationArtifactCleanup(context.Context, string, string, time.Time) error
	CompleteOperationArtifactCleanup(context.Context, string, string) error
}

func newOperationResultBlob(op Operation, inv Invocation, req api.OperationArtifactRequest, now time.Time) OperationResultBlob {
	id := newOperationID()
	accountKey := op.AccountID
	if account, err := uuid.Parse(accountKey); err == nil {
		accountKey = account.String()
	}
	expiry := now.Add(api.OperationArtifactStagingLifetime)
	return OperationResultBlob{ID: id, OperationID: op.ID, AccountID: op.AccountID, Generation: op.Generation,
		ExecutionID: inv.ID, Attempt: inv.Attempts, ReportID: req.ReportID, Fingerprint: artifactFingerprint(req),
		StorageKey: fmt.Sprintf("operation-results/%s/%s/%s", accountKey, op.ID, id), SizeBytes: req.SizeBytes,
		State: "staging", ExpiresAt: expiry, NextAttemptAt: expiry}
}

func validateOperationResultBlob(blob OperationResultBlob, op Operation, inv Invocation, req api.OperationArtifactRequest, now time.Time) error {
	if blob.OperationID != op.ID || blob.AccountID != op.AccountID || blob.Generation != op.Generation ||
		blob.ExecutionID != inv.ID || blob.Attempt != inv.Attempts || blob.ReportID != req.ReportID || blob.Fingerprint != artifactFingerprint(req) {
		return ErrConflict
	}
	if blob.State != "staging" || !blob.ExpiresAt.After(now) {
		return ErrOperationStaleAttempt
	}
	return nil
}

func checkOperationBlobQuota(limits api.OperationPlanLimits, count, bytes, additional int64) error {
	if count >= int64(limits.RetainedArtifactsPerAccount) {
		return NewOperationLimitError("retained_artifacts_per_account", int64(limits.RetainedArtifactsPerAccount), count+1)
	}
	if additional > limits.RetainedArtifactBytesPerAccount-bytes {
		return NewOperationLimitError("retained_artifact_bytes_per_account", limits.RetainedArtifactBytesPerAccount, bytes+additional)
	}
	return nil
}

func bindOperationResultBlob(op *Operation, artifactID string, blob OperationResultBlob) {
	if op.ArtifactStorageKeys == nil {
		op.ArtifactStorageKeys = map[string]string{}
	}
	op.ArtifactStorageKeys[artifactID] = blob.StorageKey
}
