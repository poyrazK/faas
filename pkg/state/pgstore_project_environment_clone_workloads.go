package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func lockCloneWorkloadOperationTx(ctx context.Context, tx pgx.Tx, accountID, projectID, operationID string) (ProjectEnvironmentCloneOperation, error) {
	q := new(sqlc.Queries)
	if _, err := q.LockProjectEnvironmentCloneProject(ctx, tx, sqlc.LockProjectEnvironmentCloneProjectParams{AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID)}); err != nil {
		return ProjectEnvironmentCloneOperation{}, mapErr(err)
	}
	return lockCloneWorkloadOperationRowTx(ctx, tx, accountID, projectID, operationID)
}

// Configuration-fence callers synchronize project scope through their clock
// and guard. Ordinary workload mutations use the project-locking wrapper above.
func lockCloneWorkloadOperationRowTx(ctx context.Context, tx pgx.Tx, accountID, projectID, operationID string) (ProjectEnvironmentCloneOperation, error) {
	q := new(sqlc.Queries)
	r, err := q.LockProjectEnvironmentCloneWorkloadOperation(ctx, tx, sqlc.LockProjectEnvironmentCloneWorkloadOperationParams{AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), OperationID: mustPgUUID(operationID)})
	if err != nil {
		return ProjectEnvironmentCloneOperation{}, mapErr(err)
	}
	if !r.LeaseLive {
		return ProjectEnvironmentCloneOperation{}, ErrConflict
	}
	op := ProjectEnvironmentCloneOperation{ID: operationID, AccountID: accountID, ProjectID: projectID, SourceEnvironment: r.SourceEnvironment,
		TargetEnvironment: r.TargetEnvironment, SourceReleaseSetID: r.SourceReleaseSetID, SourceRevisionHash: r.SourceRevisionHash, Status: r.Status, Revision: r.Revision,
		TargetReleaseSetID: r.TargetReleaseSetID, ErrorCode: r.ErrorCode}
	if err := json.Unmarshal(r.Resources, &op.Resources); err != nil {
		return ProjectEnvironmentCloneOperation{}, ErrConflict
	}
	return op, nil
}

func cloneWorkloadRecordsDB(ctx context.Context, db sqlc.DBTX, accountID, projectID, operationID string) ([]projectCloneWorkloadRecord, error) {
	rows, err := new(sqlc.Queries).ReadProjectEnvironmentCloneWorkloads(ctx, db, sqlc.ReadProjectEnvironmentCloneWorkloadsParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), OperationID: mustPgUUID(operationID)})
	if err != nil {
		return nil, mapErr(err)
	}
	records := make([]projectCloneWorkloadRecord, len(rows))
	for i, row := range rows {
		records[i], err = decodeCloneWorkloadRecord(operationID, row.AppID, row.SourceDeploymentID, row.SourceHash, row.TargetDeploymentID, row.TargetSettingsHash, row.Snapshot)
		if err != nil {
			return nil, err
		}
	}
	if _, err := verifyCloneConfigurationCaptureDB(ctx, db, accountID, projectID, operationID, records); err != nil {
		return nil, err
	}
	return records, nil
}

func cloneWorkloadRecordViews(records []projectCloneWorkloadRecord) []ProjectEnvironmentCloneWorkload {
	views := make([]ProjectEnvironmentCloneWorkload, len(records))
	for i, record := range records {
		views[i] = record.ProjectEnvironmentCloneWorkload
	}
	sort.Slice(views, func(i, j int) bool { return views[i].AppID < views[j].AppID })
	return views
}

func (s *PgStore) ProjectEnvironmentCloneWorkloads(ctx context.Context, accountID, projectID, operationID string) ([]ProjectEnvironmentCloneWorkload, error) {
	if _, err := s.ProjectEnvironmentCloneOperationByID(ctx, accountID, projectID, operationID); err != nil {
		return nil, err
	}
	records, err := cloneWorkloadRecordsDB(ctx, s.pool, accountID, projectID, operationID)
	return cloneWorkloadRecordViews(records), err
}

