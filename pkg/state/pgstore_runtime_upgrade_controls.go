package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradeReservationStore = (*PgStore)(nil)

func (s *PgStore) ReserveRuntimeUpgradeOperation(ctx context.Context, r RuntimeUpgradeOperationRequest, sourcePath string) (RuntimeUpgradeOperation, error) {
	if err := validateRuntimeUpgradeReservation(r, sourcePath); err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("begin runtime upgrade reservation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	old, err := q.LockRuntimeUpgradeOperationControl(ctx, tx, sqlc.LockRuntimeUpgradeOperationControlParams{ID: mustPgUUID(r.ID), AccountID: mustPgUUID(r.AccountID)})
	if err == nil {
		op := runtimeUpgradeOperationFromRow(old)
		if op.RuntimeUpgradeOperationRequest != r || op.SourcePath != sourcePath {
			return RuntimeUpgradeOperation{}, ErrConflict
		}
		return op, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeOperation{}, fmt.Errorf("read runtime upgrade reservation: %w", err)
	}
	// Lock the serving environment/app/deployments before inserting its sibling.
	// This uses the same owner order as cutover and scoped-input publication.
	lock := r.cutover("")
	lock.DeploymentID = r.ServingDeploymentID
	if err := lockRuntimeUpgradeCutoverDB(ctx, tx, lock); err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("lock runtime upgrade reservation: %w", runtimeUpgradeBaselineError(err))
	}
	// A concurrent identical reservation may have committed while we waited
	// for the app fence. Return its history before attempting another INSERT.
	existing, readErr := q.GetRuntimeUpgradeOperation(ctx, tx, mustPgUUID(r.ID))
	if readErr == nil {
		op := runtimeUpgradeOperationFromRow(existing)
		if op.RuntimeUpgradeOperationRequest != r || op.SourcePath != sourcePath {
			return RuntimeUpgradeOperation{}, ErrConflict
		}
		return op, nil
	}
	if !errors.Is(readErr, pgx.ErrNoRows) {
		return RuntimeUpgradeOperation{}, readErr
	}
	account, err := q.AccountByID(ctx, tx, mustPgUUID(r.AccountID))
	if err != nil {
		return RuntimeUpgradeOperation{}, mapErr(err)
	}
	limit, known := api.LimitsFor(api.Plan(account.Plan))
	if !known {
		return RuntimeUpgradeOperation{}, ErrConflict
	}
	if _, err := q.ReserveRuntimeUpgradeCandidate(ctx, tx, sqlc.ReserveRuntimeUpgradeCandidateParams{
		DeploymentID: mustPgUUID(r.DeploymentID), SourcePath: sourcePath, OperationID: r.ID, ServingID: mustPgUUID(r.ServingDeploymentID), AppID: mustPgUUID(r.AppID), AccountID: mustPgUUID(r.AccountID), SourceSha256: r.SourceSHA256, SourceMaxBytes: int64(limit.SourceTarballMaxMB) * 1024 * 1024,
	}); err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("reserve runtime upgrade candidate: %w", runtimeUpgradeBaselineError(mapErr(err)))
	}
	op, err := registerRuntimeUpgradeOperationPhaseDB(ctx, tx, r, RuntimeUpgradeReserved, sourcePath)
	if err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("retain runtime upgrade reservation: %w", runtimeUpgradeBaselineError(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("commit runtime upgrade reservation: %w", runtimeUpgradeBaselineError(err))
	}
	return op, nil
}

func (s *PgStore) PrepareReservedRuntimeUpgradeOperation(ctx context.Context, accountID, id string) (RuntimeUpgradeOperation, error) {
	return s.runtimeUpgradeControl(ctx, accountID, id, false)
}

func (s *PgStore) CancelRuntimeUpgradeOperation(ctx context.Context, accountID, id string) (RuntimeUpgradeOperation, error) {
	return s.runtimeUpgradeControl(ctx, accountID, id, true)
}

