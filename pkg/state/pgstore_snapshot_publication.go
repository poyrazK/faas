package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PublishSnapshotIfRuntimeFresh holds the original environment before the app
// and deployment, matching environment deletion's lock order. The app lock also
// serializes publication with the existing configuration-change stamp trigger.
func (s *PgStore) PublishSnapshotIfRuntimeFresh(ctx context.Context, snap Snapshot, sourceInstanceID string, sourceStartedAt time.Time) (Snapshot, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	deployment, err := q.ReadSnapshotPublicationDeployment(ctx, tx, mustPgUUID(snap.DeploymentID))
	if err != nil {
		return Snapshot{}, mapErr(err)
	}
	params := sqlc.ReadRuntimeAppEnvForDeploymentParams{
		AccountID: mustPgUUID(deployment.AccountID), AppID: mustPgUUID(deployment.AppID), DeploymentID: mustPgUUID(snap.DeploymentID),
	}
	owner, err := q.ReadRuntimeAppEnvForDeployment(ctx, tx, params)
	if err != nil {
		return Snapshot{}, snapshotPublicationOwnerError(err)
	}
	if sourceInstanceID == "" && invocationStageScope(owner.Scope) {
		return Snapshot{}, ErrSnapshotRuntimeStale
	}
	if owner.EnvironmentID != "" {
		if _, err := q.LockRuntimeSecretEnvironment(ctx, tx, sqlc.LockRuntimeSecretEnvironmentParams{
			EnvironmentID: mustPgUUID(owner.EnvironmentID), AccountID: params.AccountID, AppID: params.AppID, Scope: owner.Scope,
		}); err != nil {
			return Snapshot{}, snapshotPublicationOwnerError(err)
		}
	}
	if _, err := q.LockSnapshotPublicationApp(ctx, tx, sqlc.LockSnapshotPublicationAppParams{AccountID: params.AccountID, AppID: params.AppID}); err != nil {
		return Snapshot{}, snapshotPublicationOwnerError(err)
	}
	if _, err := q.LockSnapshotPublicationDeployment(ctx, tx, sqlc.LockSnapshotPublicationDeploymentParams{AppID: params.AppID, DeploymentID: params.DeploymentID}); err != nil {
		return Snapshot{}, snapshotPublicationOwnerError(err)
	}
	if _, err := q.LockRuntimeSecretConfigurationPins(ctx, tx, params.DeploymentID); err != nil {
		return Snapshot{}, snapshotPublicationOwnerError(err)
	}
	current, err := q.ReadRuntimeAppEnvForDeployment(ctx, tx, params)
	if err != nil {
		return Snapshot{}, snapshotPublicationOwnerError(err)
	}
	if current.EnvironmentID != owner.EnvironmentID || current.Scope != owner.Scope {
		return Snapshot{}, ErrSnapshotRuntimeStale
	}
	if sourceInstanceID != "" {
		source, err := q.ReadSnapshotPublicationSource(ctx, tx, mustPgUUID(sourceInstanceID))
		if err != nil {
			return Snapshot{}, snapshotPublicationOwnerError(err)
		}
		if source.AppID != deployment.AppID || source.DeploymentID != snap.DeploymentID || sourceStartedAt.IsZero() ||
			!source.StartedAt.Valid || !sourceStartedAt.Equal(source.StartedAt.Time) {
			return Snapshot{}, ErrSnapshotRuntimeStale
		}
	}
	changedAt, changed, err := readEnvironmentRuntimeChangedAt(ctx, tx, deployment.AppID, owner.Scope)
	if err != nil {
		return Snapshot{}, err
	}
	inputs, haveReceipt, err := readInstanceRuntimeConfigReceipt(ctx, tx, sourceInstanceID)
	if err != nil {
		return Snapshot{}, err
	}
	required, err := q.RuntimeConfigReceiptRequired(ctx, tx, sqlc.RuntimeConfigReceiptRequiredParams{AppID: params.AppID, Scope: owner.Scope})
	if err != nil {
		return Snapshot{}, mapErr(err)
	}
	if haveReceipt {
		fresh, err := readRuntimeConfigInputsFresh(ctx, tx, deployment.AppID, inputs)
		if err != nil {
			return Snapshot{}, err
		}
		if inputs.Scope != normalizedDeploymentScope(owner.Scope) || !fresh {
			return Snapshot{}, ErrSnapshotRuntimeStale
		}
	} else if required || changed && !sourceStartedAt.After(changedAt) {
		return Snapshot{}, ErrSnapshotRuntimeStale
	}
	stored, err := createSnapshotWithQuerier(ctx, tx, snap)
	if err != nil {
		return Snapshot{}, err
	}
	if haveReceipt {
		variables, secrets, refs, sidecars := runtimeConfigInputsJSON(inputs)
		if err := q.InsertSnapshotRuntimeConfigReceipt(ctx, tx, sqlc.InsertSnapshotRuntimeConfigReceiptParams{
			SnapshotID: mustPgUUID(stored.ID), Scope: inputs.Scope, BoundaryAt: gitOpsTime(inputs.Boundary),
			Variables: variables, SecretVersions: secrets, SecretRefs: refs, SidecarSecretVersions: sidecars, AllSecrets: inputs.AllSecrets,
		}); err != nil {
			return Snapshot{}, mapErr(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Snapshot{}, err
	}
	return stored, nil
}

func snapshotPublicationOwnerError(err error) error {
	err = mapErr(err)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
		return ErrSnapshotRuntimeStale
	}
	return err
}
