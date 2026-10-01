package state

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func runtimeAppSecretFenceDB(ctx context.Context, db sqlc.DBTX, accountID, appID, instanceID string, fence RuntimeAppSecretFence) (string, error) {
	queries := sqlc.New()
	// Match environment deletion and deployment publication lock order.
	if fence.EnvironmentID != "" {
		if _, err := queries.LockRuntimeSecretEnvironment(ctx, db, sqlc.LockRuntimeSecretEnvironmentParams{
			EnvironmentID: mustPgUUID(fence.EnvironmentID), AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: fence.Scope,
		}); err != nil {
			return "", runtimeSecretFenceError(err)
		}
	}
	if _, err := queries.LockRuntimeSecretApp(ctx, db, sqlc.LockRuntimeSecretAppParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)}); err != nil {
		return "", runtimeSecretFenceError(err)
	}
	owner, err := queries.LockRuntimeSecretOwner(ctx, db, sqlc.LockRuntimeSecretOwnerParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), InstanceID: mustPgUUID(instanceID), RequireActive: !fence.empty(),
	})
	if err != nil {
		return "", runtimeSecretFenceError(err)
	}
	if fence.empty() && owner.RequiresFence {
		return "", ErrConflict
	}
	deploymentID := pgUUIDString(owner.DeploymentID)
	if !fence.empty() && (fence.DeploymentID != deploymentID || fence.Scope != owner.Scope) {
		return "", ErrConflict
	}
	if _, err := queries.LockRuntimeSecretConfigurationPins(ctx, db, owner.DeploymentID); err != nil {
		return "", runtimeSecretFenceError(err)
	}
	if _, err := queries.LockRuntimeSecretSidecarSignals(ctx, db, owner.DeploymentID); err != nil {
		return "", runtimeSecretFenceError(err)
	}
	// Serialize observations and rotations in sorted key order. FOR UPDATE
	// avoids competing reporters upgrading shared secret-row locks.
	if _, err := queries.LockRuntimeSecretRows(ctx, db, sqlc.LockRuntimeSecretRowsParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: owner.Scope,
	}); err != nil {
		return "", runtimeSecretFenceError(err)
	}
	if !fence.empty() {
		snapshot, err := runtimeAppValuesDB(ctx, db, accountID, appID, deploymentID)
		if err != nil {
			return "", runtimeSecretFenceError(err)
		}
		current, err := NewRuntimeAppSecretFence(snapshot)
		if err != nil || current != fence {
			return "", ErrConflict
		}
	}
	return owner.Scope, nil
}

func runtimeSecretFenceError(err error) error {
	err = mapErr(err)
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	return err
}
