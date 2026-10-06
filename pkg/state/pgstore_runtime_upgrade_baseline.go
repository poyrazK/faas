package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradeBaselineStore = (*PgStore)(nil)

func (s *PgStore) CaptureDeploymentRuntimeUpgradeBaseline(ctx context.Context, deploymentID, servingID string) (RuntimeUpgradeBaseline, error) {
	if validateRuntimeAppEnvIDs(deploymentID, servingID, deploymentID) != nil {
		return RuntimeUpgradeBaseline{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return RuntimeUpgradeBaseline{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockRuntimeUpgradeTargetApp(ctx, tx, mustPgUUID(deploymentID)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RuntimeUpgradeBaseline{}, ErrNotFound
		}
		return RuntimeUpgradeBaseline{}, runtimeUpgradeBaselineError(err)
	}
	if _, err := q.LockRuntimeUpgradeBaselineCandidate(ctx, tx, mustPgUUID(deploymentID)); err != nil {
		return RuntimeUpgradeBaseline{}, runtimeUpgradeBaselineError(err)
	}
	current, err := runtimeUpgradeBaselineDB(ctx, tx, deploymentID, servingID)
	if err != nil {
		return RuntimeUpgradeBaseline{}, err
	}
	row, err := q.InsertDeploymentRuntimeUpgradeBaseline(ctx, tx, sqlc.InsertDeploymentRuntimeUpgradeBaselineParams{
		DeploymentID: mustPgUUID(deploymentID), ServingDeploymentID: mustPgUUID(servingID), ServingRootfsKey: current.ServingRootfsKey,
		ServingRuntimeReleaseID: current.ServingRuntimeReleaseID, TargetReleaseID: current.TargetReleaseID,
		ConfigurationFingerprint: current.ConfigurationFingerprint, SecretFingerprint: current.SecretFingerprint,
		InputFingerprint: current.InputFingerprint, InputSecretFingerprint: current.InputSecretFingerprint,
	})
	if err != nil {
		return RuntimeUpgradeBaseline{}, runtimeUpgradeBaselineError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RuntimeUpgradeBaseline{}, runtimeUpgradeBaselineError(err)
	}
	return runtimeUpgradeBaselineFromRow(row), nil
}

func (s *PgStore) DeploymentRuntimeUpgradeBaseline(ctx context.Context, deploymentID string) (RuntimeUpgradeBaseline, error) {
	if validateRuntimeAppEnvIDs(deploymentID, deploymentID, deploymentID) != nil {
		return RuntimeUpgradeBaseline{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetDeploymentRuntimeUpgradeBaseline(ctx, s.pool, mustPgUUID(deploymentID))
	return runtimeUpgradeBaselineFromRow(row), mapErr(err)
}

func (s *PgStore) ValidateDeploymentRuntimeUpgradeBaseline(ctx context.Context, deploymentID string) error {
	if validateRuntimeAppEnvIDs(deploymentID, deploymentID, deploymentID) != nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := sqlc.New().GetDeploymentRuntimeUpgradeBaseline(ctx, tx, mustPgUUID(deploymentID))
	if err != nil {
		return mapErr(err) // only actual baseline absence may select legacy behavior
	}
	baseline := runtimeUpgradeBaselineFromRow(row)
	current, err := runtimeUpgradeBaselineDB(ctx, tx, deploymentID, baseline.ServingDeploymentID)
	if err != nil {
		return err
	}
	if !sameRuntimeUpgradeBaseline(baseline, current) {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}

func runtimeUpgradeBaselineDB(ctx context.Context, db sqlc.DBTX, deploymentID, servingID string) (RuntimeUpgradeBaseline, error) {
	q := sqlc.New()
	rows, err := q.ReadRuntimeUpgradeBaselineDeployments(ctx, db, sqlc.ReadRuntimeUpgradeBaselineDeploymentsParams{
		DeploymentID: mustPgUUID(deploymentID), ServingDeploymentID: mustPgUUID(servingID),
	})
	if err != nil {
		return RuntimeUpgradeBaseline{}, fmt.Errorf("read runtime upgrade deployments: %w", runtimeUpgradeBaselineError(err))
	}
	var candidate, serving Deployment
	deployments := make([]Deployment, 0, len(rows))
	for _, row := range rows {
		dep, err := runtimeUpgradeDeploymentFromRow(row)
		if err != nil {
			return RuntimeUpgradeBaseline{}, err
		}
		deployments = append(deployments, dep)
		if dep.ID == deploymentID {
			candidate = dep
		}
		if dep.ID == servingID {
			serving = dep
		}
	}
	if candidate.ID == "" || serving.ID == "" || !runtimeUpgradeServingStable(deployments, candidate, serving) {
		return RuntimeUpgradeBaseline{}, ErrConflict
	}
	appRow, err := q.AppByID(ctx, db, mustPgUUID(candidate.AppID))
	if err != nil {
		return RuntimeUpgradeBaseline{}, runtimeUpgradeBaselineError(err)
	}
	app := App{ID: candidate.AppID, AccountID: pgUUIDString(appRow.AccountID), Status: AppStatus(appRow.Status), Type: AppType(appRow.Type), Runtime: appRow.Runtime}
	if json.Unmarshal(appRow.Manifest, &app.Manifest) != nil {
		return RuntimeUpgradeBaseline{}, ErrConflict
	}
	pin, err := q.GetDeploymentRuntimeUpgradeTarget(ctx, db, mustPgUUID(deploymentID))
	if err != nil {
		return RuntimeUpgradeBaseline{}, runtimeUpgradeBaselineError(err)
	}
	if !pin.SourceMatches {
		return RuntimeUpgradeBaseline{}, ErrConflict
	}
	target, err := q.GetRuntimeRelease(ctx, db, pin.ID)
	if err != nil {
		return RuntimeUpgradeBaseline{}, runtimeUpgradeBaselineError(err)
	}
	current, err := q.GetArtifactRuntimeRelease(ctx, db, sqlc.GetArtifactRuntimeReleaseParams{AccountID: appRow.AccountID, RootfsKey: serving.RootfsKey})
	if err != nil {
		return RuntimeUpgradeBaseline{}, runtimeUpgradeBaselineError(err)
	}
	candidateValues, err := runtimeAppValuesDB(ctx, db, app.AccountID, app.ID, deploymentID)
	if err != nil {
		return RuntimeUpgradeBaseline{}, runtimeUpgradeBaselineError(err)
	}
	servingValues, err := runtimeAppValuesDB(ctx, db, app.AccountID, app.ID, servingID)
	if err != nil {
		return RuntimeUpgradeBaseline{}, runtimeUpgradeBaselineError(err)
	}
	return newRuntimeUpgradeBaseline(app, candidate, serving, runtimeReleaseFromRow(target), runtimeReleaseFromRow(current), candidateValues, servingValues)
}

func runtimeUpgradeDeploymentFromRow(row sqlc.ReadRuntimeUpgradeBaselineDeploymentsRow) (Deployment, error) {
	var artifact projectCloneArtifact
	if json.Unmarshal(row.Artifact, &artifact) != nil {
		return Deployment{}, ErrConflict
	}
	dep := artifact.deployment("", row.Scope, ProjectEnvironmentWorkloadSettings{})
	dep.ID, dep.AppID, dep.Status = row.ID, row.AppID, DeploymentStatus(row.Status)
	dep.SourceBytes, dep.SourceRoot = row.SourceBytes.Int64, row.SourceRoot
	dep.TrafficPercent, dep.TrafficPercentExplicit = int(row.TrafficPercent), row.TrafficPercentExplicit
	dep.MinInstances, dep.CanaryTotalSteps, dep.CanaryPreset = int(row.MinInstances), int(row.CanaryTotalSteps), row.CanaryPreset
	dep.CanaryStages, dep.RolloutState = row.CanaryStages, row.RolloutState
	dep.RollbackOn5xx = row.RollbackOn5xx
	dep.EnvironmentWorkloadRuntime = string(row.EnvironmentWorkloadRuntime)
	if row.DeletedAt.Valid {
		dep.DeletedAt = &row.DeletedAt.Time
	}
	return dep, nil
}

func runtimeUpgradeBaselineFromRow(row sqlc.DeploymentRuntimeUpgradeBaseline) RuntimeUpgradeBaseline {
	return RuntimeUpgradeBaseline{DeploymentID: pgUUIDString(row.DeploymentID), ServingDeploymentID: pgUUIDString(row.ServingDeploymentID),
		ServingRootfsKey: row.ServingRootfsKey, ServingRuntimeReleaseID: row.ServingRuntimeReleaseID, TargetReleaseID: row.TargetReleaseID,
		ConfigurationFingerprint: row.ConfigurationFingerprint, SecretFingerprint: row.SecretFingerprint,
		InputFingerprint: row.InputFingerprint, InputSecretFingerprint: row.InputSecretFingerprint, CapturedAt: row.CapturedAt.Time}
}

func runtimeUpgradeBaselineError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01") {
		return ErrConflict // a concurrent queue or intent write won
	}
	return runtimeSecretFenceError(err)
}
