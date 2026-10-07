// adr: 669
package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func validateHTTPArtifactDeclaration(op Operation, inv Invocation, req api.OperationArtifactRequest) error {
	upload := api.OperationArtifactUploadRequest{ReportID: req.ReportID, Name: req.Name, SizeBytes: req.SizeBytes, SHA256: req.SHA256}
	if req.URI == operations.UploadArtifactDeclaration(op.ID, inv.ID, inv.Attempts, upload).URI {
		if err := operations.ValidateArtifactUpload(upload, op.PlanLimits); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
		}
		return nil
	}
	if err := operations.ValidateArtifact(req, op.PlanLimits); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	app, _, _, _ := operations.ParseArtifactURI(req.URI)
	if app != op.AppID {
		return ErrNotFound
	}
	return nil
}

func httpUploadReceipt(op Operation, inv Invocation, req api.OperationArtifactRequest, blob OperationResultBlob) (api.OperationArtifactUploadResponse, error) {
	id := operations.ArtifactIdentity(op.ID, inv.ID, inv.Attempts, req.ReportID)
	if blob.State != "retained" || blob.StorageKey == "" || op.ArtifactStorageKeys[id] != blob.StorageKey ||
		blob.OperationID != op.ID || blob.AccountID != op.AccountID || blob.Generation != op.Generation ||
		blob.ExecutionID != inv.ID || blob.Attempt != inv.Attempts || blob.ReportID != req.ReportID ||
		blob.Fingerprint != artifactFingerprint(req) || blob.SizeBytes != req.SizeBytes {
		return api.OperationArtifactUploadResponse{}, ErrConflict
	}
	for _, artifact := range op.Artifacts {
		if artifact.ID == id && artifact.URI == req.URI && artifact.Name == req.Name && artifact.SizeBytes == req.SizeBytes && artifact.SHA256 == req.SHA256 {
			return api.OperationArtifactUploadResponse{Available: true, Artifact: &artifact}, nil
		}
	}
	return api.OperationArtifactUploadResponse{}, ErrConflict
}

func (m *MemStore) ReuseOperationArtifact(_ context.Context, id string, a OperationExecutionAuthority, req api.OperationArtifactRequest) (api.OperationArtifactUploadResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	op, exists := data.operations[id]
	inv, live := m.invocations[a.InvocationID]
	if !exists || !live {
		return api.OperationArtifactUploadResponse{}, ErrNotFound
	}
	now := time.Now().UTC()
	if err := ValidateOperationExecutionAuthority(op, inv, a, now); err != nil {
		return api.OperationArtifactUploadResponse{}, err
	}
	if err := validateHTTPArtifactDeclaration(op, inv, req); err != nil {
		return api.OperationArtifactUploadResponse{}, err
	}
	key := fmt.Sprintf("%s/%s/%d/%s", op.ID, inv.ID, inv.Attempts, req.ReportID)
	if prior, ok := data.reports[key]; ok {
		if prior != artifactFingerprint(req) {
			return api.OperationArtifactUploadResponse{}, ErrOperationInputConflict
		}
		storageKey := op.ArtifactStorageKeys[operations.ArtifactIdentity(op.ID, inv.ID, inv.Attempts, req.ReportID)]
		for _, blob := range data.blobs {
			if blob.StorageKey == storageKey {
				return httpUploadReceipt(op, inv, req, blob)
			}
		}
		return api.OperationArtifactUploadResponse{}, ErrConflict
	}
	copy := cloneOperation(op)
	_, err := operationArtifact(&copy, inv, req, now)
	return api.OperationArtifactUploadResponse{}, err
}

func (s *PgStore) ReuseOperationArtifact(ctx context.Context, id string, a OperationExecutionAuthority, req api.OperationArtifactRequest) (api.OperationArtifactUploadResponse, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.OperationArtifactUploadResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	inv, err := operationLockedInvocation(ctx, tx, a.InvocationID)
	if err != nil {
		return api.OperationArtifactUploadResponse{}, mapErr(err)
	}
	op, _, _, exists, err := operationForInvocationTx(ctx, tx, inv.ID)
	if err != nil {
		return api.OperationArtifactUploadResponse{}, err
	}
	if !exists || op.ID != id {
		return api.OperationArtifactUploadResponse{}, ErrNotFound
	}
	now := time.Now().UTC()
	if err := ValidateOperationExecutionAuthority(op, inv, a, now); err != nil {
		return api.OperationArtifactUploadResponse{}, err
	}
	if err := validateHTTPArtifactDeclaration(op, inv, req); err != nil {
		return api.OperationArtifactUploadResponse{}, err
	}
	q := sqlc.New()
	operationID, _ := operationUUID(op.ID)
	executionID, _ := operationUUID(inv.ID)
	prior, err := q.GetCustomerOperationReport(ctx, tx, sqlc.GetCustomerOperationReportParams{OperationID: operationID, ExecutionID: executionID, Attempt: int32(inv.Attempts), ReportID: req.ReportID})
	if errors.Is(err, pgx.ErrNoRows) {
		copy := cloneOperation(op)
		_, err := operationArtifact(&copy, inv, req, now)
		return api.OperationArtifactUploadResponse{}, err
	}
	if err != nil {
		return api.OperationArtifactUploadResponse{}, err
	}
	if prior != artifactFingerprint(req) {
		return api.OperationArtifactUploadResponse{}, ErrOperationInputConflict
	}
	key := op.ArtifactStorageKeys[operations.ArtifactIdentity(op.ID, inv.ID, inv.Attempts, req.ReportID)]
	row, err := q.CustomerOperationBlobByKey(ctx, tx, key)
	if err != nil {
		return api.OperationArtifactUploadResponse{}, mapErr(err)
	}
	row, err = q.LockCustomerOperationBlob(ctx, tx, row.ID)
	if err != nil {
		return api.OperationArtifactUploadResponse{}, mapErr(err)
	}
	if err := ValidateOperationExecutionAuthority(op, inv, a, time.Now().UTC()); err != nil {
		return api.OperationArtifactUploadResponse{}, err
	}
	return httpUploadReceipt(op, inv, req, operationPGBlob(row))
}
