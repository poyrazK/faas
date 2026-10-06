package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Callers must verify bucket ownership, environment, placement and actual
// bytes, and successfully write the reserved platform copy before attachment.
// Store methods recheck the claim and staging receipt atomically.
type OperationArtifactStore interface {
	AttachVerifiedOperationArtifact(context.Context, string, OperationExecutionAuthority, api.OperationArtifactRequest, string) (Operation, error)
}

func operationArtifact(op *Operation, inv Invocation, req api.OperationArtifactRequest, now time.Time) (api.OperationEvent, error) {
	if err := operations.ValidateArtifact(req, op.PlanLimits); err != nil {
		return api.OperationEvent{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	app, _, _, _ := operations.ParseArtifactURI(req.URI)
	if app != op.AppID {
		return api.OperationEvent{}, ErrNotFound
	}
	if len(op.Artifacts) >= op.PlanLimits.ArtifactsPerOperation {
		return api.OperationEvent{}, NewOperationLimitError("artifacts_per_operation", int64(op.PlanLimits.ArtifactsPerOperation), int64(len(op.Artifacts))+1)
	}
	if op.ReportCount >= op.PlanLimits.ReportsPerOperation {
		return api.OperationEvent{}, NewOperationLimitError("reports_per_operation", int64(op.PlanLimits.ReportsPerOperation), int64(op.ReportCount)+1)
	}
	total := req.SizeBytes
	for _, artifact := range op.Artifacts {
		if artifact.Name == req.Name {
			return api.OperationEvent{}, ErrConflict
		}
		total += artifact.SizeBytes
	}
	if total > op.PlanLimits.ArtifactTotalMaxBytes {
		return api.OperationEvent{}, NewOperationLimitError("artifact_total_bytes", op.PlanLimits.ArtifactTotalMaxBytes, total)
	}
	id := operations.ArtifactIdentity(op.ID, inv.ID, inv.Attempts, req.ReportID)
	expiry := op.ExpiresAt
	artifact := api.OperationResultArtifact{ID: id, Name: req.Name, URI: req.URI, SizeBytes: req.SizeBytes, SHA256: req.SHA256, ExpiresAt: &expiry}
	op.Artifacts = append(op.Artifacts, artifact)
	op.ReportCount++
	return operationEvent(op, inv, "artifact_attached", artifact, now), nil
}

func artifactFingerprint(req api.OperationArtifactRequest) string {
	b, _ := json.Marshal(req)
	digest, _ := operations.InputFingerprint(b)
	return digest
}

func (m *MemStore) AttachVerifiedOperationArtifact(_ context.Context, id string, authority OperationExecutionAuthority, req api.OperationArtifactRequest, blobID string) (Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	op, exists := data.operations[id]
	if !exists {
		return Operation{}, ErrNotFound
	}
	inv, exists := m.invocations[authority.InvocationID]
	if !exists {
		return Operation{}, ErrNotFound
	}
	now := time.Now().UTC()
	if err := ValidateOperationExecutionAuthority(op, inv, authority, now); err != nil {
		return Operation{}, err
	}
	key := fmt.Sprintf("%s/%s/%d/%s", op.ID, inv.ID, inv.Attempts, req.ReportID)
	fingerprint := artifactFingerprint(req)
	if prior, exists := data.reports[key]; exists {
		if prior != fingerprint {
			return Operation{}, ErrOperationInputConflict
		}
		return cloneOperation(op), nil
	}
	blob, exists := data.blobs[blobID]
	if !exists {
		return Operation{}, ErrNotFound
	}
	if err := validateOperationResultBlob(blob, op, inv, req, now); err != nil {
		return Operation{}, err
	}
	event, err := operationArtifact(&op, inv, req, now)
	if err != nil {
		return Operation{}, err
	}
	blob.State = "retained"
	data.blobs[blobID] = blob
	bindOperationResultBlob(&op, operations.ArtifactIdentity(op.ID, inv.ID, inv.Attempts, req.ReportID), blob)
	data.reports[key] = fingerprint
	m.operationSaveLocked(op, event)
	return cloneOperation(op), nil
}

func (s *PgStore) AttachVerifiedOperationArtifact(ctx context.Context, id string, authority OperationExecutionAuthority, req api.OperationArtifactRequest, blobID string) (Operation, error) {
	if _, err := operationUUID(authority.InvocationID); err != nil {
		return Operation{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	execution, _ := operationUUID(authority.InvocationID)
	claim, err := q.LockCustomerOperationClaim(ctx, tx, execution)
	if err != nil {
		return Operation{}, mapErr(err)
	}
	inv := Invocation{ID: claim.ID, AppID: claim.AppID, AccountID: claim.AccountID, PlatformTenantID: claim.PlatformTenantID, InstanceID: claim.InstanceID, State: InvocationState(claim.State), Attempts: int(claim.Attempts)}
	if claim.LeaseExpiresAt.Valid {
		expiry := claim.LeaseExpiresAt.Time
		inv.LeaseExpiresAt = &expiry
	}
	op, _, _, exists, err := operationForInvocationTx(ctx, tx, inv.ID)
	if err != nil {
		return Operation{}, err
	}
	if !exists || op.ID != id {
		return Operation{}, ErrNotFound
	}
	now := time.Now().UTC()
	if err := ValidateOperationExecutionAuthority(op, inv, authority, now); err != nil {
		return Operation{}, err
	}
	operationID, _ := operationUUID(op.ID)
	fingerprint := artifactFingerprint(req)
	prior, err := q.GetCustomerOperationReport(ctx, tx, sqlc.GetCustomerOperationReportParams{OperationID: operationID, ExecutionID: execution, Attempt: int32(inv.Attempts), ReportID: req.ReportID})
	if err == nil {
		if prior != fingerprint {
			return Operation{}, ErrOperationInputConflict
		}
		return op, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, err
	}
	parsedBlob, err := operationUUID(blobID)
	if err != nil {
		return Operation{}, err
	}
	row, err := q.LockCustomerOperationBlob(ctx, tx, parsedBlob)
	if err != nil {
		return Operation{}, mapErr(err)
	}
	blob := operationPGBlob(row)
	if err := validateOperationResultBlob(blob, op, inv, req, now); err != nil {
		return Operation{}, err
	}
	event, err := operationArtifact(&op, inv, req, now)
	if err != nil {
		return Operation{}, err
	}
	n, err := q.RetainCustomerOperationBlob(ctx, tx, sqlc.RetainCustomerOperationBlobParams{ID: parsedBlob, Now: operationBlobTime(now)})
	if err != nil {
		return Operation{}, err
	}
	if n != 1 {
		return Operation{}, ErrOperationStaleAttempt
	}
	bindOperationResultBlob(&op, operations.ArtifactIdentity(op.ID, inv.ID, inv.Attempts, req.ReportID), blob)
	if err := q.InsertCustomerOperationReport(ctx, tx, sqlc.InsertCustomerOperationReportParams{OperationID: operationID, ExecutionID: execution, Attempt: int32(inv.Attempts), ReportID: req.ReportID, Fingerprint: fingerprint}); err != nil {
		return Operation{}, err
	}
	if err := operationSaveTx(ctx, tx, op, event); err != nil {
		return Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Operation{}, err
	}
	return op, nil
}

func refreshOperationArtifactExpiry(op *Operation) {
	for i := range op.Artifacts {
		expiry := op.ExpiresAt
		op.Artifacts[i].ExpiresAt = &expiry
	}
}
