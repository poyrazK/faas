package state

// adr: 1545

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ FunctionMigrationSourceStore = (*PgStore)(nil)

func validateFunctionMigrationSource(source FunctionMigrationSource) error {
	for _, id := range []string{source.InstanceID, source.SourceNodeID, source.SourceWakeID} {
		if _, err := uuid.Parse(id); err != nil {
			return ErrInvalidArgument
		}
	}
	if source.LeaseToken == "" {
		return ErrInvalidArgument
	}
	return nil
}

func functionMigrationSource(row sqlc.FunctionMigrationSource) FunctionMigrationSource {
	source := FunctionMigrationSource{InstanceID: pgUUIDString(row.InstanceID), LeaseToken: row.LeaseToken,
		SourceNodeID: row.SourceNodeID, SourceWakeID: pgUUIDString(row.SourceWakeID), Phase: row.Phase}
	if row.CaptureToken.Valid {
		source.CaptureToken = pgUUIDString(row.CaptureToken)
	}
	return source
}

func (s *PgStore) RegisterFunctionMigrationSource(ctx context.Context, source FunctionMigrationSource, memory, vmstate string) error {
	if err := validateFunctionMigrationSource(source); err != nil {
		return err
	}
	if memory == "" || vmstate == "" || memory == vmstate {
		return ErrInvalidArgument
	}
	count, err := sqlc.New().RegisterFunctionMigrationSource(ctx, s.pool, sqlc.RegisterFunctionMigrationSourceParams{
		InstanceID: mustPgUUID(source.InstanceID), LeaseToken: source.LeaseToken,
		SourceNodeID: source.SourceNodeID, SourceWakeID: mustPgUUID(source.SourceWakeID), MemoryKey: memory, VmstateKey: vmstate,
	})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) PrepareFunctionMigrationSource(ctx context.Context, id, token string) error {
	count, err := sqlc.New().SetFunctionMigrationSourcePhase(ctx, s.pool, sqlc.SetFunctionMigrationSourcePhaseParams{
		InstanceID: mustPgUUID(id), LeaseToken: token, Phase: FunctionMigrationPrepared, ExpectedPhase: FunctionMigrationPreparing,
	})
	if err != nil {
		return mapErr(err)
	}
	if count == 1 {
		return nil
	}
	// Expiry may already have fenced the attempt while capture was finishing.
	source, err := s.GetFunctionMigrationSource(ctx, id, token)
	if err != nil {
		return err
	}
	if source.Phase == FunctionMigrationRecovering {
		return nil
	}
	return ErrConflict
}

func (s *PgStore) GetFunctionMigrationSource(ctx context.Context, id, token string) (FunctionMigrationSource, error) {
	row, err := sqlc.New().GetFunctionMigrationSource(ctx, s.pool, sqlc.GetFunctionMigrationSourceParams{InstanceID: mustPgUUID(id), LeaseToken: token})
	if err != nil {
		return FunctionMigrationSource{}, mapErr(err)
	}
	return functionMigrationSource(row), nil
}

func (s *PgStore) GetFunctionMigrationSourceByInstance(ctx context.Context, id string) (FunctionMigrationSource, error) {
	row, err := sqlc.New().GetFunctionMigrationSourceByInstance(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return FunctionMigrationSource{}, mapErr(err)
	}
	return functionMigrationSource(row), nil
}

func (s *PgStore) ListFunctionMigrationSourceInstances(ctx context.Context, app string) ([]string, error) {
	rows, err := sqlc.New().ListFunctionMigrationSourceInstances(ctx, s.pool, mustPgUUID(app))
	if err != nil {
		return nil, mapErr(err)
	}
	result := make([]string, 0, len(rows))
	for _, id := range rows {
		result = append(result, pgUUIDString(id))
	}
	return result, nil
}

