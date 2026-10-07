// adr: 646
package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func jobOperationAuthorityTx(ctx context.Context, tx pgx.Tx, id string, a JobOperationAuthority) (Operation, JobTask, error) {
	for _, value := range []string{id, a.RunID, a.InstanceID} {
		if _, err := operationUUID(value); err != nil {
			return Operation{}, JobTask{}, err
		}
	}
	op, task, err := lockOperationJobTx(ctx, tx, a.RunID)
	if err != nil {
		return Operation{}, JobTask{}, err
	}
	if op.ID != id {
		return Operation{}, JobTask{}, ErrNotFound
	}
	valid, err := sqlc.New().CustomerOperationJobRuntimeOwner(ctx, tx, sqlc.CustomerOperationJobRuntimeOwnerParams{InstanceID: mustPgUUID(a.InstanceID), AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(op.AppID), TenantID: mustPgUUID(op.PlatformTenantID), RunID: mustPgUUID(a.RunID)})
	if err != nil {
		return Operation{}, JobTask{}, err
	}
	if !valid {
		return Operation{}, JobTask{}, ErrNotFound
	}
	if err := validateOperationJobAuthority(op, task, a, time.Now().UTC()); err != nil {
		return Operation{}, JobTask{}, err
	}
	return op, task, nil
}

func (s *PgStore) ReuseJobOperationArtifact(ctx context.Context, id string, a JobOperationAuthority, req api.OperationArtifactRequest) (api.OperationJobArtifactResponse, error) {
	_, _, response, err := s.jobArtifactTransaction(ctx, id, a, req, "reuse", "")
	return response, err
}
func (s *PgStore) ReserveJobOperationArtifact(ctx context.Context, id string, a JobOperationAuthority, req api.OperationArtifactRequest) (OperationResultBlob, Operation, error) {
	blob, op, _, err := s.jobArtifactTransaction(ctx, id, a, req, "reserve", "")
	return blob, op, err
}
func (s *PgStore) PrepareVerifiedJobOperationArtifact(ctx context.Context, id string, a JobOperationAuthority, req api.OperationArtifactRequest, blobID string) (api.OperationJobArtifactResponse, error) {
	_, _, response, err := s.jobArtifactTransaction(ctx, id, a, req, "prepare", blobID)
	return response, err
}

