package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func releaseConfigCaptureDB(ctx context.Context, db sqlc.DBTX, accountID, projectID, baselineID string) (projectReleaseConfigCapture, error) {
	raw, err := sqlc.New().ReadProjectReleaseConfigurationCapture(ctx, db, sqlc.ReadProjectReleaseConfigurationCaptureParams{AccountID: accountID, ProjectID: projectID, BaselineID: baselineID})
	if err != nil {
		return projectReleaseConfigCapture{}, mapErr(err)
	}
	var capture projectReleaseConfigCapture
	if err := json.Unmarshal(raw, &capture); err != nil {
		return capture, err
	}
	return capture, nil
}
func (s *PgStore) PreviewProjectReleaseConfiguration(ctx context.Context, accountID, projectID, baselineID string, request json.RawMessage) (*api.ProjectReleaseConfiguration, error) {
	capture, err := releaseConfigCaptureDB(ctx, s.pool, accountID, projectID, baselineID)
	if err != nil {
		return nil, err
	}
	if err := loadReleaseSecretVersionsDB(ctx, s.pool, &capture, accountID, request); err != nil {
		return nil, err
	}
	return capture.prepare(accountID, request, api.MustLimitsFor(capture.Plan))
}
func lockReleaseConfigurationEnvironmentDB(ctx context.Context, db sqlc.DBTX, accountID, projectID string) error {
	q := sqlc.New()
	if _, err := q.LockEnvironmentGitSourceForScope(ctx, db, sqlc.LockEnvironmentGitSourceForScopeParams{AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), Environment: "production"}); err != nil {
		return err
	}
	return q.LockSafeProjectConfigEnvironment(ctx, db, sqlc.LockSafeProjectConfigEnvironmentParams{AccountID: accountID, ProjectID: projectID})
}
func (s *PgStore) StageProjectReleaseConfiguration(ctx context.Context, accountID, attempt, projectID string, safe *api.ProjectSafeRelease, request json.RawMessage) error {
	if safe.Configuration == nil {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	entry, err := q.LockSafeProjectSubmission(ctx, tx, sqlc.LockSafeProjectSubmissionParams{AccountID: accountID, AttemptID: attempt})
	if err != nil {
		return mapErr(err)
	}
	if entry.Status != "processing" {
		return ErrConflict
	}
	if err := lockReleaseConfigurationEnvironmentDB(ctx, tx, accountID, projectID); err != nil {
		return err
	}
	if err := q.LockSafeProjectReleaseProject(ctx, tx, sqlc.LockSafeProjectReleaseProjectParams{AccountID: accountID, ProjectID: projectID}); err != nil {
		return err
	}
	if err := q.LockSafeProjectReleaseApps(ctx, tx, projectID); err != nil {
		return err
	}
	capture, err := releaseConfigCaptureDB(ctx, tx, accountID, projectID, safe.PreviousReleaseID)
	if err != nil {
		return err
	}
	if err := loadReleaseSecretVersionsDB(ctx, tx, &capture, accountID, request); err != nil {
		return err
	}
	descriptor, err := capture.prepare(accountID, request, api.MustLimitsFor(capture.Plan))
	if err != nil {
		return err
	}
	old, err := q.ReadStagedProjectReleaseConfiguration(ctx, tx, sqlc.ReadStagedProjectReleaseConfigurationParams{AccountID: accountID, AttemptID: attempt})
	if err == nil {
		var prior projectReleaseConfigCapture
		if json.Unmarshal(old, &prior) != nil {
			return ErrConflict
		}
		capture.reuseSecretEnvironmentProof(prior, descriptor)
		if !matchingReleaseConfiguration(descriptor, safe.Configuration) || !matchingReleaseConfiguration(prior.Descriptor, descriptor) {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(mapErr(err), ErrNotFound) {
		return err
	}
	if !matchingReleaseConfiguration(descriptor, safe.Configuration) {
		return ErrConflict
	}
	raw, err := json.Marshal(capture)
	if err != nil {
		return err
	}
	if err := q.StageProjectReleaseConfiguration(ctx, tx, sqlc.StageProjectReleaseConfigurationParams{AccountID: accountID, AttemptID: attempt, Payload: raw}); err != nil {
		return err
	}
	for _, w := range capture.Workloads {
		if capture.Descriptor.Secrets != nil {
			rows := []releaseSecretVersion{}
			if w.SecretSnapshot != nil {
				rows = cloneReleaseSecrets(*w.SecretSnapshot)
			} else {
				for _, row := range w.LiveSecrets {
					rows = append(rows, releaseSecretFromRow(w.AppID, row))
				}
			}
			if err := pinReleaseSecretsDB(ctx, tx, w.DeploymentID, rows); err != nil {
				return err
			}
		}
		if err := pinReleaseEnvironmentDB(ctx, tx, w.DeploymentID, w.Legacy); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func pinReleaseEnvironmentDB(ctx context.Context, db sqlc.DBTX, deploymentID string, rows []projectReleaseVariable) error {
	raw, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	return sqlc.New().PinDeploymentReleaseEnvironment(ctx, db, sqlc.PinDeploymentReleaseEnvironmentParams{DeploymentID: deploymentID, ValuesJson: raw})
}
func candidateReleaseConfigurationDB(ctx context.Context, db sqlc.DBTX, d Deployment) ([]projectReleaseVariable, error) {
	q := sqlc.New()
	app, err := q.ReleaseConfigurationCandidateOwner(ctx, db, d.AppID)
	if err != nil {
		return nil, mapErr(err)
	}
	raw, err := q.ReadStagedProjectReleaseConfiguration(ctx, db, sqlc.ReadStagedProjectReleaseConfigurationParams{AccountID: app.AccountID, AttemptID: d.releaseConfigAttempt})
	if err != nil {
		return nil, mapErr(err)
	}
	var capture projectReleaseConfigCapture
	if json.Unmarshal(raw, &capture) != nil {
		return nil, ErrConflict
	}
	if app.ProjectID != capture.ProjectID {
		return nil, ErrConflict
	}
	current, err := releaseConfigCaptureDB(ctx, db, app.AccountID, app.ProjectID, capture.BaselineID)
	if err != nil {
		return nil, err
	}
	if err := loadReleaseSecretVersionsDB(ctx, db, &current, app.AccountID, capture.Request); err != nil {
		return nil, err
	}
	descriptor, err := current.prepare(app.AccountID, capture.Request, api.MustLimitsFor(current.Plan))
	current.reuseSecretEnvironmentProof(capture, descriptor)
	if err != nil || !matchingReleaseConfiguration(descriptor, capture.Descriptor) {
		return nil, ErrConflict
	}
	return candidateReleaseVariables(capture, d)
}
func guardReleaseConfigurationDB(ctx context.Context, db sqlc.DBTX, accountID, attempt, projectID string, safe *api.ProjectSafeRelease, rollback bool) (projectReleaseConfigCapture, error) {
	raw, err := sqlc.New().ReadStagedProjectReleaseConfiguration(ctx, db, sqlc.ReadStagedProjectReleaseConfigurationParams{AccountID: accountID, AttemptID: attempt})
	if err != nil {
		return projectReleaseConfigCapture{}, mapErr(err)
	}
	var capture projectReleaseConfigCapture
	if json.Unmarshal(raw, &capture) != nil {
		return capture, ErrConflict
	}
	active := safe.PreviousReleaseID
	if rollback {
		active = safe.ReleaseID
	}
	current, err := releaseConfigCaptureDB(ctx, db, accountID, projectID, active)
	if err != nil {
		return capture, err
	}
	descriptor, err := current.prepare(accountID, nil, api.MustLimitsFor(current.Plan))
	if err != nil || descriptor == nil {
		return capture, ErrConflict
	}
	expectedVersion, expectedHash := safe.Configuration.PreviousVersion, safe.Configuration.PreviousHash
	if rollback {
		expectedVersion, expectedHash = safe.Configuration.TargetVersion, safe.Configuration.TargetHash
	}
	current.reuseSecretEnvironmentProof(capture, descriptor)
	if descriptor.PreviousVersion != expectedVersion || descriptor.PreviousHash != expectedHash || descriptor.EnvironmentHash != safe.Configuration.EnvironmentHash {
		return capture, ErrConflict
	}
	return capture, nil
}
func appendReleaseConfigurationDB(ctx context.Context, db sqlc.DBTX, accountID, projectID string, version int64, values json.RawMessage, hash string) (int64, error) {
	return sqlc.New().AppendProjectReleaseConfigurationVersion(ctx, db, sqlc.AppendProjectReleaseConfigurationVersionParams{AccountID: accountID, ProjectID: projectID, Version: version, ConfigHash: hash, ConfigJson: values})
}

func inheritedReleaseVariablesDB(ctx context.Context, db sqlc.DBTX, appID, scope string) ([]projectReleaseVariable, error) {
	raw, err := sqlc.New().ReadActiveReleaseEnvironmentForApp(ctx, db, sqlc.ReadActiveReleaseEnvironmentForAppParams{AppID: appID, Scope: scope})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var rows []projectReleaseVariable
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
