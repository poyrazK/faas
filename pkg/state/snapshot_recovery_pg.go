package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	_ SnapshotRecoveryStore = (*PgStore)(nil)
	_ OperatorColdBootStore = (*PgStore)(nil)
)

func lockSnapshotRecoveryDeployment(ctx context.Context, tx pgx.Tx, deploymentID string) (sqlc.ReadSnapshotRecoveryDeploymentRow, error) {
	q := sqlc.New()
	d, err := q.ReadSnapshotRecoveryDeployment(ctx, tx, mustPgUUID(deploymentID))
	if err != nil {
		return d, mapErr(err)
	}
	if _, err := q.LockSnapshotRecoveryApp(ctx, tx, mustPgUUID(d.AppID)); err != nil {
		return d, mapErr(err)
	}
	generation, err := q.LockSnapshotRecoveryDeployment(ctx, tx, sqlc.LockSnapshotRecoveryDeploymentParams{
		DeploymentID: mustPgUUID(deploymentID), AppID: mustPgUUID(d.AppID),
	})
	d.SnapshotRecoveryGeneration = generation
	return d, mapErr(err)
}

func invalidateSnapshotRecoveryDB(ctx context.Context, tx pgx.Tx, deploymentID string) (SnapshotRecoveryResult, error) {
	q := sqlc.New()
	generation, err := q.AdvanceSnapshotRecoveryGeneration(ctx, tx, mustPgUUID(deploymentID))
	if err != nil {
		return SnapshotRecoveryResult{}, fmt.Errorf("advance snapshot recovery generation: %w", err)
	}
	rows, err := q.InvalidateSnapshotRecovery(ctx, tx, mustPgUUID(deploymentID))
	if err != nil {
		return SnapshotRecoveryResult{}, fmt.Errorf("invalidate recovery snapshots: %w", err)
	}
	complete, err := q.SnapshotRecoveryInvalidationComplete(ctx, tx, mustPgUUID(deploymentID))
	if err != nil {
		return SnapshotRecoveryResult{}, err
	}
	if !complete.Valid || !complete.Bool {
		return SnapshotRecoveryResult{}, fmt.Errorf("snapshot invalidation was suppressed: %w", ErrConflict)
	}
	ids := make([]struct{ ID, Tier string }, len(rows))
	for j, row := range rows {
		ids[j] = struct{ ID, Tier string }{row.ID, row.Tier}
	}
	return SnapshotRecoveryResult{Generation: generation, SnapshotIDs: sortRestartSnapshots(ids)}, nil
}