func (s *PgStore) CaptureProjectEnvironmentCloneWorkloads(ctx context.Context, accountID, projectID, operationID string, revision int64) ([]ProjectEnvironmentCloneWorkload, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := lockCloneWorkloadOperationTx(ctx, tx, accountID, projectID, operationID)
	if err != nil {
		return nil, mapProjectCloneSnapshotErr(err)
	}
	if op.Status != CloneOperationCapturing || op.Revision != revision {
		return nil, ErrConflict
	}
	existing, err := cloneWorkloadRecordsDB(ctx, tx, accountID, projectID, operationID)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return cloneWorkloadRecordViews(existing), nil
	}
	q := new(sqlc.Queries)
	releaseID, err := q.ReadProjectEnvironmentCloneSourceRelease(ctx, tx, sqlc.ReadProjectEnvironmentCloneSourceReleaseParams{ProjectID: mustPgUUID(projectID), Environment: op.SourceEnvironment})
	if err != nil {
		return nil, mapErr(err)
	}
	if releaseID != op.SourceReleaseSetID {
		return nil, ErrConflict
	}
	clone := ProjectEnvironmentClone{AccountID: accountID, ProjectID: projectID, SourceSlug: op.SourceEnvironment}
	if err := lockProjectEnvironmentCloneSource(ctx, tx, clone); err != nil {
		return nil, mapProjectCloneSnapshotErr(err)
	}
	scopesJSON, err := projectCloneValueScopesTx(ctx, tx, clone)
	if err != nil {
		return nil, mapProjectCloneSnapshotErr(err)
	}
	var scopes map[string]string
	if err := json.Unmarshal(scopesJSON, &scopes); err != nil {
		return nil, err
	}
	if len(scopes) == 0 {
		return nil, ErrConflict
	}
	var records []projectCloneWorkloadRecord
	for appID, scope := range scopes {
		snapshot, err := captureCloneWorkloadTx(ctx, tx, op, appID, scope)
		if err != nil {
			return nil, mapProjectCloneSnapshotErr(err)
		}
		raw, hash, err := encodeCloneWorkloadSnapshot(snapshot)
		if err != nil {
			return nil, err
		}
		if err := q.InsertProjectEnvironmentCloneWorkload(ctx, tx, sqlc.InsertProjectEnvironmentCloneWorkloadParams{
			OperationID: mustPgUUID(operationID), AppID: mustPgUUID(appID), SourceDeploymentID: mustPgUUID(snapshot.Artifact.ID), SourceHash: hash, Snapshot: raw}); err != nil {
			return nil, mapProjectCloneSnapshotErr(err)
		}
		record, err := decodeCloneWorkloadRecord(operationID, appID, snapshot.Artifact.ID, hash, "", "", raw)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := pinCloneLayerArtifactsTx(ctx, tx, records); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapProjectCloneSnapshotErr(err)
	}
	return cloneWorkloadRecordViews(records), nil
}

func captureCloneWorkloadTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, appID, scope string) (projectCloneWorkloadSnapshot, error) {
	return captureCloneWorkloadDB(ctx, tx, op, appID, scope, true)
}

