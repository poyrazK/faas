package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type workerDeploymentActivationKey struct{}
type workerDeploymentActivation struct {
	attempt    snapshotPrimeAttempt
	instanceID string
	ephemeral  *Instance
}

// WithWorkerDeploymentActivation carries imaged's readiness recipient to the
// store's final activation transaction. It grants no bypass of other gates.
func WithWorkerDeploymentActivation(ctx context.Context, d Deployment, instanceID string) (context.Context, error) {
	attempt, err := snapshotPrimeAttemptFor(d)
	if err != nil {
		return ctx, err
	}
	if instanceID == "" {
		return ctx, ErrInvalidArgument
	}
	return context.WithValue(ctx, workerDeploymentActivationKey{}, workerDeploymentActivation{attempt: attempt, instanceID: instanceID}), nil
}

// WithEphemeralDeploymentActivation retains the exact stopped prime recipient.
// Its proof is checked with current inputs inside the final activation write.
func WithEphemeralDeploymentActivation(ctx context.Context, d Deployment, source Instance) (context.Context, error) {
	attempt, err := snapshotPrimeAttemptFor(d)
	if err != nil {
		return ctx, err
	}
	if source.ID == "" || source.AppID != d.AppID || source.DeploymentID != d.ID || source.State != string(StateStopped) ||
		(source.Mode != string(InstanceModeNormal) && source.Mode != string(InstanceModeService)) {
		return ctx, ErrInvalidArgument
	}
	return context.WithValue(ctx, workerDeploymentActivationKey{}, workerDeploymentActivation{attempt: attempt, instanceID: source.ID, ephemeral: &source}), nil
}

func (g workerDeploymentActivation) kind() string {
	if g.ephemeral != nil {
		return "ephemeral-secret"
	}
	return "worker"
}

func checkWorkerActivationInstance(expected workerDeploymentActivation, d Deployment, i Instance) error {
	if err := matchSnapshotPrimeAttempt(expected.attempt, d.AppID, d.ID, d); err != nil {
		return err
	}
	mode, status := string(InstanceModeWorker), string(StateRunning)
	if source := expected.ephemeral; source != nil {
		mode, status = source.Mode, string(StateStopped)
		if i.NodeID != source.NodeID || i.WakeID != source.WakeID || !i.StartedAt.Equal(source.StartedAt) {
			return fmt.Errorf("%w: ephemeral-secret readiness runtime changed", ErrConflict)
		}
	}
	if i.ID != expected.instanceID || i.AppID != d.AppID || i.DeploymentID != d.ID || i.Mode != mode || i.State != status ||
		(!expected.attempt.StartedAt.IsZero() && i.StartedAt.Before(expected.attempt.StartedAt)) {
		return fmt.Errorf("%w: %s readiness belongs to another attempt", ErrConflict, expected.kind())
	}
	return nil
}

func (m *MemStore) checkWorkerDeploymentActivationLocked(ctx context.Context, d Deployment) error {
	guard, ok := ctx.Value(workerDeploymentActivationKey{}).(workerDeploymentActivation)
	if !ok {
		return nil
	}
	i := m.instances[guard.instanceID]
	if err := checkWorkerActivationInstance(guard, d, i); err != nil {
		return err
	}
	proof, ok := m.runtimeInstanceConfigProofs[i.ID]
	if !ok || proof.WakeID != i.WakeID || proof.NodeID != i.NodeID {
		return fmt.Errorf("%w: %s readiness has no runtime proof", ErrConflict, guard.kind())
	}
	values, err := m.runtimeAppValuesLocked(m.apps[d.AppID].AccountID, d.AppID, d.ID)
	if err != nil {
		return err
	}
	current, err := NewRuntimeAppConfigFence(values)
	if err != nil || current != proof.Fence {
		return fmt.Errorf("%w: %s readiness configuration changed", ErrConflict, guard.kind())
	}
	return nil
}

func checkWorkerDeploymentActivationTx(ctx context.Context, tx pgx.Tx, d Deployment) error {
	guard, ok := ctx.Value(workerDeploymentActivationKey{}).(workerDeploymentActivation)
	if !ok {
		return nil
	}
	q := sqlc.New()
	i, accountID, err := lockDeploymentReadinessInstance(ctx, tx, guard)
	if err != nil {
		return err
	}
	if err := checkWorkerActivationInstance(guard, d, i); err != nil {
		return err
	}
	proof, err := q.ReadRuntimeInstanceConfigProof(ctx, tx, sqlc.ReadRuntimeInstanceConfigProofParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(d.AppID), InstanceID: mustPgUUID(i.ID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s readiness has no runtime proof", ErrConflict, guard.kind())
	}
	if err != nil {
		return fmt.Errorf("%s readiness runtime proof: %w", guard.kind(), err)
	}
	// The activation already owns the app/deployment parent locks in its
	// traffic transaction. Runtime writers invalidate those parents; use the
	// same transaction snapshot rather than acquiring environment locks later.
	values, err := runtimeAppValuesDB(ctx, tx, accountID, d.AppID, d.ID)
	if err != nil {
		return err
	}
	current, err := NewRuntimeAppConfigFence(values)
	stored := RuntimeAppConfigFence{SecretFence: RuntimeAppSecretFence{DeploymentID: proof.DeploymentID, EnvironmentID: proof.EnvironmentID, Scope: proof.Scope, Fingerprint: proof.SecretFingerprint}, Fingerprint: proof.ConfigFingerprint}
	if err != nil || current != stored {
		return fmt.Errorf("%w: %s readiness configuration changed", ErrConflict, guard.kind())
	}
	return nil
}

func lockDeploymentReadinessInstance(ctx context.Context, tx pgx.Tx, guard workerDeploymentActivation) (Instance, string, error) {
	q := sqlc.New()
	if guard.ephemeral != nil {
		row, err := q.LockPrimeRecoveryWorker(ctx, tx, mustPgUUID(guard.instanceID))
		if err != nil {
			return Instance{}, "", mapErr(err)
		}
		i := Instance{ID: row.ID, AppID: row.AppID, DeploymentID: row.DeploymentID, Mode: row.Mode, State: row.State, NodeID: row.NodeID, WakeID: row.WakeID}
		if row.StartedAt.Valid {
			i.StartedAt = row.StartedAt.Time
		}
		return i, row.AccountID, nil
	}
	row, err := q.LockWorkerDeploymentActivationInstance(ctx, tx, mustPgUUID(guard.instanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Instance{}, "", fmt.Errorf("%w: worker readiness instance is missing", ErrConflict)
	}
	if err != nil {
		return Instance{}, "", fmt.Errorf("worker readiness instance: %w", err)
	}
	i := Instance{ID: row.ID, AppID: row.AppID, DeploymentID: row.DeploymentID, Mode: row.Mode, State: row.State}
	if row.StartedAt.Valid {
		i.StartedAt = row.StartedAt.Time
	}
	return i, row.AccountID, nil
}