func (s *PgStore) runtimeUpgradeControl(ctx context.Context, accountID, id string, cancel bool) (RuntimeUpgradeOperation, error) {
	if validateRuntimeAppEnvIDs(accountID, id, id) != nil {
		return RuntimeUpgradeOperation{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("begin runtime upgrade control: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockRuntimeUpgradeOperationControl(ctx, tx, sqlc.LockRuntimeUpgradeOperationControlParams{ID: mustPgUUID(id), AccountID: mustPgUUID(accountID)})
	if err != nil {
		return RuntimeUpgradeOperation{}, mapErr(err)
	}
	op := runtimeUpgradeOperationFromRow(row)
	var phase RuntimeUpgradeOperationPhase
	var blocker, wake string
	if cancel {
		if op.Phase == RuntimeUpgradeCancelled {
			return op, nil
		}
		if !runtimeUpgradeActive(op.Phase) {
			return RuntimeUpgradeOperation{}, ErrConflict
		}
		phase, blocker, wake, err = cancelRuntimeUpgradeOperationDB(ctx, tx, op)
	} else {
		if op.Phase != RuntimeUpgradeReserved {
			return op, nil
		}
		phase, blocker, wake, err = advanceRuntimeUpgradeOperationDB(ctx, tx, op)
	}
	if err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("apply runtime upgrade control: %w", runtimeUpgradeBaselineError(err))
	}
	wakeID := pgtype.UUID{}
	if wake != "" {
		wakeID = mustPgUUID(wake)
	}
	row, err = q.CheckpointRuntimeUpgradeOperationControl(ctx, tx, sqlc.CheckpointRuntimeUpgradeOperationControlParams{ID: mustPgUUID(id), Phase: string(phase), Blocker: blocker, WakeID: wakeID})
	if err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("checkpoint runtime upgrade control: %w", runtimeUpgradeBaselineError(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("commit runtime upgrade control: %w", runtimeUpgradeBaselineError(err))
	}
	return runtimeUpgradeOperationFromRow(row), nil
}

func cancelRuntimeUpgradeOperationDB(ctx context.Context, tx pgx.Tx, op RuntimeUpgradeOperation) (RuntimeUpgradeOperationPhase, string, string, error) {
	q := sqlc.New()
	// Cancellation needs ownership and pipeline locks, even after app suspension;
	// it does not read/apply configuration or grant traffic.
	if _, err := q.LockRuntimeUpgradeCancelApp(ctx, tx, sqlc.LockRuntimeUpgradeCancelAppParams{AppID: mustPgUUID(op.AppID), AccountID: mustPgUUID(op.AccountID)}); err != nil {
		return "", "", "", err
	}
	if _, err := q.LockRuntimeUpgradeCutoverDeployments(ctx, tx, mustPgUUID(op.AppID)); err != nil {
		return "", "", "", err
	}
	old, err := q.GetDeploymentRuntimeUpgradeCutover(ctx, tx, mustPgUUID(op.DeploymentID))
	if err == nil {
		c := runtimeUpgradeCutoverFromRow(old)
		if !op.cutover(c.WakeID).matches(c) {
			return "", "", "", ErrConflict
		}
		return RuntimeUpgradeComplete, "", c.WakeID, nil // acknowledge history; cancellation never rolls traffic back
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", err
	}
	candidate, err := q.ReadRuntimeUpgradeOperationCandidate(ctx, tx, mustPgUUID(op.DeploymentID))
	if err != nil {
		return "", "", "", err
	}
	if candidate.TrafficPercent != 0 || !candidate.TrafficPercentExplicit {
		return "", "", "", ErrConflict
	}
	if err := q.CancelRuntimeUpgradeCandidate(ctx, tx, sqlc.CancelRuntimeUpgradeCandidateParams{AccountID: op.AccountID, DeploymentID: mustPgUUID(op.DeploymentID)}); err != nil {
		return "", "", "", err
	}
	if err := q.CancelRuntimeUpgradeReleaseTasks(ctx, tx, mustPgUUID(op.DeploymentID)); err != nil {
		return "", "", "", err
	}
	builds, err := q.CancelRuntimeUpgradeBuilds(ctx, tx, mustPgUUID(op.DeploymentID))
	if err != nil {
		return "", "", "", err
	}
	for _, build := range builds {
		if err := q.NotifyRuntimeUpgradeCancellation(ctx, tx, sqlc.NotifyRuntimeUpgradeCancellationParams{BuildID: pgUUIDString(build), DeploymentID: op.DeploymentID}); err != nil {
			return "", "", "", err
		}
	}
	if err := q.NotifyRuntimeUpgradeCandidateCancelled(ctx, tx, sqlc.NotifyRuntimeUpgradeCandidateCancelledParams{AppID: op.AppID, DeploymentID: op.DeploymentID}); err != nil {
		return "", "", "", err
	}
	return RuntimeUpgradeCancelled, "", "", nil
}
