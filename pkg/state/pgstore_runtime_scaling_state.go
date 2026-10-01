package state

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) RuntimeScalingStateForDeployment(ctx context.Context, accountID, appID, deploymentID string) (RuntimeScalingState, error) {
	if err := validateRuntimeAppEnvIDs(accountID, appID, deploymentID); err != nil {
		return RuntimeScalingState{}, err
	}
	row, err := sqlc.New().ReadRuntimeScalingStateForDeployment(ctx, s.pool, sqlc.ReadRuntimeScalingStateForDeploymentParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(deploymentID),
	})
	if err != nil {
		return RuntimeScalingState{}, mapErr(err)
	}
	result := RuntimeScalingState{RuntimeAppEnvSnapshot: RuntimeAppEnvSnapshot{AccountID: accountID, AppID: appID, DeploymentID: deploymentID, Scope: row.Scope, EnvironmentID: row.EnvironmentID}}
	if row.LastScaleInAt.Valid {
		stamp := row.LastScaleInAt.Time
		result.LastScaleInAt = &stamp
	}
	if row.LastScaleOutAt.Valid {
		stamp := row.LastScaleOutAt.Time
		result.LastScaleOutAt = &stamp
	}
	return result, nil
}

func (s *PgStore) StampDeploymentScaleIn(ctx context.Context, deploymentID string) error {
	return s.stampRuntimeScalingState(ctx, deploymentID, "in")
}
func (s *PgStore) StampDeploymentScaleOut(ctx context.Context, deploymentID string) error {
	return s.stampRuntimeScalingState(ctx, deploymentID, "out")
}

// Lock the exact original environment before its app, deployment and pins,
// matching deletion/publication ordering. No slug can adopt a recreated owner.
func (s *PgStore) stampRuntimeScalingState(ctx context.Context, deploymentID, direction string) error {
	id, err := uuid.Parse(deploymentID)
	if err != nil || id == uuid.Nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	deployment, err := q.ReadSnapshotPublicationDeployment(ctx, tx, mustPgUUID(deploymentID))
	if err != nil {
		return mapErr(err)
	}
	params := sqlc.ReadRuntimeScalingStateForDeploymentParams{AccountID: mustPgUUID(deployment.AccountID), AppID: mustPgUUID(deployment.AppID), DeploymentID: mustPgUUID(deploymentID)}
	owner, err := q.ReadRuntimeScalingStateForDeployment(ctx, tx, params)
	if err != nil {
		return mapErr(err)
	}
	if owner.EnvironmentID != "" {
		if _, err := q.LockRuntimeSecretEnvironment(ctx, tx, sqlc.LockRuntimeSecretEnvironmentParams{EnvironmentID: mustPgUUID(owner.EnvironmentID), AccountID: params.AccountID, AppID: params.AppID, Scope: owner.Scope}); err != nil {
			return mapErr(err)
		}
	}
	if _, err := q.LockSnapshotPublicationApp(ctx, tx, sqlc.LockSnapshotPublicationAppParams{AccountID: params.AccountID, AppID: params.AppID}); err != nil {
		return mapErr(err)
	}
	if _, err := q.LockSnapshotPublicationDeployment(ctx, tx, sqlc.LockSnapshotPublicationDeploymentParams{DeploymentID: params.DeploymentID, AppID: params.AppID}); err != nil {
		return mapErr(err)
	}
	if _, err := q.LockRuntimeSecretConfigurationPins(ctx, tx, params.DeploymentID); err != nil {
		return mapErr(err)
	}
	current, err := q.ReadRuntimeScalingStateForDeployment(ctx, tx, params)
	if err != nil {
		return mapErr(err)
	}
	if current.EnvironmentID != owner.EnvironmentID || current.Scope != owner.Scope {
		return ErrConflict
	}
	var environmentID pgtype.UUID
	if current.EnvironmentID != "" {
		environmentID = mustPgUUID(current.EnvironmentID)
	}
	key := runtimeScalingEnvironmentKey(current.Scope, current.EnvironmentID)
	if err := q.WriteRuntimeScalingState(ctx, tx, sqlc.WriteRuntimeScalingStateParams{
		AppID: params.AppID, EnvironmentKey: key, EnvironmentID: environmentID, Scope: workloadEnvironmentSlug(current.Scope), Direction: direction,
		PriorScaleIn: current.LastScaleInAt, PriorScaleOut: current.LastScaleOutAt,
	}); err != nil {
		return mapErr(err)
	}
	if workloadEnvironmentSlug(current.Scope) == "production" {
		if err := q.ProjectProductionScalingState(ctx, tx, sqlc.ProjectProductionScalingStateParams{AppID: params.AppID, EnvironmentKey: key}); err != nil {
			return mapErr(err)
		}
	}
	return tx.Commit(ctx)
}
