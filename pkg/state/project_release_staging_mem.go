package state

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func (m *MemStore) releaseStageSnapshotLocked(account, project, scope, source string) (releaseStageSnapshot, error) {
	p, ok := m.projects[project]
	if !ok || p.AccountID != account {
		return releaseStageSnapshot{}, ErrNotFound
	}
	found := false
	for _, env := range m.projectEnvironments {
		if env.ProjectID == project && env.Slug == scope {
			found = true
		}
	}
	if !found {
		return releaseStageSnapshot{}, ErrNotFound
	}
	out := releaseStageSnapshot{Project: p, Deployments: map[string]Deployment{}, Policies: map[string]api.BindingReleasePolicy{}, Managed: map[string]bool{}, Active: m.activeProjectReleaseSets[releaseKey(project, scope)]}
	for _, app := range m.apps {
		if app.ProjectID == project && app.AccountID == account && app.Status != AppDeleted && app.PreviewOfSlug == "" {
			out.Apps = append(out.Apps, app)
			out.Policies[app.ID] = m.bindingReleasePolicyLocked(app.ID, scope)
			out.Managed[app.ID] = m.environmentGitOpsManagedForScopeLocked(app.ID, scope)
		}
	}
	for _, dep := range m.deployments {
		out.Deployments[stageCanonicalID(dep.ID)] = dep
	}
	out.Mirrors = map[string]bool{}
	for _, rule := range m.mirrorRules {
		out.Mirrors[stageCanonicalID(rule.MirrorDeploymentID)] = true
	}
	if source != "" {
		for id, release := range m.projectReleaseSets {
			if stageCanonicalID(id) == source {
				r := release
				out.Source = &r
			}
		}
	}
	return out, nil
}

func (m *MemStore) StageProjectRelease(_ context.Context, account, project string, request api.StageProjectReleaseRequest) (api.ProjectReleaseStage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if request.TTLSeconds < 1 || request.TTLSeconds > api.ProjectReleaseStageMaxTTLSeconds {
		return api.ProjectReleaseStage{}, ErrInvalidArgument
	}
	snapshot, err := m.releaseStageSnapshotLocked(account, project, request.Manifest.Environment, request.Manifest.RollbackReleaseID)
	if err != nil {
		return api.ProjectReleaseStage{}, err
	}
	targets, err := validateReleaseStage(snapshot, request.Manifest)
	if err != nil {
		return api.ProjectReleaseStage{}, err
	}
	for _, app := range snapshot.Apps {
		dep := snapshot.Deployments[request.Manifest.Deployments[app.Slug]]
		if err := m.requireLayerArtifactsRetainedLocked(m.deploymentLayerKeysLocked(dep)); err != nil {
			return api.ProjectReleaseStage{}, err
		}
	}
	encoded, _ := json.Marshal(request.Manifest)
	for _, stage := range m.projectReleaseStages {
		if stage.ProjectID == project && stage.Manifest.Environment == request.Manifest.Environment && !stage.Cancelled && time.Now().Before(stage.ExpiresAt) {
			old, _ := json.Marshal(stage.Manifest)
			if string(old) != string(encoded) {
				return api.ProjectReleaseStage{}, ErrConflict
			}
			return m.releaseStageViewLocked(stage, snapshot), nil
		}
	}
	now := time.Now().UTC()
	stage := projectReleaseStage{ID: uuid.NewString(), AccountID: account, ProjectID: project, Manifest: request.Manifest, Targets: targets, CreatedAt: now, ExpiresAt: now.Add(time.Duration(request.TTLSeconds) * time.Second)}
	if m.projectReleaseStages == nil {
		m.projectReleaseStages = map[string]projectReleaseStage{}
	}
	m.projectReleaseStages[stage.ID] = cloneReleaseStage(stage)
	return m.releaseStageViewLocked(stage, snapshot), nil
}
func (m *MemStore) releaseStageViewLocked(stage projectReleaseStage, snapshot releaseStageSnapshot) api.ProjectReleaseStage {
	var instances []Instance
	for _, ins := range m.instances {
		instances = append(instances, ins)
	}
	return releaseStageView(stage, snapshot, instances, m.serviceRecovery)
}
func (m *MemStore) ProjectReleaseStage(_ context.Context, account, project, scope, id string) (api.ProjectReleaseStage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readReleaseStageLocked(account, project, scope, id, false)
}
func (m *MemStore) CancelProjectReleaseStage(_ context.Context, account, project, scope, id string) (api.ProjectReleaseStage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readReleaseStageLocked(account, project, scope, id, true)
}
func (m *MemStore) readReleaseStageLocked(account, project, scope, id string, cancel bool) (api.ProjectReleaseStage, error) {
	stage, ok := m.projectReleaseStages[id]
	if !ok || stage.AccountID != account || stage.ProjectID != project || stage.Manifest.Environment != scope {
		return api.ProjectReleaseStage{}, ErrNotFound
	}
	if cancel {
		stage.Cancelled = true
		m.projectReleaseStages[id] = stage
	}
	snapshot, err := m.releaseStageSnapshotLocked(account, project, scope, stage.Manifest.RollbackReleaseID)
	if err != nil {
		return api.ProjectReleaseStage{}, err
	}
	return m.releaseStageViewLocked(stage, snapshot), nil
}
func (m *MemStore) ServiceReleaseReadinessTargets(_ context.Context, appID string) (map[string]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	app, ok := m.apps[appID]
	if !ok {
		return out, ErrNotFound
	}
	desired := 1
	if app.Manifest.ServiceReplicas != nil && app.Manifest.ServiceReplicas.Desired > desired {
		desired = app.Manifest.ServiceReplicas.Desired
	}
	for _, stage := range m.projectReleaseStages {
		if stage.ProjectID != app.ProjectID || stage.Cancelled || !time.Now().Before(stage.ExpiresAt) {
			continue
		}
		snapshot, err := m.releaseStageSnapshotLocked(app.AccountID, app.ProjectID, stage.Manifest.Environment, stage.Manifest.RollbackReleaseID)
		if err != nil {
			continue
		}
		targets, err := validateReleaseStage(snapshot, stage.Manifest)
		if err != nil {
			continue
		}
		for id, n := range targets {
			if m.deployments[id].AppID == appID && n > out[id] {
				out[id] = n
			}
		}
	}
	for _, release := range m.projectReleaseSets {
		if release.Active {
			for _, member := range release.Members {
				if member.AppID == appID && !m.deployments[member.DeploymentID].EnvironmentWorkloadManaged() {
					out[member.DeploymentID] = desired
				}
			}
		}
	}
	return out, nil
}