func (s *PgStore) markFunctionMigrationSource(ctx context.Context, id, node, token string) (bool, error) {
	current, err := s.GetFunctionMigrationSourceByInstance(ctx, id)
	if errors.Is(err, ErrNotFound) {
		if strings.HasPrefix(token, FunctionMigrationTokenPrefix) {
			return true, ErrConflict
		}
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if current.LeaseToken != token {
		return true, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return true, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if current.CaptureToken != "" {
		locked, err := lockFunctionMigrationAdmission(ctx, tx, id, token, string(StateRunning))
		if err != nil {
			return true, err
		}
		grant, err := decodeFunctionMigrationGrant(locked.row.CaptureGrant, runtimeadmission.FunctionMigrationCapture)
		if err != nil {
			return true, err
		}
		if err := checkFunctionMigrationPermission(grant, locked.input.Parent, locked.input.SourceStartedAtUnixNano, locked.lease,
			time.Unix(0, locked.input.ClockUnixNano), locked.input.artifactDeadline()); err != nil {
			return true, err
		}
	}
	row, err := q.LockInstanceMigrationCommit(ctx, tx, mustPgUUID(id))
	if err != nil {
		return true, mapErr(err)
	}
	lease, err := q.LockFunctionMigrationSourceLease(ctx, tx, sqlc.LockFunctionMigrationSourceLeaseParams{InstanceID: row.ID, LeaseToken: token})
	if err != nil {
		return true, mapErr(err)
	}
	source, err := q.LockFunctionMigrationSource(ctx, tx, sqlc.LockFunctionMigrationSourceParams{InstanceID: row.ID, LeaseToken: token})
	if err != nil {
		return true, mapErr(err)
	}
	if row.State != string(StateRunning) || row.LeaseToken.String != "" || source.Phase != FunctionMigrationPrepared ||
		pgUUIDString(row.NodeID) != node || source.SourceNodeID != node || row.WakeID != source.SourceWakeID || lease.Pending || !lease.LeaseExpiresAt.Time.After(time.Now()) {
		return true, ErrConflict
	}
	if source.CaptureToken.Valid {
		grant, err := decodeFunctionMigrationGrant(source.CaptureGrant, runtimeadmission.FunctionMigrationCapture)
		if err != nil || grant.Validate(time.Now()) != nil {
			return true, ErrConflict
		}
	}
	count, err := q.FenceFunctionMigrationSource(ctx, tx, sqlc.FenceFunctionMigrationSourceParams{
		InstanceID: row.ID, SourceNodeID: node, SourceWakeID: row.WakeID, ExpectedState: row.State, ExpectedToken: "", RollbackToken: token,
	})
	if err != nil {
		return true, mapErr(err)
	}
	if count != 1 {
		return true, ErrConflict
	}
	return true, mapErr(tx.Commit(ctx))
}

func (s *PgStore) FenceFunctionMigrationSource(ctx context.Context, id, token string) (MigrationCommitResolution, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MigrationCommitRetained, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	// Match the ownership commit's lock before locking the source receipt.
	row, err := q.LockInstanceMigrationCommit(ctx, tx, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return MigrationCommitObsolete, nil
	}
	if err != nil {
		return MigrationCommitRetained, mapErr(err)
	}
	receipt, err := q.LockFunctionMigrationSource(ctx, tx, sqlc.LockFunctionMigrationSourceParams{InstanceID: row.ID, LeaseToken: token})
	if err != nil {
		return MigrationCommitRetained, mapErr(err)
	}
	source := functionMigrationSource(receipt)
	ins := Instance{ID: id, NodeID: pgUUIDString(row.NodeID), State: row.State, WakeID: pgUUIDString(row.WakeID), LeaseToken: row.LeaseToken.String}
	resolution := classifyFunctionMigrationSource(ins, source)
	if resolution != MigrationCommitAborted {
		return resolution, nil
	}
	if source.Phase == FunctionMigrationRecovered || source.Phase == FunctionMigrationRetired {
		return resolution, nil // Completion ACK was lost; never create a new fence.
	}
	var count int64
	if row.State == string(StateMigrating) {
		count, err = q.FencePreparedFunctionMigrationSource(ctx, tx, sqlc.FencePreparedFunctionMigrationSourceParams{
			InstanceID: row.ID, SourceNodeID: source.SourceNodeID, SourceWakeID: row.WakeID, ExpectedToken: row.LeaseToken.String, RollbackToken: FunctionMigrationRollbackToken(token),
		})
	} else {
		count, err = q.FenceFunctionMigrationSource(ctx, tx, sqlc.FenceFunctionMigrationSourceParams{
			InstanceID: row.ID, SourceNodeID: source.SourceNodeID, SourceWakeID: row.WakeID,
			ExpectedState: row.State, ExpectedToken: row.LeaseToken.String, RollbackToken: FunctionMigrationRollbackToken(token),
		})
	}
	if err != nil {
		return MigrationCommitRetained, mapErr(err)
	}
	if count != 1 {
		return MigrationCommitRetained, ErrConflict
	}
	count, err = q.SetFunctionMigrationSourcePhase(ctx, tx, sqlc.SetFunctionMigrationSourcePhaseParams{
		InstanceID: row.ID, LeaseToken: token, Phase: FunctionMigrationRecovering, ExpectedPhase: receipt.Phase,
	})
	if err != nil {
		return MigrationCommitRetained, mapErr(err)
	}
	if count != 1 {
		return MigrationCommitRetained, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return MigrationCommitRetained, mapErr(err)
	}
	return MigrationCommitAborted, nil
}

func (s *PgStore) CompleteFunctionMigrationSource(ctx context.Context, id, token string, retired bool) error {
	snapshot, err := s.GetFunctionMigrationSource(ctx, id, token)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if !retired {
		if err := q.LockFunctionMigrationSourceNode(ctx, tx, sqlc.LockFunctionMigrationSourceNodeParams{LockClass: nodeReservationLockClass, NodeID: snapshot.SourceNodeID}); err != nil {
			return err
		}
		if snapshot.CaptureToken != "" && snapshot.Phase == FunctionMigrationRecovering {
			if err := functionDestinationRetiredTx(ctx, tx, id, token); err != nil {
				return err
			}
			// Lock the current native inputs before the instance, lease and
			// source. Keep these locks through RUNNING publication.
			locked, err := lockFunctionMigrationAdmission(ctx, tx, id, token, string(StateMigrating))
			if err != nil {
				return err
			}
			grant, err := decodeFunctionMigrationGrant(locked.row.RecoveryGrant, runtimeadmission.FunctionMigrationRecover)
			if err != nil {
				return err
			}
			if err := checkLockedFunctionMigrationGrant(locked, grant, true); err != nil {
				return err
			}
		}
	}
	row, err := q.LockInstanceMigrationCommit(ctx, tx, mustPgUUID(id))
	if err != nil {
		return mapErr(err)
	}
	receipt, err := q.LockFunctionMigrationSource(ctx, tx, sqlc.LockFunctionMigrationSourceParams{InstanceID: row.ID, LeaseToken: token})
	if err != nil {
		return mapErr(err)
	}
	source := functionMigrationSource(receipt)
	phase, target := FunctionMigrationRecovered, string(StateRunning)
	if retired {
		phase, target = FunctionMigrationRetired, string(StateStopped)
	}
	if receipt.Phase == phase && row.State == target && row.LeaseToken.String == "" && pgUUIDString(row.WakeID) == source.SourceWakeID && pgUUIDString(row.NodeID) == source.SourceNodeID {
		return nil
	}
	if receipt.Phase != FunctionMigrationRecovering {
		return ErrConflict
	}
	if !retired && source.CaptureToken != "" {
		grant, err := decodeFunctionMigrationGrant(receipt.RecoveryGrant, runtimeadmission.FunctionMigrationRecover)
		if err != nil || grant.Validate(time.Now()) != nil {
			return ErrConflict
		}
	}
	if !retired {
		if err := reserveNodeForMigration(ctx, tx, id, source.SourceNodeID); err != nil {
			return err
		}
	}
	count, err := q.CompleteFunctionMigrationSource(ctx, tx, sqlc.CompleteFunctionMigrationSourceParams{
		InstanceID: row.ID, SourceNodeID: source.SourceNodeID, SourceWakeID: mustPgUUID(source.SourceWakeID),
		State: target, RollbackToken: FunctionMigrationRollbackToken(token),
	})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	count, err = q.SetFunctionMigrationSourcePhase(ctx, tx, sqlc.SetFunctionMigrationSourcePhaseParams{
		InstanceID: row.ID, LeaseToken: token, Phase: phase, ExpectedPhase: FunctionMigrationRecovering,
	})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}

func (s *PgStore) ListExpiredFunctionMigrationSources(ctx context.Context, now time.Time, maxRows int) ([]FunctionMigrationSource, error) {
	if maxRows <= 0 {
		return nil, nil
	}
	rows, err := sqlc.New().ListExpiredFunctionMigrationSources(ctx, s.pool, sqlc.ListExpiredFunctionMigrationSourcesParams{Now: pgtype.Timestamptz{Time: now, Valid: true}, MaxRows: int64(maxRows)})
	if err != nil {
		return nil, mapErr(err)
	}
	result := make([]FunctionMigrationSource, 0, len(rows))
	for _, row := range rows {
		result = append(result, functionMigrationSource(row))
	}
	return result, nil
}
