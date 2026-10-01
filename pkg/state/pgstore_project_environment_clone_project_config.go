package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readCloneProjectConfigDB(ctx context.Context, db sqlc.DBTX, accountID, projectID, environment string) (ProjectEnvironmentConfig, error) {
	r, err := new(sqlc.Queries).ReadProjectEnvironmentCloneProjectConfiguration(ctx, db, sqlc.ReadProjectEnvironmentCloneProjectConfigurationParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), Environment: environment,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironmentConfig{AccountID: accountID, ProjectID: projectID, EnvironmentSlug: environment,
			ConfigHash: api.EmptyProjectEnvironmentConfigHash(), Values: []byte(`{}`)}, nil
	}
	if err != nil {
		return ProjectEnvironmentConfig{}, mapErr(err)
	}
	return ProjectEnvironmentConfig{ID: r.ID, AccountID: accountID, ProjectID: projectID, EnvironmentSlug: environment,
		ConfigHash: r.ConfigHash, Values: r.ConfigJson}, nil
}

func verifyCloneProjectConfigTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, resources []ProjectEnvironmentCloneResource) error {
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil || len(records) == 0 {
		return err
	}
	target, err := readCloneProjectConfigDB(ctx, tx, op.AccountID, op.ProjectID, op.TargetEnvironment)
	if err != nil {
		return err
	}
	if err := validateCloneProjectConfigProof(op, resources, records, target); err != nil {
		return err
	}
	captured, err := capturedCloneProjectConfig(records)
	if err != nil {
		return err
	}
	flags, err := readCloneFeatureFlagsTx(ctx, tx, op.AccountID, op.ProjectID, op.TargetEnvironment)
	if err != nil {
		return err
	}
	return validateCloneFeatureFlagsProof(captured.FeatureFlags, flags)
}
