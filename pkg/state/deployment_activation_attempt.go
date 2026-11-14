package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrDeploymentActivationStale retires verification whose deployment attempt
// or runtime inputs changed before the final activation transaction.
var ErrDeploymentActivationStale = fmt.Errorf("%w: deployment activation attempt or configuration changed", ErrConflict)

type deploymentActivationAttemptKey struct{}
type deploymentActivationAttempt struct {
	attempt   snapshotPrimeAttempt
	accountID string
	config    RuntimeAppConfigFence
}

// WithDeploymentActivationAttempt binds verification to the inputs observed
// before activation. Stage progress and verification receipts may
// advance independently; a new preparation or runtime edit must verify again.
func WithDeploymentActivationAttempt(ctx context.Context, d Deployment, values RuntimeAppValuesSnapshot) (context.Context, error) {
	attempt, err := snapshotPrimeAttemptFor(d)
	if err != nil {
		return ctx, err
	}
	if values.AppID != d.AppID || values.DeploymentID != d.ID {
		return ctx, ErrInvalidArgument
	}
	fence, err := NewRuntimeAppConfigFence(values)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, deploymentActivationAttemptKey{}, deploymentActivationAttempt{attempt, values.AccountID, fence}), nil
}

func checkActivationSnapshotSource(ctx context.Context, startedAt time.Time) error {
	expected, ok := ctx.Value(deploymentActivationAttemptKey{}).(deploymentActivationAttempt)
	// Legacy snapshot writers have no source timestamp. Preserve their existing
	// publication checks; modern captures must belong to this preparation.
	if ok && !startedAt.IsZero() && !expected.attempt.StartedAt.IsZero() && startedAt.Before(expected.attempt.StartedAt) {
		return ErrSnapshotRuntimeStale
	}
	return nil
}

func matchDeploymentActivationAttempt(expected deploymentActivationAttempt, d Deployment, values RuntimeAppValuesSnapshot) error {
	if err := matchSnapshotPrimeAttempt(expected.attempt, d.AppID, d.ID, d); err != nil {
		return ErrDeploymentActivationStale
	}
	current, err := NewRuntimeAppConfigFence(values)
	if err != nil {
		return err
	}
	if current != expected.config {
		return ErrDeploymentActivationStale
	}
	return nil
}

func (m *MemStore) checkDeploymentActivationAttemptLocked(ctx context.Context, d Deployment) error {
	expected, ok := ctx.Value(deploymentActivationAttemptKey{}).(deploymentActivationAttempt)
	if !ok {
		return nil
	}
	if err := matchSnapshotPrimeAttempt(expected.attempt, d.AppID, d.ID, d); err != nil {
		return ErrDeploymentActivationStale
	}
	values, err := m.runtimeAppValuesLocked(expected.accountID, d.AppID, d.ID)
	if err != nil {
		return err
	}
	return matchDeploymentActivationAttempt(expected, d, values)
}

func checkDeploymentActivationAttemptTx(ctx context.Context, tx pgx.Tx, d Deployment) error {
	expected, ok := ctx.Value(deploymentActivationAttemptKey{}).(deploymentActivationAttempt)
	if !ok {
		return nil
	}
	if err := matchSnapshotPrimeAttempt(expected.attempt, d.AppID, d.ID, d); err != nil {
		return ErrDeploymentActivationStale
	}
	// Callers own the app/deployment parent locks, as runtime writers do.
	values, err := runtimeAppValuesDB(ctx, tx, expected.accountID, d.AppID, d.ID)
	if err != nil {
		return err
	}
	return matchDeploymentActivationAttempt(expected, d, values)
}

// Failure finalization and prime retirement need the same parent ordering as
// snapshot publication. Lock the environment before the app, then let the
// caller lock the deployment and instance before checking the captured inputs.
func lockDeploymentActivationParentsTx(ctx context.Context, tx pgx.Tx) error {
	expected, ok := ctx.Value(deploymentActivationAttemptKey{}).(deploymentActivationAttempt)
	if !ok {
		return nil
	}
	q := sqlc.New()
	fence := expected.config.SecretFence
	if fence.EnvironmentID != "" {
		if _, err := q.LockRuntimeSecretEnvironment(ctx, tx, sqlc.LockRuntimeSecretEnvironmentParams{
			EnvironmentID: mustPgUUID(fence.EnvironmentID), AccountID: mustPgUUID(expected.accountID), AppID: mustPgUUID(expected.attempt.AppID), Scope: fence.Scope,
		}); err != nil {
			return snapshotPublicationOwnerError(err)
		}
	}
	_, err := q.LockSnapshotPublicationApp(ctx, tx, sqlc.LockSnapshotPublicationAppParams{
		AccountID: mustPgUUID(expected.accountID), AppID: mustPgUUID(expected.attempt.AppID),
	})
	return err
}

// Generic activation failures must obey the same preparation/configuration
// fence as promotion, before changing stage history, traffic, or diagnostics.
// Other failure callers retain their existing contract without this context.
func checkDeploymentActivationFailureTx(ctx context.Context, tx pgx.Tx, id string) error {
	if _, ok := ctx.Value(deploymentActivationAttemptKey{}).(deploymentActivationAttempt); !ok {
		return nil
	}
	if err := lockDeploymentActivationParentsTx(ctx, tx); err != nil {
		return err
	}
	row, err := sqlc.New().LockSnapshotPrimeFailure(ctx, tx, mustPgUUID(id))
	if err != nil {
		return mapErr(err)
	}
	d := Deployment{ID: row.DeploymentID, AppID: row.AppID, Status: DeploymentStatus(row.Status), RootfsKey: row.RootfsKey, RootfsPath: row.RootfsPath, StageState: row.StageState}
	if err := checkDeploymentActivationAttemptTx(ctx, tx, d); err != nil {
		return err
	}
	return activationReadinessFailure(checkWorkerDeploymentActivationTx(ctx, tx, d))
}

func (m *MemStore) checkDeploymentActivationFailureLocked(ctx context.Context, d Deployment) error {
	if _, ok := ctx.Value(deploymentActivationAttemptKey{}).(deploymentActivationAttempt); !ok {
		return nil
	}
	if err := m.checkDeploymentActivationAttemptLocked(ctx, d); err != nil {
		return err
	}
	return activationReadinessFailure(m.checkWorkerDeploymentActivationLocked(ctx, d))
}

// A stale readiness hint cannot authorize a failure any more than a promotion.
// Preserve read failures so an unavailable proof can be retried safely.
func activationReadinessFailure(err error) error {
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
		return ErrDeploymentActivationStale
	}
	return err
}
