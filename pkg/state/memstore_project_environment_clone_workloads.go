package state

import (
	"context"
	"sort"
	"strings"
	"time"
)

func (m *MemStore) CaptureProjectEnvironmentCloneWorkloads(_ context.Context, accountID, projectID, operationID string, revision int64) ([]ProjectEnvironmentCloneWorkload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.projectEnvironmentCloneOperations[operationID]
	if !ok || op.AccountID != accountID || op.ProjectID != projectID {
		return nil, ErrNotFound
	}
	if op.Status != CloneOperationCapturing || op.Revision != revision || !m.cloneOperationLeaseLiveLocked(operationID) {
		return nil, ErrConflict
	}
	if records := m.projectEnvironmentCloneWorkloads[operationID]; len(records) > 0 {
		return cloneWorkloadViews(records), nil
	}
	if m.activeProjectReleaseSets[releaseKey(projectID, op.SourceEnvironment)] != op.SourceReleaseSetID {
		return nil, ErrConflict
	}
	apps := m.projectCloneAppsLocked(projectID)
	if len(apps) == 0 {
		return nil, ErrConflict
	}
	if _, err := m.projectEnvironmentBySlugLocked(projectID, op.SourceEnvironment); err != nil {
		return nil, err
	}
	scopes, err := m.projectCloneValueScopesLocked(apps, ProjectEnvironmentClone{ProjectID: projectID, SourceSlug: op.SourceEnvironment})
	if err != nil {
		return nil, err
	}
	records := map[string]projectCloneWorkloadRecord{}
	for appID := range apps {
		app := m.apps[appID]
		var source Deployment
		if op.SourceReleaseSetID != "" {
			source = m.deployments[releaseMemberForApp(m.projectReleaseSets[op.SourceReleaseSetID], appID)]
		} else {
			for _, d := range m.deployments {
				if d.AppID == appID && d.Status == DeployLive && normalizedDeploymentScope(d.Scope) == scopes[appID] && d.TrafficPercent > 0 &&
					(source.ID == "" || deploymentPreferredForWake(d, source)) {
					source = d
				}
			}
		}
		if source.ID == "" || source.AppID != appID || source.Status != DeployLive || normalizedDeploymentScope(source.Scope) != scopes[appID] {
			return nil, ErrConflict
		}
		settings, err := WorkloadSettingsFromApp(app)
		if err != nil {
			return nil, err
		}
		if route, ok := m.projectEnvironmentRoutePolicies[projectEnvironmentRoutePolicyKey(appID, op.SourceEnvironment)]; ok {
			settings.OnlyAllowDeclaredRoutes, settings.DeclaredRoutes = route.OnlyAllowDeclaredRoutes, cloneDeclaredRoutes(route.DeclaredRoutes)
		}
		if specID := m.projectEnvironmentWorkloadDeploymentSpecs[source.ID]; specID != "" {
			spec, found := m.projectEnvironmentWorkloadSpecs[specID]
			if !found || spec.AppID != appID || spec.EnvironmentSlug != workloadEnvironmentSlug(source.Scope) {
				return nil, ErrConflict
			}
			hash, err := WorkloadSettingsHash(spec.Settings)
			if err != nil || hash != spec.Hash {
				return nil, ErrConflict
			}
			settings = spec.Settings
		}
		snapshot := projectCloneWorkloadSnapshot{WorkloadSlug: app.Slug, Artifact: projectCloneArtifactFromDeployment(source), Settings: settings, SidecarSignals: map[string]string{}}
		for _, layer := range m.deploymentSidecarLayers {
			if layer.DeploymentID == source.ID {
				snapshot.Layers = append(snapshot.Layers, layer)
			}
		}
		for key, signal := range m.sidecarSecretReloadSignals {
			if name, ok := strings.CutPrefix(key, source.ID+"\x00"); ok {
				snapshot.SidecarSignals[name] = signal
			}
		}
		raw, hash, err := encodeCloneWorkloadSnapshot(snapshot)
		if err != nil {
			return nil, err
		}
		record, err := decodeCloneWorkloadRecord(operationID, appID, source.ID, hash, "", "", raw)
		if err != nil {
			return nil, err
		}
		records[appID] = record
	}
	m.projectEnvironmentCloneWorkloads[operationID] = records
	return cloneWorkloadViews(records), nil
}

