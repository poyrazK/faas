package state

// adr: 051 — characterization boot: observed workload classification.

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) RecordObservedWorkloadClass(ctx context.Context, accountID, appID, instanceID string, class WorkloadClass) (bool, error) {
	if class == "" {
		return false, ErrInvalidArgument
	}
	if err := validateRuntimeAppEnvIDs(accountID, appID, instanceID); err != nil {
		return false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.ReadRuntimeInstanceConfigProof(ctx, tx, sqlc.ReadRuntimeInstanceConfigProofParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), InstanceID: mustPgUUID(instanceID),
	})
	if err != nil && !errors.Is(mapErr(err), ErrNotFound) {
		return false, mapErr(err)
	}
	proved := err == nil
	fence := RuntimeAppConfigFence{SecretFence: RuntimeAppSecretFence{DeploymentID: row.DeploymentID, EnvironmentID: row.EnvironmentID,
		Scope: row.Scope, Fingerprint: row.SecretFingerprint}, Fingerprint: row.ConfigFingerprint}
	// Secret writers' lock order: the environment, then the app exclusively,
	// so updating the app never upgrades a shared lock.
	if proved && fence.SecretFence.EnvironmentID != "" {
		if _, err := q.LockRuntimeSecretEnvironment(ctx, tx, sqlc.LockRuntimeSecretEnvironmentParams{
			EnvironmentID: mustPgUUID(fence.SecretFence.EnvironmentID), AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: fence.SecretFence.Scope,
		}); err != nil {
			if err = runtimeSecretFenceError(err); !errors.Is(err, ErrConflict) {
				return false, err
			}
			proved = false
		}
	}
	if _, err := q.LockRuntimeSecretAppExclusive(ctx, tx, sqlc.LockRuntimeSecretAppExclusiveParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)}); err != nil {
		return false, mapErr(err)
	}
	// The proof is current when the configuration still gives the
	// fingerprint the boot published with.
	current := false
	if proved {
		err := runtimeAppConfigFenceDB(ctx, tx, RuntimeInstancePublication{AccountID: accountID, AppID: appID, InstanceID: instanceID,
			Fence: fence.SecretFence, ConfigFence: fence})
		if err != nil && !errors.Is(err, ErrConflict) {
			return false, err
		}
		current = err == nil
	}
	n, err := q.SetObservedAppWorkloadClass(ctx, tx, sqlc.SetObservedAppWorkloadClassParams{
		WorkloadClass: string(class), AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID),
	})
	if err != nil {
		return false, mapErr(err)
	}
	if n == 0 {
		return false, ErrNotFound
	}
	restamped := false
	if current {
		snapshot, err := runtimeAppValuesDB(ctx, tx, accountID, appID, fence.SecretFence.DeploymentID)
		if err != nil {
			return false, runtimeSecretFenceError(err)
		}
		next, err := NewRuntimeAppConfigFence(snapshot)
		if err == nil && next.SecretFence == fence.SecretFence && next.Fingerprint != fence.Fingerprint {
			n, err := q.RestampRuntimeInstanceConfigProof(ctx, tx, sqlc.RestampRuntimeInstanceConfigProofParams{
				ConfigFingerprint: next.Fingerprint, InstanceID: mustPgUUID(instanceID), PreviousFingerprint: fence.Fingerprint,
			})
			if err != nil {
				return false, mapErr(err)
			}
			restamped = n == 1
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, mapErr(err)
	}
	return restamped, nil
}