func (s *PgStore) jobArtifactTransaction(ctx context.Context, id string, a JobOperationAuthority, req api.OperationArtifactRequest, action, blobID string) (OperationResultBlob, Operation, api.OperationJobArtifactResponse, error) {
	if _, err := operationUUID(a.RunID); err != nil {
		return OperationResultBlob{}, Operation{}, api.OperationJobArtifactResponse{}, err
	}
	// Resolve the quota key before native locks, then validate the association
	// again under those locks. All artifact families share this advisory lock.
	owner, exists, err := s.OperationForJobRun(ctx, a.RunID)
	if err != nil {
		return OperationResultBlob{}, Operation{}, api.OperationJobArtifactResponse{}, err
	}
	if !exists || owner.ID != id {
		return OperationResultBlob{}, Operation{}, api.OperationJobArtifactResponse{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OperationResultBlob{}, Operation{}, api.OperationJobArtifactResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if action == "reserve" {
		if err := sqlc.New().LockCustomerOperationArtifactQuota(ctx, tx, owner.AccountID); err != nil {
			return OperationResultBlob{}, Operation{}, api.OperationJobArtifactResponse{}, err
		}
	}
	op, task, err := jobOperationAuthorityTx(ctx, tx, id, a)
	if err != nil {
		return OperationResultBlob{}, Operation{}, api.OperationJobArtifactResponse{}, err
	}
	if op.AccountID != owner.AccountID {
		return OperationResultBlob{}, Operation{}, api.OperationJobArtifactResponse{}, ErrNotFound
	}
	blob, response, err := jobArtifactMutationTx(ctx, tx, &op, task, a, req, action, blobID)
	if err != nil {
		return OperationResultBlob{}, Operation{}, api.OperationJobArtifactResponse{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OperationResultBlob{}, Operation{}, api.OperationJobArtifactResponse{}, err
	}
	return blob, op, response, nil
}

func jobArtifactMutationTx(ctx context.Context, tx pgx.Tx, op *Operation, task JobTask, a JobOperationAuthority, req api.OperationArtifactRequest, action, blobID string) (OperationResultBlob, api.OperationJobArtifactResponse, error) {
	q := sqlc.New()
	receipt, exists, err := jobArtifactReceipt(*op, a, req)
	if err != nil {
		return OperationResultBlob{}, api.OperationJobArtifactResponse{}, err
	}
	if exists {
		row, err := q.LockCustomerOperationBlob(ctx, tx, mustPgUUID(receipt.BlobID))
		if err != nil {
			return OperationResultBlob{}, api.OperationJobArtifactResponse{}, mapErr(err)
		}
		blob := operationPGBlob(row)
		if err := validateOperationJobAuthority(*op, task, a, time.Now().UTC()); err != nil {
			return OperationResultBlob{}, api.OperationJobArtifactResponse{}, err
		}
		response, err := reuseJobArtifact(*op, a, receipt, blob)
		return blob, response, err
	}
	if action == "reuse" {
		copy := cloneOperation(*op)
		_, err := prepareJobArtifact(&copy, a, req, OperationResultBlob{})
		return OperationResultBlob{}, api.OperationJobArtifactResponse{Available: false}, err
	}
	now := time.Now().UTC()
	if action == "reserve" {
		blob, err := reserveJobArtifactTx(ctx, tx, *op, task, a, req, now)
		return blob, api.OperationJobArtifactResponse{}, err
	}
	parsed, err := operationUUID(blobID)
	if err != nil {
		return OperationResultBlob{}, api.OperationJobArtifactResponse{}, err
	}
	row, err := q.LockCustomerOperationBlob(ctx, tx, parsed)
	if err != nil {
		return OperationResultBlob{}, api.OperationJobArtifactResponse{}, mapErr(err)
	}
	blob := operationPGBlob(row)
	// Blob locking may outlast the task lease. Validate after the last lock.
	if err := validateOperationJobAuthority(*op, task, a, time.Now().UTC()); err != nil {
		return OperationResultBlob{}, api.OperationJobArtifactResponse{}, err
	}
	if err := validateJobArtifactBlob(blob, *op, a, req, now); err != nil {
		return OperationResultBlob{}, api.OperationJobArtifactResponse{}, err
	}
	artifact, err := prepareJobArtifact(op, a, req, blob)
	if err != nil {
		return OperationResultBlob{}, api.OperationJobArtifactResponse{}, err
	}
	n, err := q.RetainCustomerOperationBlob(ctx, tx, sqlc.RetainCustomerOperationBlobParams{ID: parsed, Now: operationBlobTime(now)})
	if err != nil {
		return OperationResultBlob{}, api.OperationJobArtifactResponse{}, err
	}
	if n != 1 {
		return OperationResultBlob{}, api.OperationJobArtifactResponse{}, ErrOperationStaleAttempt
	}
	event := operationJobEvent(op, task, "artifact_prepared", map[string]any{"artifact_id": artifact.ID}, now)
	if err := operationSaveTx(ctx, tx, *op, event); err != nil {
		return OperationResultBlob{}, api.OperationJobArtifactResponse{}, err
	}
	return blob, api.OperationJobArtifactResponse{Available: true, Artifact: &artifact}, nil
}

func reserveJobArtifactTx(ctx context.Context, tx pgx.Tx, op Operation, task JobTask, a JobOperationAuthority, req api.OperationArtifactRequest, now time.Time) (OperationResultBlob, error) {
	q := sqlc.New()
	blob := newJobArtifactBlob(op, a, req, now)
	copy := cloneOperation(op)
	if _, err := prepareJobArtifact(&copy, a, req, blob); err != nil {
		return OperationResultBlob{}, err
	}
	account := mustPgUUID(op.AccountID)
	plan, err := q.CustomerOperationAccountPlan(ctx, tx, account)
	if err != nil {
		return OperationResultBlob{}, mapErr(err)
	}
	usage, err := q.CustomerOperationBlobUsage(ctx, tx, account)
	if err != nil {
		return OperationResultBlob{}, err
	}
	if err := checkOperationBlobQuota(api.MustLimitsFor(api.Plan(plan)).Operations, usage.BlobCount, usage.Bytes, req.SizeBytes); err != nil {
		return OperationResultBlob{}, err
	}
	if err := validateOperationJobAuthority(op, task, a, time.Now().UTC()); err != nil {
		return OperationResultBlob{}, err
	}
	err = q.InsertCustomerOperationBlob(ctx, tx, sqlc.InsertCustomerOperationBlobParams{ID: mustPgUUID(blob.ID), OperationID: mustPgUUID(op.ID), AccountID: account, Generation: int32(op.Generation), JobRunID: mustPgUUID(a.RunID), Attempt: int32(a.Attempt), ReportID: req.ReportID, Fingerprint: blob.Fingerprint, StorageKey: blob.StorageKey, SizeBytes: blob.SizeBytes, ExpiresAt: operationBlobTime(blob.ExpiresAt)})
	return blob, err
}

var _ OperationJobArtifactStore = (*PgStore)(nil)
