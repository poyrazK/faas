package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) RecoverOperation(ctx context.Context, accountID, tenantID, operationID string, req api.OperationRecoveryRequest) (Operation, error) {
	// The initial read is only an ownership boundary and lock-order hint. The
	// locked row below is rechecked after any concurrent recovery completes.
	snapshot, err := s.OperationByID(ctx, accountID, tenantID, operationID)
	if err != nil {
		return Operation{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	original, err := operationLockedInvocation(ctx, tx, snapshot.CurrentInvocationID)
	if err != nil {
		return Operation{}, mapErr(err)
	}
	op, def, limits, exists, err := operationForInvocationTx(ctx, tx, original.ID)
	if err != nil {
		return Operation{}, err
	}
	if !exists || op.ID != operationID || op.AccountID != accountID || (tenantID != "" && op.PlatformTenantID != tenantID) {
		return Operation{}, ErrNotFound
	}
	now := time.Now().UTC()
	if !op.ExpiresAt.After(now) {
		return Operation{}, ErrOperationExpired
	}
	fingerprintLimits := limits
	fingerprintLimits.MaxSourceBytesPerInvocation = op.ValueMaxBytes
	fingerprint, err := operationRecoveryFingerprint(req, fingerprintLimits)
	if err != nil {
		return Operation{}, err
	}
	id, _ := operationUUID(op.ID)
	q := sqlc.New()
	prior, err := q.GetCustomerOperationRecovery(ctx, tx, sqlc.GetCustomerOperationRecoveryParams{OperationID: id, RecoveryID: req.RecoveryID})
	if err == nil {
		if prior != fingerprint {
			return Operation{}, ErrOperationInputConflict
		}
		return op, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, err
	}
	if req.Resolution == "safe_to_retry" {
		if !limits.Operations.Allowed {
			return Operation{}, NewOperationLimitError("plan_admission", 0, 1)
		}
		account, _ := operationUUID(accountID)
		tenant, _ := operationUUID(op.PlatformTenantID)
		status, err := q.LockCustomerOperationTenant(ctx, tx, sqlc.LockCustomerOperationTenantParams{AccountID: account, TenantID: tenant})
		if err != nil {
			return Operation{}, mapErr(err)
		}
		if status != PlatformTenantActive {
			return Operation{}, ErrPlatformTenantSuspended
		}
	}
	inv, event, err := prepareOperationRecovery(&op, original, def, limits, req, now)
	if err != nil {
		return Operation{}, err
	}
	if req.Resolution == "safe_to_retry" {
		if _, err := enqueueInvocationRow(ctx, tx, inv); err != nil {
			return Operation{}, err
		}
		execution, _ := operationUUID(inv.ID)
		if err := q.SetCustomerOperationExecutionIdentity(ctx, tx, sqlc.SetCustomerOperationExecutionIdentityParams{InvocationID: execution, OperationID: id}); err != nil {
			return Operation{}, err
		}
		if err := q.InsertCustomerOperationExecution(ctx, tx, sqlc.InsertCustomerOperationExecutionParams{OperationID: id, Generation: int32(op.Generation), InvocationID: execution}); err != nil {
			return Operation{}, err
		}
	} else {
		if err := operationCompletionTx(ctx, tx, &op, def); err != nil {
			return Operation{}, err
		}
	}
	raw, _ := json.Marshal(req)
	if err := q.InsertCustomerOperationRecovery(ctx, tx, sqlc.InsertCustomerOperationRecoveryParams{OperationID: id, RecoveryID: req.RecoveryID, Fingerprint: fingerprint, Request: raw, Now: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
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
