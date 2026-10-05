package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ReserveOperationArtifact(ctx context.Context, id string, authority OperationExecutionAuthority, req api.OperationArtifactRequest) (OperationResultBlob, error) {
	account, err := operationUUID(authority.AccountID)
	if err != nil {
		return OperationResultBlob{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OperationResultBlob{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	// Account first serializes storage quota reservations across all its apps.
	// Remaining locks follow the existing invocation -> operation -> blob order.
	plan, err := q.LockCustomerOperationAccount(ctx, tx, account)
	if err != nil {
		return OperationResultBlob{}, mapErr(err)
	}
	inv, err := operationLockedInvocation(ctx, tx, authority.InvocationID)
	if err != nil {
		return OperationResultBlob{}, mapErr(err)
	}
	op, _, _, exists, err := operationForInvocationTx(ctx, tx, inv.ID)
	if err != nil {
		return OperationResultBlob{}, err
	}
	if !exists || op.ID != id {
		return OperationResultBlob{}, ErrNotFound
	}
	now := time.Now().UTC()
	if err := ValidateOperationExecutionAuthority(op, inv, authority, now); err != nil {
		return OperationResultBlob{}, err
	}
	operationID, _ := operationUUID(op.ID)
	execution, _ := operationUUID(inv.ID)
	prior, err := q.GetCustomerOperationReport(ctx, tx, sqlc.GetCustomerOperationReportParams{OperationID: operationID, ExecutionID: execution, Attempt: int32(inv.Attempts), ReportID: req.ReportID})
	if err == nil {
		if prior != artifactFingerprint(req) {
			return OperationResultBlob{}, ErrOperationInputConflict
		}
		key := op.ArtifactStorageKeys[operations.ArtifactIdentity(op.ID, inv.ID, inv.Attempts, req.ReportID)]
		row, err := q.CustomerOperationBlobByKey(ctx, tx, key)
		if err != nil {
			return OperationResultBlob{}, mapErr(err)
		}
		return operationPGBlob(row), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return OperationResultBlob{}, err
	}
	copy := cloneOperation(op)
	if _, err := operationArtifact(&copy, inv, req, now); err != nil {
		return OperationResultBlob{}, err
	}
	usage, err := q.CustomerOperationBlobUsage(ctx, tx, account)
	if err != nil {
		return OperationResultBlob{}, err
	}
	if err := checkOperationBlobQuota(api.MustLimitsFor(api.Plan(plan)).Operations, usage.BlobCount, usage.Bytes, req.SizeBytes); err != nil {
		return OperationResultBlob{}, err
	}
	blob := newOperationResultBlob(op, inv, req, now)
	blobID, _ := operationUUID(blob.ID)
	if err := q.InsertCustomerOperationBlob(ctx, tx, sqlc.InsertCustomerOperationBlobParams{ID: blobID, OperationID: operationID, AccountID: account,
		Generation: int32(blob.Generation), ExecutionID: execution, Attempt: int32(blob.Attempt), ReportID: blob.ReportID,
		Fingerprint: blob.Fingerprint, StorageKey: blob.StorageKey, SizeBytes: blob.SizeBytes, ExpiresAt: operationBlobTime(blob.ExpiresAt)}); err != nil {
		return OperationResultBlob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OperationResultBlob{}, err
	}
	return blob, nil
}

func (s *PgStore) ClaimOperationArtifactCleanup(ctx context.Context, token string, now time.Time) (OperationResultBlob, error) {
	if token == "" {
		return OperationResultBlob{}, ErrInvalidArgument
	}
	row, err := sqlc.New().ClaimCustomerOperationBlobCleanup(ctx, s.pool, sqlc.ClaimCustomerOperationBlobCleanupParams{LeaseToken: token, Now: operationBlobTime(now), LeaseUntil: operationBlobTime(now.Add(api.OperationArtifactCleanupLease))})
	if err != nil {
		return OperationResultBlob{}, mapErr(err)
	}
	return operationPGBlob(row), nil
}

func (s *PgStore) RetryOperationArtifactCleanup(ctx context.Context, id, token string, next time.Time) error {
	parsed, err := operationUUID(id)
	if err != nil {
		return err
	}
	n, err := sqlc.New().RetryCustomerOperationBlobCleanup(ctx, s.pool, sqlc.RetryCustomerOperationBlobCleanupParams{ID: parsed, LeaseToken: token, Now: operationBlobTime(time.Now()), NextAttemptAt: operationBlobTime(next)})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) CompleteOperationArtifactCleanup(ctx context.Context, id, token string) error {
	parsed, err := operationUUID(id)
	if err != nil {
		return err
	}
	n, err := sqlc.New().CompleteCustomerOperationBlobCleanup(ctx, s.pool, sqlc.CompleteCustomerOperationBlobCleanupParams{ID: parsed, LeaseToken: token, Now: operationBlobTime(time.Now())})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func operationBlobTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func operationPGBlob(row sqlc.CustomerOperationResultBlob) OperationResultBlob {
	blob := OperationResultBlob{ID: uuid.UUID(row.ID.Bytes).String(), OperationID: uuid.UUID(row.OperationID.Bytes).String(), AccountID: uuid.UUID(row.AccountID.Bytes).String(),
		ExecutionID: uuid.UUID(row.ExecutionID.Bytes).String(), Generation: int(row.Generation), Attempt: int(row.Attempt),
		ReportID: row.ReportID, Fingerprint: row.Fingerprint, StorageKey: row.StorageKey, SizeBytes: row.SizeBytes, State: row.State,
		ExpiresAt: row.ExpiresAt.Time, NextAttemptAt: row.NextAttemptAt.Time, LeaseToken: row.LeaseToken}
	if row.LeaseUntil.Valid {
		expiry := row.LeaseUntil.Time
		blob.LeaseUntil = &expiry
	}
	return blob
}

var _ OperationResultBlobStore = (*PgStore)(nil)
