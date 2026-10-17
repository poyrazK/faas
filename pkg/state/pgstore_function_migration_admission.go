package state

// adr: 1546

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ FunctionMigrationAdmissionStore = (*PgStore)(nil)

type functionMigrationLocked struct {
	input  standardSnapshotLockedInputs
	owner  Instance
	lease  MigrationLease
	source FunctionMigrationSource
	row    sqlc.FunctionMigrationSource
	found  bool
}

func lockFunctionMigrationAdmission(ctx context.Context, tx pgx.Tx, id, token, expected string) (functionMigrationLocked, error) {
	var locked functionMigrationLocked
	var err error
	locked.input, err = lockStandardSnapshotCapture(ctx, tx, id, expected)
	if err != nil {
		return locked, err
	}
	q := sqlc.New()
	owner, err := q.LockInstanceMigrationCommit(ctx, tx, mustPgUUID(id))
	if err != nil {
		return locked, mapErr(err)
	}
	locked.owner = Instance{ID: id, NodeID: pgUUIDString(owner.NodeID), WakeID: pgUUIDString(owner.WakeID), State: owner.State, LeaseToken: owner.LeaseToken.String}
	lease, err := q.LockFunctionMigrationAdmissionLease(ctx, tx, sqlc.LockFunctionMigrationAdmissionLeaseParams{InstanceID: owner.ID, LeaseToken: token})
	if err != nil {
		return locked, mapErr(err)
	}
	locked.lease = MigrationLease{InstanceID: id, LeaseToken: lease.LeaseToken, SourceNodeID: lease.SourceNodeID,
		CreatedAt: lease.CreatedAt.Time, LeaseExpiresAt: lease.LeaseExpiresAt.Time, Pending: lease.Pending,
		MemStorageKey: lease.MemStorageKey, VMStateStorageKey: lease.VmstateStorageKey}
	locked.row, err = q.LockFunctionMigrationSource(ctx, tx, sqlc.LockFunctionMigrationSourceParams{InstanceID: owner.ID, LeaseToken: token})
	if errors.Is(err, pgx.ErrNoRows) && expected == string(StateRunning) {
		return locked, nil
	}
	if err != nil {
		return locked, mapErr(err)
	}
	locked.found, locked.source = true, functionMigrationSource(locked.row)
	return locked, nil
}