func captureCloneWorkloadDB(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, appID, scope string, lockFlags bool) (projectCloneWorkloadSnapshot, error) {
	q := new(sqlc.Queries)
	snapshot := projectCloneWorkloadSnapshot{SidecarSignals: map[string]string{}}
	artifact, err := q.ReadProjectEnvironmentCloneSelectedArtifact(ctx, tx, sqlc.ReadProjectEnvironmentCloneSelectedArtifactParams{AppID: mustPgUUID(appID), SourceScope: scope, ReleaseID: op.SourceReleaseSetID})
	if err != nil {
		return snapshot, mapErr(err)
	}
	if err := json.Unmarshal(artifact, &snapshot.Artifact); err != nil {
		return snapshot, ErrConflict
	}
	if snapshot.Artifact.AppID != appID || snapshot.Artifact.Scope != scope {
		return snapshot, ErrConflict
	}
	config, err := readCloneProjectConfigDB(ctx, tx, op.AccountID, op.ProjectID, op.SourceEnvironment)
	if err != nil {
		return snapshot, err
	}
	flagSource, err := readCloneFeatureFlagsDB(ctx, tx, op.AccountID, op.ProjectID, op.SourceEnvironment, lockFlags)
	if err != nil {
		return snapshot, err
	}
	flagSnapshot, err := captureCloneFeatureFlags(flagSource)
	if err != nil {
		return snapshot, err
	}
	projectConfig, err := normalizeCloneProjectConfig(projectCloneProjectConfig{Hash: config.ConfigHash, Values: config.Values, FeatureFlags: &flagSnapshot})
	if err != nil {
		return snapshot, err
	}
	snapshot.ProjectConfig = &projectConfig
	scopes, _ := json.Marshal(map[string]string{appID: scope})
	variables, secrets, err := projectCloneValuesDB(ctx, tx, ProjectEnvironmentClone{
		AccountID: op.AccountID, ProjectID: op.ProjectID, sourceValueScopesJSON: scopes,
	})
	if err != nil {
		return snapshot, err
	}
	snapshot.Values = &projectCloneWorkloadValues{Variables: variables, Secrets: secrets}
	bindings, err := captureCloneBindingsDB(ctx, tx, op.AccountID, appID, scope, *snapshot.Values)
	if err != nil {
		return snapshot, err
	}
	snapshot.Bindings = &bindings
	legacy, err := q.ReadProjectEnvironmentCloneLegacySettings(ctx, tx, sqlc.ReadProjectEnvironmentCloneLegacySettingsParams{AppID: mustPgUUID(appID), Environment: op.SourceEnvironment})
	if err != nil {
		return snapshot, mapErr(err)
	}
	var identity struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(legacy.App, &identity); err != nil {
		return snapshot, ErrConflict
	}
	snapshot.WorkloadSlug = identity.Slug
	pinned, err := q.ReadProjectEnvironmentCloneDeployedSettings(ctx, tx, mustPgUUID(snapshot.Artifact.ID))
	if err == nil {
		if err := json.Unmarshal(pinned.Settings, &snapshot.Settings); err != nil {
			return snapshot, ErrConflict
		}
		hash, err := WorkloadSettingsHash(snapshot.Settings)
		if err != nil || hash != pinned.ConfigHash {
			return snapshot, ErrConflict
		}
	} else if errors.Is(err, pgx.ErrNoRows) {
		if err := json.Unmarshal(legacy.App, &snapshot.Settings); err != nil {
			return snapshot, ErrConflict
		}
		var route struct {
			Only   bool            `json:"only_allow_declared_routes"`
			Routes []DeclaredRoute `json:"declared_routes"`
		}
		if err := json.Unmarshal(legacy.Route, &route); err != nil {
			return snapshot, ErrConflict
		}
		if string(legacy.Route) != "{}" {
			snapshot.Settings.OnlyAllowDeclaredRoutes, snapshot.Settings.DeclaredRoutes = route.Only, route.Routes
		}
		snapshot.Settings.DeclaredRoutes = cloneDeclaredRoutes(snapshot.Settings.DeclaredRoutes)
	} else {
		return snapshot, mapErr(err)
	}
	layers, err := q.ReadProjectEnvironmentCloneSidecarLayers(ctx, tx, mustPgUUID(snapshot.Artifact.ID))
	if err != nil {
		return snapshot, mapErr(err)
	}
	for _, raw := range layers {
		var layer DeploymentSidecarLayer
		if err := json.Unmarshal(raw, &layer); err != nil {
			return snapshot, ErrConflict
		}
		snapshot.Layers = append(snapshot.Layers, layer)
	}
	signals, err := q.ReadProjectEnvironmentCloneSidecarSignals(ctx, tx, mustPgUUID(snapshot.Artifact.ID))
	if err != nil {
		return snapshot, mapErr(err)
	}
	for _, signal := range signals {
		snapshot.SidecarSignals[signal.SidecarName] = signal.Signal
	}
	policies, err := captureCloneScopedPoliciesDB(ctx, tx, op, appID, snapshot.Settings)
	if err != nil {
		return snapshot, err
	}
	snapshot.Policies = &policies
	return snapshot, nil
}

func (s *PgStore) CreateDeploymentForEnvironmentClone(ctx context.Context, accountID, projectID, operationID string, revision int64, appID, targetSettingsHash string) (Deployment, error) {
	input := projectEnvironmentCloneDeploymentInput{AccountID: accountID, ProjectID: projectID, OperationID: operationID, Revision: revision, AppID: appID, TargetSettingsHash: targetSettingsHash}
	created, _, err := s.createDeployment(ctx, Deployment{AppID: appID}, nil, nil, &input)
	return created, err
}