func cloneWorkloadViews(records map[string]projectCloneWorkloadRecord) []ProjectEnvironmentCloneWorkload {
	views := make([]ProjectEnvironmentCloneWorkload, 0, len(records))
	for _, record := range records {
		views = append(views, record.ProjectEnvironmentCloneWorkload)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].AppID < views[j].AppID })
	return views
}

func (m *MemStore) ProjectEnvironmentCloneWorkloads(_ context.Context, accountID, projectID, operationID string) ([]ProjectEnvironmentCloneWorkload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.projectEnvironmentCloneOperations[operationID]
	if !ok || op.AccountID != accountID || op.ProjectID != projectID {
		return nil, ErrNotFound
	}
	return cloneWorkloadViews(m.projectEnvironmentCloneWorkloads[operationID]), nil
}

func (m *MemStore) CreateDeploymentForEnvironmentClone(_ context.Context, accountID, projectID, operationID string, revision int64, appID, targetSettingsHash string) (Deployment, error) {
	input := projectEnvironmentCloneDeploymentInput{AccountID: accountID, ProjectID: projectID, OperationID: operationID, Revision: revision, AppID: appID, TargetSettingsHash: targetSettingsHash}
	created, _, err := m.createDeployment(Deployment{AppID: appID}, nil, nil, &input)
	return created, err
}

func (m *MemStore) prepareCloneDeploymentLocked(input projectEnvironmentCloneDeploymentInput) (Deployment, projectCloneWorkloadRecord, error) {
	op, ok := m.projectEnvironmentCloneOperations[input.OperationID]
	if !ok || op.AccountID != input.AccountID || op.ProjectID != input.ProjectID {
		return Deployment{}, projectCloneWorkloadRecord{}, ErrNotFound
	}
	if op.Status != CloneOperationCopying || op.Revision != input.Revision || !m.cloneOperationLeaseLiveLocked(input.OperationID) {
		return Deployment{}, projectCloneWorkloadRecord{}, ErrConflict
	}
	record, ok := m.projectEnvironmentCloneWorkloads[op.ID][input.AppID]
	app := m.apps[input.AppID]
	if !ok || app.ProjectID != op.ProjectID || app.AccountID != op.AccountID || app.Status == AppDeleted || app.PreviewOfSlug != "" {
		return Deployment{}, record, ErrConflict
	}
	if record.TargetDeploymentID != "" {
		d := m.deployments[record.TargetDeploymentID]
		if record.TargetSettingsHash != input.TargetSettingsHash || d.AppID != app.ID || d.Scope != op.TargetEnvironment ||
			(d.Status != DeployPending && d.Status != DeployLive) {
			return Deployment{}, record, ErrConflict
		}
		return d, record, nil
	}
	spec, settings, err := m.environmentWorkloadSettingsLocked(app, op.TargetEnvironment)
	if err != nil {
		return Deployment{}, record, err
	}
	if spec.ID == "" || spec.Hash != input.TargetSettingsHash {
		return Deployment{}, record, ErrConflict
	}
	captured, err := copyCloneWorkloadRecord(record)
	if err != nil {
		return Deployment{}, record, err
	}
	return captured.snapshot.Artifact.deployment(op.ID, op.TargetEnvironment, settings), record, nil
}

func (m *MemStore) attachCloneDeploymentLocked(input projectEnvironmentCloneDeploymentInput, record projectCloneWorkloadRecord, deployment Deployment) {
	record.TargetDeploymentID, record.TargetSettingsHash = deployment.ID, input.TargetSettingsHash
	m.projectEnvironmentCloneWorkloads[input.OperationID][input.AppID] = record
	for _, layer := range record.snapshot.Layers {
		layer.DeploymentID = deployment.ID
		layer.CreatedAt, layer.UpdatedAt = time.Now().UTC(), time.Now().UTC()
		m.deploymentSidecarLayers[deployment.ID+"\x00"+layer.SidecarName] = layer
	}
	for name, signal := range record.snapshot.SidecarSignals {
		m.sidecarSecretReloadSignals[deployment.ID+"\x00"+name] = signal
	}
}