func (s *PgStore) IssueFunctionMigrationCapture(ctx context.Context, req FunctionMigrationCaptureRequest) (runtimeadmission.FunctionMigrationGrant, error) {
	if validateFunctionMigrationSource(req.Source) != nil {
		return runtimeadmission.FunctionMigrationGrant{}, ErrInvalidArgument
	}
	return retryRuntimeAdmissionBusy(ctx, func() (runtimeadmission.FunctionMigrationGrant, error) {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return runtimeadmission.FunctionMigrationGrant{}, mapErr(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		grant, err := issueFunctionMigrationCaptureTx(ctx, tx, req)
		if err != nil {
			return grant, err
		}
		return grant, mapErr(tx.Commit(ctx))
	})
}

func issueFunctionMigrationCaptureTx(ctx context.Context, tx pgx.Tx, req FunctionMigrationCaptureRequest) (runtimeadmission.FunctionMigrationGrant, error) {
	locked, err := lockFunctionMigrationAdmission(ctx, tx, req.Source.InstanceID, req.Source.LeaseToken, string(StateRunning))
	if err != nil {
		return runtimeadmission.FunctionMigrationGrant{}, err
	}
	if err := functionMigrationPermissionOwner(locked.owner, req.Source, locked.lease, false); err != nil {
		return runtimeadmission.FunctionMigrationGrant{}, err
	}
	now := time.Unix(0, locked.input.ClockUnixNano).UTC()
	grant, err := newFunctionMigrationCapture(req, locked.lease, locked.input.Parent, locked.input.SourceStartedAtUnixNano, now, locked.input.artifactDeadline())
	if err != nil {
		return grant, err
	}
	if locked.found {
		old, err := decodeFunctionMigrationGrant(locked.row.CaptureGrant, runtimeadmission.FunctionMigrationCapture)
		grant.IssuedAtUnixNano, grant.ExpiresAtUnixNano = old.IssuedAtUnixNano, old.ExpiresAtUnixNano
		if err != nil || locked.source.Phase != FunctionMigrationPreparing || !old.Equal(grant) ||
			checkFunctionMigrationPermission(old, locked.input.Parent, locked.input.SourceStartedAtUnixNano, locked.lease, now, locked.input.artifactDeadline()) != nil {
			return runtimeadmission.FunctionMigrationGrant{}, ErrConflict
		}
		return old.Clone(), nil
	}
	q := sqlc.New()
	count, err := q.RegisterFunctionMigrationSource(ctx, tx, sqlc.RegisterFunctionMigrationSourceParams{
		InstanceID: mustPgUUID(req.Source.InstanceID), LeaseToken: req.Source.LeaseToken, SourceNodeID: req.Source.SourceNodeID,
		SourceWakeID: mustPgUUID(req.Source.SourceWakeID), MemoryKey: req.MemoryKey, VmstateKey: req.VMStateKey,
	})
	if err != nil {
		return grant, mapErr(err)
	}
	if count != 1 {
		return grant, ErrConflict
	}
	raw, err := json.Marshal(grant)
	if err != nil {
		return grant, err
	}
	count, err = q.SetFunctionMigrationCapturePermission(ctx, tx, sqlc.SetFunctionMigrationCapturePermissionParams{
		InstanceID: mustPgUUID(req.Source.InstanceID), LeaseToken: req.Source.LeaseToken, CaptureToken: mustPgUUID(grant.CaptureToken), CaptureGrant: raw,
	})
	if err != nil {
		return grant, mapErr(err)
	}
	if count != 1 {
		return grant, ErrConflict
	}
	return grant, nil
}

func checkLockedFunctionMigrationGrant(locked functionMigrationLocked, grant runtimeadmission.FunctionMigrationGrant, recovering bool) error {
	if !locked.found || locked.source.CaptureToken != grant.CaptureToken {
		return ErrApplicationStandardRuntimeStale
	}
	if err := functionMigrationPermissionOwner(locked.owner, locked.source, locked.lease, recovering); err != nil {
		return err
	}
	raw, purpose := locked.row.CaptureGrant, runtimeadmission.FunctionMigrationCapture
	if recovering {
		raw, purpose = locked.row.RecoveryGrant, runtimeadmission.FunctionMigrationRecover
	} else if locked.source.Phase != FunctionMigrationPreparing {
		return ErrConflict
	}
	stored, err := decodeFunctionMigrationGrant(raw, purpose)
	if err != nil || !stored.Equal(grant) {
		return ErrApplicationStandardRuntimeStale
	}
	return checkFunctionMigrationPermission(grant, locked.input.Parent, locked.input.SourceStartedAtUnixNano, locked.lease,
		time.Unix(0, locked.input.ClockUnixNano), locked.input.artifactDeadline())
}

func (s *PgStore) checkFunctionMigrationGrant(ctx context.Context, grant runtimeadmission.FunctionMigrationGrant, recovering bool) error {
	if grant.Validate(time.Now()) != nil || (grant.Purpose == runtimeadmission.FunctionMigrationRecover) != recovering {
		return ErrApplicationStandardRuntimeStale
	}
	_, err := retryRuntimeAdmissionBusy(ctx, func() (struct{}, error) {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return struct{}{}, mapErr(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		expected := string(StateRunning)
		if recovering {
			expected = string(StateMigrating)
		}
		locked, err := lockFunctionMigrationAdmission(ctx, tx, grant.Parent.Binding.InstanceID, grant.LeaseToken, expected)
		if err == nil {
			err = checkLockedFunctionMigrationGrant(locked, grant, recovering)
		}
		if err == nil && recovering {
			err = functionDestinationRetiredTx(ctx, tx, grant.Parent.Binding.InstanceID, grant.LeaseToken)
		}
		return struct{}{}, err
	})
	return err
}

func (s *PgStore) CheckFunctionMigrationCapture(ctx context.Context, grant runtimeadmission.FunctionMigrationGrant) error {
	return s.checkFunctionMigrationGrant(ctx, grant, false)
}

func (s *PgStore) CheckFunctionMigrationRecovery(ctx context.Context, grant runtimeadmission.FunctionMigrationGrant) error {
	return s.checkFunctionMigrationGrant(ctx, grant, true)
}

func (s *PgStore) IssueFunctionMigrationRecovery(ctx context.Context, id, token string) (runtimeadmission.FunctionMigrationGrant, error) {
	if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil || parsed.String() != id {
		return runtimeadmission.FunctionMigrationGrant{}, ErrInvalidArgument
	}
	return retryRuntimeAdmissionBusy(ctx, func() (runtimeadmission.FunctionMigrationGrant, error) {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return runtimeadmission.FunctionMigrationGrant{}, mapErr(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		grant, err := issueFunctionMigrationRecoveryTx(ctx, tx, id, token)
		if err != nil {
			return grant, err
		}
		return grant, mapErr(tx.Commit(ctx))
	})
}

func issueFunctionMigrationRecoveryTx(ctx context.Context, tx pgx.Tx, id, token string) (runtimeadmission.FunctionMigrationGrant, error) {
	locked, err := lockFunctionMigrationAdmission(ctx, tx, id, token, string(StateMigrating))
	if err != nil {
		return runtimeadmission.FunctionMigrationGrant{}, err
	}
	if err := functionMigrationPermissionOwner(locked.owner, locked.source, locked.lease, true); err != nil {
		return runtimeadmission.FunctionMigrationGrant{}, err
	}
	if err := functionDestinationRetiredTx(ctx, tx, id, token); err != nil {
		return runtimeadmission.FunctionMigrationGrant{}, err
	}
	capture, err := decodeFunctionMigrationGrant(locked.row.CaptureGrant, runtimeadmission.FunctionMigrationCapture)
	if err != nil || !capture.Parent.Equal(locked.input.Parent) || capture.SourceStartedAtUnixNano != locked.input.SourceStartedAtUnixNano ||
		capture.LeaseCreatedAtUnixNano != locked.lease.CreatedAt.UnixNano() || capture.MemoryKey != locked.lease.MemStorageKey || capture.VMStateKey != locked.lease.VMStateStorageKey {
		return runtimeadmission.FunctionMigrationGrant{}, ErrApplicationStandardRuntimeStale
	}
	now := time.Unix(0, locked.input.ClockUnixNano)
	if len(locked.row.RecoveryGrant) != 0 {
		old, err := decodeFunctionMigrationGrant(locked.row.RecoveryGrant, runtimeadmission.FunctionMigrationRecover)
		if err != nil || !old.SameSource(capture) {
			return runtimeadmission.FunctionMigrationGrant{}, ErrApplicationStandardRuntimeStale
		}
		if checkFunctionMigrationPermission(old, locked.input.Parent, locked.input.SourceStartedAtUnixNano, locked.lease, now, locked.input.artifactDeadline()) == nil {
			return old.Clone(), nil // Duplicate RPCs must not revoke an in-flight ACK.
		}
	}
	grant, err := newFunctionMigrationRecovery(capture, locked.lease, now, locked.input.artifactDeadline())
	if err != nil {
		return grant, err
	}
	raw, err := json.Marshal(grant)
	if err != nil {
		return grant, err
	}
	count, err := sqlc.New().SetFunctionMigrationRecoveryPermission(ctx, tx, sqlc.SetFunctionMigrationRecoveryPermissionParams{
		InstanceID: mustPgUUID(id), LeaseToken: token, CaptureToken: mustPgUUID(grant.CaptureToken), RecoveryGrant: raw,
	})
	if err != nil {
		return grant, mapErr(err)
	}
	if count != 1 {
		return grant, ErrConflict
	}
	return grant, nil
}