func prepareCloneDeploymentTx(ctx context.Context, tx pgx.Tx, input projectEnvironmentCloneDeploymentInput, op ProjectEnvironmentCloneOperation) (Deployment, projectCloneWorkloadRecord, error) {
	if op.Status != CloneOperationCopying || op.Revision != input.Revision {
		return Deployment{}, projectCloneWorkloadRecord{}, ErrConflict
	}
	if _, err := new(sqlc.Queries).ReadProjectEnvironmentCloneOwnedApp(ctx, tx, sqlc.ReadProjectEnvironmentCloneOwnedAppParams{
		AppID: mustPgUUID(input.AppID), AccountID: mustPgUUID(input.AccountID), ProjectID: mustPgUUID(input.ProjectID),
	}); err != nil {
		return Deployment{}, projectCloneWorkloadRecord{}, mapErr(err)
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, input.AccountID, input.ProjectID, input.OperationID)
	if err != nil {
		return Deployment{}, projectCloneWorkloadRecord{}, err
	}
	for _, record := range records {
		if record.AppID != input.AppID {
			continue
		}
		if record.TargetDeploymentID != "" {
			d, err := deploymentByIDDB(ctx, tx, record.TargetDeploymentID)
			if err != nil {
				return Deployment{}, record, err
			}
			if record.TargetSettingsHash != input.TargetSettingsHash || d.AppID != input.AppID || d.Scope != op.TargetEnvironment ||
				(d.Status != DeployPending && d.Status != DeployLive) {
				return Deployment{}, record, ErrConflict
			}
			raw, err := new(sqlc.Queries).ReadProjectEnvironmentCloneTargetArtifact(ctx, tx, mustPgUUID(d.ID))
			if err != nil {
				return Deployment{}, record, mapErr(err)
			}
			var artifact projectCloneArtifact
			if err := json.Unmarshal(raw, &artifact); err != nil {
				return Deployment{}, record, ErrConflict
			}
			d.SecretReloadSignal, d.SecretReloadSignalKnown = artifact.SecretReloadSignal, artifact.SecretReloadSignalKnown
			return d, record, nil
		}
		spec, err := new(sqlc.Queries).ReadProjectEnvironmentCloneTargetSettings(ctx, tx, sqlc.ReadProjectEnvironmentCloneTargetSettingsParams{
			AccountID: mustPgUUID(input.AccountID), ProjectID: mustPgUUID(input.ProjectID), AppID: mustPgUUID(input.AppID), Environment: op.TargetEnvironment})
		if err != nil {
			return Deployment{}, record, err
		}
		var settings ProjectEnvironmentWorkloadSettings
		if err := json.Unmarshal(spec.Settings, &settings); err != nil {
			return Deployment{}, record, ErrConflict
		}
		hash, err := WorkloadSettingsHash(settings)
		if err != nil || hash != spec.ConfigHash || spec.ConfigHash != input.TargetSettingsHash {
			return Deployment{}, record, ErrConflict
		}
		return record.snapshot.Artifact.deployment(op.ID, op.TargetEnvironment, settings), record, nil
	}
	return Deployment{}, projectCloneWorkloadRecord{}, ErrConflict
}

func attachCloneDeploymentTx(ctx context.Context, tx pgx.Tx, input projectEnvironmentCloneDeploymentInput, record projectCloneWorkloadRecord, deployment Deployment) error {
	q := new(sqlc.Queries)
	count, err := q.AttachProjectEnvironmentCloneDeployment(ctx, tx, sqlc.AttachProjectEnvironmentCloneDeploymentParams{OperationID: mustPgUUID(input.OperationID), AppID: mustPgUUID(input.AppID), DeploymentID: mustPgUUID(deployment.ID), SettingsHash: input.TargetSettingsHash})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	a := record.snapshot.Artifact
	if err := q.SetProjectEnvironmentCloneDeploymentArtifact(ctx, tx, sqlc.SetProjectEnvironmentCloneDeploymentArtifactParams{DeploymentID: mustPgUUID(deployment.ID), RootfsPath: a.RootfsPath, RootfsKey: a.RootfsKey, RootfsBytes: a.RootfsBytes, SignalKnown: a.SecretReloadSignalKnown, Signal: a.SecretReloadSignal}); err != nil {
		return mapErr(err)
	}
	for _, l := range record.snapshot.Layers {
		if err := q.InsertProjectEnvironmentCloneSidecarLayer(ctx, tx, sqlc.InsertProjectEnvironmentCloneSidecarLayerParams{DeploymentID: mustPgUUID(deployment.ID), SidecarName: l.SidecarName, StorageKey: l.StorageKey, Bytes: l.Bytes, ContentDigest: l.ContentDigest}); err != nil {
			return mapErr(err)
		}
	}
	for name, signal := range record.snapshot.SidecarSignals {
		if err := q.InsertProjectEnvironmentCloneSidecarSignal(ctx, tx, sqlc.InsertProjectEnvironmentCloneSidecarSignalParams{DeploymentID: mustPgUUID(deployment.ID), SidecarName: name, Signal: signal}); err != nil {
			return mapErr(err)
		}
	}
	return nil
}
