package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// SnapshotPrimeWorkerRecoveryStore withdraws a stale readiness worker before
// schedd destroys it. Callers must carry WithDeploymentActivationAttempt.
// DRAINING remains accounted and permits retry after interrupted destruction.
type SnapshotPrimeWorkerRecoveryStore interface {
	ClaimStalePrimeWorker(context.Context, App, Instance) (bool, error)
}

func primeRecoveryWorkerMatches(guard deploymentActivationAttempt, expected, actual Instance) bool {
	return guard.attempt.Status == DeploySnapshotting && actual.ID == expected.ID && actual.AppID == guard.attempt.AppID &&
		actual.DeploymentID == guard.attempt.DeploymentID && actual.Mode == string(InstanceModeWorker) &&
		(actual.State == string(StateRunning) || actual.State == string(StateDraining)) &&
		actual.NodeID == expected.NodeID && actual.WakeID == expected.WakeID && actual.StartedAt.Equal(expected.StartedAt) &&
		(guard.attempt.StartedAt.IsZero() || !actual.StartedAt.Before(guard.attempt.StartedAt))
}

func (m *MemStore) ClaimStalePrimeWorker(ctx context.Context, app App, expected Instance) (bool, error) {
	guard, ok := ctx.Value(deploymentActivationAttemptKey{}).(deploymentActivationAttempt)
	if !ok || app.ID != guard.attempt.AppID || app.AccountID != guard.accountID {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if actual := m.apps[app.ID]; actual.Status != AppActive || actual.NodeID != app.NodeID {
		return false, nil
	}
	d := m.deployments[guard.attempt.DeploymentID]
	if err := m.checkDeploymentActivationAttemptLocked(ctx, d); err != nil {
		return false, err
	}
	i := m.instances[expected.ID]
	if !primeRecoveryWorkerMatches(guard, expected, i) {
		return false, nil
	}
	if proof, ok := m.runtimeInstanceConfigProofs[i.ID]; i.State == string(StateRunning) && ok && proof.WakeID == i.WakeID && proof.NodeID == i.NodeID && proof.Fence == guard.config {
		return false, nil
	}
	after := i
	after.State = string(StateDraining)
	if err := m.guardInstanceRuntimeTransitionLocked(ctx, i, after); err != nil {
		return false, err
	}
	m.instances[i.ID] = after
	return true, nil
}

func (s *PgStore) ClaimStalePrimeWorker(ctx context.Context, app App, expected Instance) (bool, error) {
	guard, ok := ctx.Value(deploymentActivationAttemptKey{}).(deploymentActivationAttempt)
	if !ok || app.ID != guard.attempt.AppID || app.AccountID != guard.accountID {
		return false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := lockDeploymentActivationParentsTx(ctx, tx); err != nil {
		return false, err
	}
	q := sqlc.New()
	row, err := q.LockSnapshotPrimeAttempt(ctx, tx, mustPgUUID(guard.attempt.DeploymentID))
	if err != nil {
		return false, mapErr(err)
	}
	d := Deployment{ID: row.DeploymentID, AppID: row.AppID, Status: DeploymentStatus(row.Status), RootfsKey: row.RootfsKey, RootfsPath: row.RootfsPath, StageState: row.StageState}
	if err := checkDeploymentActivationAttemptTx(ctx, tx, d); err != nil {
		return false, err
	}
	worker, err := q.LockPrimeRecoveryWorker(ctx, tx, mustPgUUID(expected.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	i := Instance{ID: worker.ID, AppID: worker.AppID, DeploymentID: worker.DeploymentID, NodeID: worker.NodeID, WakeID: worker.WakeID, Mode: worker.Mode, State: worker.State}
	if worker.StartedAt.Valid {
		i.StartedAt = worker.StartedAt.Time
	}
	if worker.AppStatus != string(AppActive) || worker.AppNodeID != app.NodeID || !primeRecoveryWorkerMatches(guard, expected, i) {
		return false, nil
	}
	proof, err := q.ReadRuntimeInstanceConfigProof(ctx, tx, sqlc.ReadRuntimeInstanceConfigProofParams{
		AccountID: mustPgUUID(app.AccountID), AppID: mustPgUUID(app.ID), InstanceID: mustPgUUID(i.ID),
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	stored := RuntimeAppConfigFence{SecretFence: RuntimeAppSecretFence{DeploymentID: proof.DeploymentID, EnvironmentID: proof.EnvironmentID, Scope: proof.Scope, Fingerprint: proof.SecretFingerprint}, Fingerprint: proof.ConfigFingerprint}
	if i.State == string(StateRunning) && err == nil && stored == guard.config {
		return false, nil
	}
	changed, err := q.DrainPrimeRecoveryWorker(ctx, tx, sqlc.DrainPrimeRecoveryWorkerParams{InstanceID: mustPgUUID(i.ID), NodeID: mustPgUUID(i.NodeID), WakeID: mustPgUUID(i.WakeID)})
	if err != nil || changed != 1 {
		return false, err
	}
	return true, tx.Commit(ctx)
}