// InvalidateSnapshotRecovery serves the legacy unary RPC. Each invocation is
// a new action; HTTP operator intents provide a durable replayable result.
func (s *PgStore) InvalidateSnapshotRecovery(ctx context.Context, deploymentID string) (SnapshotRecoveryResult, error) {
	if err := validateSnapshotRecoveryIDs(deploymentID); err != nil {
		return SnapshotRecoveryResult{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SnapshotRecoveryResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := lockSnapshotRecoveryDeployment(ctx, tx, deploymentID); err != nil {
		return SnapshotRecoveryResult{}, err
	}
	result, err := invalidateSnapshotRecoveryDB(ctx, tx, deploymentID)
	if err != nil {
		return result, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SnapshotRecoveryResult{}, err
	}
	return result, nil
}

func (s *PgStore) ExecuteOperatorColdBoot(ctx context.Context, id, token string) (OperatorColdBootResult, error) {
	if err := validateSnapshotRecoveryIDs(id, token); err != nil {
		return OperatorColdBootResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OperatorColdBootResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockOperatorIntentAttempt(ctx, tx, mustPgUUID(id))
	if err != nil {
		return OperatorColdBootResult{}, mapErr(err)
	}
	i := operatorIntentAttempt(row)
	if i.Kind != OperatorIntentKindForceColdBoot || i.ClaimToken != token {
		return OperatorColdBootResult{}, ErrOperatorColdBootClaimLost
	}
	if i.Status != OperatorIntentRunning {
		if (i.Status == OperatorIntentSucceeded || i.Status == OperatorIntentFailed) && i.CompletionAuditOutboxID > 0 {
			return coldBootResult(i, true), nil
		}
		return OperatorColdBootResult{}, ErrConflict
	}
	i.Status, i.Error, i.SnapIDsMarkedStale = OperatorIntentFailed, "Cold-boot target deployment is unavailable.", []string{}
	if validateSnapshotRecoveryIDs(i.TargetID) == nil {
		d, targetErr := lockSnapshotRecoveryDeployment(ctx, tx, i.TargetID)
		if targetErr != nil && !errors.Is(targetErr, ErrNotFound) {
			return OperatorColdBootResult{}, fmt.Errorf("lock cold-boot target: %w", targetErr)
		}
		if targetErr == nil && (i.AccountID == nil || *i.AccountID == d.AccountID) {
			recovery, err := invalidateSnapshotRecoveryDB(ctx, tx, i.TargetID)
			if err != nil {
				return OperatorColdBootResult{}, err
			}
			i.Status, i.Error = OperatorIntentSucceeded, ""
			i.ColdBootGeneration, i.SnapIDsMarkedStale = recovery.Generation, recovery.SnapshotIDs
		}
	}
	finished, err := q.OperatorRestartDatabaseTime(ctx, tx)
	if err != nil {
		return OperatorColdBootResult{}, err
	}
	i.FinishedAt = &finished.Time
	subject := pgtype.UUID{}
	if i.AccountID != nil {
		subject = mustPgUUID(*i.AccountID)
	}
	outboxID, err := q.EnqueueOperatorColdBootOutcome(ctx, tx, sqlc.EnqueueOperatorColdBootOutcomeParams{
		Subject: subject, Data: coldBootOutcomeAudit(i), DedupeKey: "operator.cold_boot.outcome:" + i.ID,
	})
	if err != nil {
		return OperatorColdBootResult{}, fmt.Errorf("enqueue cold-boot outcome: %w", err)
	}
	final, err := q.CompleteOperatorColdBoot(ctx, tx, sqlc.CompleteOperatorColdBootParams{
		ID: mustPgUUID(id), ClaimToken: mustPgUUID(token), Status: string(i.Status), Error: i.Error,
		SnapIds: i.SnapIDsMarkedStale, Generation: pgtype.Int8{Int64: i.ColdBootGeneration, Valid: i.ColdBootGeneration > 0},
		OutboxID: outboxID, FinishedAt: finished,
	})
	if err != nil {
		return OperatorColdBootResult{}, fmt.Errorf("complete cold-boot intent: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OperatorColdBootResult{}, fmt.Errorf("commit cold-boot intent: %w", err)
	}
	return coldBootResult(operatorIntentAttempt(final), false), nil
}

func (s *PgStore) BindInstanceSnapshotRecovery(ctx context.Context, instanceID, wakeID, snapshotID string) error {
	if err := validateSnapshotRecoveryIDs(instanceID, wakeID); err != nil {
		return err
	}
	if snapshotID != "" {
		if err := validateSnapshotRecoveryIDs(snapshotID); err != nil {
			return err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	instance, err := q.ReadSnapshotRecoveryInstance(ctx, tx, mustPgUUID(instanceID))
	if err != nil {
		return mapErr(err)
	}
	d, err := lockSnapshotRecoveryDeployment(ctx, tx, instance.DeploymentID)
	if err != nil {
		return err
	}
	locked, err := q.LockSnapshotRecoveryInstance(ctx, tx, mustPgUUID(instanceID))
	if err != nil {
		return mapErr(err)
	}
	expected := string(StateColdBooting)
	if snapshotID != "" {
		expected = string(StateWaking)
	}
	if locked.DeploymentID != instance.DeploymentID || locked.AppID != d.AppID || locked.WakeID != wakeID || locked.State != expected {
		return ErrConflict
	}
	existing, err := q.ReadInstanceSnapshotRecovery(ctx, tx, mustPgUUID(instanceID))
	if err == nil {
		if pgUUIDString(existing.WakeID) == wakeID && pgUUIDString(existing.SnapshotID) == snapshotID {
			return nil
		}
		return ErrConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if snapshotID != "" {
		valid, err := q.SnapshotRecoveryAdmissionValid(ctx, tx, sqlc.SnapshotRecoveryAdmissionValidParams{
			SnapshotID: mustPgUUID(snapshotID), DeploymentID: mustPgUUID(instance.DeploymentID),
		})
		if err != nil {
			return err
		}
		if !valid {
			return ErrConflict
		}
	}
	if _, err := q.BindInstanceSnapshotRecovery(ctx, tx, sqlc.BindInstanceSnapshotRecoveryParams{
		InstanceID: mustPgUUID(instanceID), WakeID: mustPgUUID(wakeID), SnapshotID: mustPgUUID(snapshotID), Generation: d.SnapshotRecoveryGeneration,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) InstanceSnapshotRecoveryFresh(ctx context.Context, instanceID string) (bool, error) {
	fresh, err := sqlc.New().InstanceSnapshotRecoveryFresh(ctx, s.pool, mustPgUUID(instanceID))
	return fresh, mapErr(err)
}
