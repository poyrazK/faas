package state

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Lock the environment before reading its flag head. Flag writers take this
// same lock, so a publication proof remains valid through the graph commit.
func readCloneFeatureFlagsTx(ctx context.Context, db pgx.Tx, accountID, projectID, environment string) (FeatureFlagVersion, error) {
	return readCloneFeatureFlagsDB(ctx, db, accountID, projectID, environment, true)
}

// A false lockEnvironment is private to readers holding the configuration
// synchronization clock and project guard. They must not lock a config row
// behind a mutation waiting on that guard.
func readCloneFeatureFlagsDB(ctx context.Context, db sqlc.DBTX, accountID, projectID, environment string, lockEnvironment bool) (FeatureFlagVersion, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return FeatureFlagVersion{}, err
	}
	project, err := parsePgUUID(projectID)
	if err != nil {
		return FeatureFlagVersion{}, err
	}
	id, err := sqlc.New().ReadProjectEnvironmentCloneFlagScope(ctx, db, sqlc.ReadProjectEnvironmentCloneFlagScopeParams{
		AccountID: account, ProjectID: project, Environment: environment,
	})
	if err != nil {
		return FeatureFlagVersion{}, mapErr(err)
	}
	scope := FeatureFlagScope{AccountID: accountID, ProjectID: projectID, EnvironmentID: uuidString(id)}
	p, err := flagPGScope(scope)
	if err != nil {
		return FeatureFlagVersion{}, err
	}
	if lockEnvironment {
		if _, err := sqlc.New().LockFeatureFlagEnvironment(ctx, db, p); err != nil {
			return FeatureFlagVersion{}, mapErr(err)
		}
	}
	return flagPGGet(ctx, db, scope, p, 0)
}

func copyCloneFeatureFlagsTx(ctx context.Context, tx pgx.Tx, target ProjectEnvironment, snapshot projectCloneFeatureFlags) error {
	p, err := flagPGScope(FeatureFlagScope{AccountID: target.AccountID, ProjectID: target.ProjectID, EnvironmentID: target.ID})
	if err != nil {
		return err
	}
	raw, err := json.Marshal(snapshot.Config)
	if err != nil {
		return err
	}
	_, err = sqlc.New().InsertFeatureFlagVersion(ctx, tx, sqlc.InsertFeatureFlagVersionParams{
		AccountID: p.AccountID, ProjectID: p.ProjectID, EnvironmentID: p.EnvironmentID, Version: 1, Config: raw, Actor: "environment-clone",
	})
	return mapErr(err)
}
