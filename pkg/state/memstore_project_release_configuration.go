package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) releaseConfigCaptureLocked(accountID, projectID, baselineID string) (projectReleaseConfigCapture, error) {
	project, ok := m.projects[projectID]
	baseline, found := m.projectReleaseSets[baselineID]
	if !ok || project.AccountID != accountID || !found || baseline.AccountID != accountID || baseline.ProjectID != projectID || baseline.EnvironmentSlug != "production" || m.activeProjectReleaseSets[releaseKey(projectID, "production")] != baselineID {
		return projectReleaseConfigCapture{}, ErrConflict
	}
	prior := m.projectEnvironmentConfigLatestLocked(projectID, "production")
	capture := projectReleaseConfigCapture{ProjectID: projectID, BaselineID: baselineID, Previous: projectReleaseConfigVersion{Version: prior.Version, Hash: prior.ConfigHash, Values: prior.Values}}
	for _, member := range baseline.Members {
		app, ok := m.apps[member.AppID]
		d := m.deployments[member.DeploymentID]
		if !ok || app.AccountID != accountID || app.ProjectID != projectID || app.Status == AppDeleted || d.Scope != "production" {
			return capture, ErrConflict
		}
		name := app.WorkloadName
		if name == "" {
			name = app.Slug
		}
		w := projectReleaseConfigWorkload{AppID: app.ID, Name: name, DeploymentID: d.ID, Legacy: []projectReleaseVariable{}, SecretKeys: []string{}}
		for _, value := range m.envs {
			if value.AppID == app.ID && value.AccountID == accountID {
				if value.Scope == "production" {
					w.Legacy = append(w.Legacy, releaseVariables([]AppEnv{value})[0])
				} else {
					w.OtherVariables++
				}
			}
		}
		for _, secret := range m.secrets {
			if secret.AppID == app.ID && secret.Scope == "production" {
				w.SecretKeys = append(w.SecretKeys, secret.Key)
				w.LiveSecrets = append(w.LiveSecrets, releaseSecretRow(secret))
			}
		}
		for key := range m.appEnvironmentSecretRefs {
			if key.AppID == app.ID {
				w.OtherVariables++
				if m.projectEnvironments[key.EnvironmentID].Slug == "production" {
					w.SecretKeys = append(w.SecretKeys, key.Key)
				}
			}
		}
		for key, intent := range m.appEnvironmentWorkloadIntents {
			if key.AppID == app.ID && intent.AccountID == accountID {
				w.OtherVariables += len(intent.ServiceBindings)
				if m.projectEnvironments[key.EnvironmentID].Slug == "production" {
					for _, binding := range intent.ServiceBindings {
						w.SecretKeys = append(w.SecretKeys, binding.EnvKey)
					}
				}
			}
		}
		w.SecretIneligible = len(m.environmentSecretRefsLocked(app.ID, "production")) > 0 || len(m.environmentSecretSuppressionsLocked(app.ID, "production")) > 0 || nonemptySecretJSON(d.OverrideEnvSecrets) || nonemptySecretJSON(d.Sidecars) || d.SecretReloadSignal != ""
		if rows, pinned := m.releaseSecretPins[d.ID]; pinned {
			copy := cloneReleaseSecrets(rows)
			w.SecretSnapshot = &copy
		}
		if rows, pinned := m.releaseEnvironmentPins[d.ID]; pinned {
			snapshot := releaseVariables(rows)
			w.Snapshot = &snapshot
		}
		sort.Slice(w.Legacy, func(i, j int) bool { return w.Legacy[i].Key < w.Legacy[j].Key })
		// References and materialized secrets can name the same private variable.
		keys := map[string]bool{}
		for _, key := range w.SecretKeys {
			keys[key] = true
		}
		w.SecretKeys = []string{}
		for key := range keys {
			w.SecretKeys = append(w.SecretKeys, key)
		}
		capture.Workloads = append(capture.Workloads, w)
	}
	sort.Slice(capture.Workloads, func(i, j int) bool { return capture.Workloads[i].AppID < capture.Workloads[j].AppID })
	return capture, nil
}
func (m *MemStore) PreviewProjectReleaseConfiguration(_ context.Context, accountID, projectID, baselineID string, request json.RawMessage) (*api.ProjectReleaseConfiguration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	capture, err := m.releaseConfigCaptureLocked(accountID, projectID, baselineID)
	if err != nil {
		return nil, err
	}
	m.loadReleaseSecretVersionsLocked(&capture, accountID, request)
	return capture.prepare(accountID, request, api.MustLimitsFor(m.accounts[accountID].Plan))
}
func (m *MemStore) StageProjectReleaseConfiguration(_ context.Context, accountID, attempt, projectID string, safe *api.ProjectSafeRelease, request json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if safe.Configuration == nil {
		return nil
	}
	key := accountID + "\x00" + attempt
	entry, exists := m.projectSubmissions[key]
	if !exists || entry.Status != "processing" {
		return ErrConflict
	}
	capture, err := m.releaseConfigCaptureLocked(accountID, projectID, safe.PreviousReleaseID)
	if err != nil {
		return err
	}
	m.loadReleaseSecretVersionsLocked(&capture, accountID, request)
	descriptor, err := capture.prepare(accountID, request, api.MustLimitsFor(m.accounts[accountID].Plan))
	if err != nil {
		return err
	}
	if old, found := m.releaseConfigurationIntents[key]; found {
		capture.reuseSecretEnvironmentProof(old, descriptor)
	}
	if !matchingReleaseConfiguration(descriptor, safe.Configuration) {
		return ErrConflict
	}
	if old, found := m.releaseConfigurationIntents[key]; found {
		if matchingReleaseConfiguration(old.Descriptor, descriptor) {
			return nil
		}
		return ErrConflict
	}
	if err := m.secretRekeyDocumentAllowedLocked(SecretRekeyDocument{Kind: "capture", Payload: mustMarshalSecretDocument(capture)}); err != nil {
		return err
	}
	data, err := json.Marshal(capture)
	if err != nil {
		return err
	}
	if m.releaseConfigurationIntents == nil {
		m.releaseConfigurationIntents = map[string]projectReleaseConfigCapture{}
	}
	var detached projectReleaseConfigCapture
	if err := json.Unmarshal(data, &detached); err != nil {
		return err
	}
	if m.releaseEnvironmentPins == nil {
		m.releaseEnvironmentPins = map[string][]AppEnv{}
	}
	if detached.Descriptor.Secrets != nil && m.releaseSecretPins == nil {
		m.releaseSecretPins = map[string][]releaseSecretVersion{}
	}
	for _, w := range detached.Workloads {
		if detached.Descriptor.Secrets != nil {
			if _, exists := m.releaseSecretPins[w.DeploymentID]; !exists {
				if w.SecretSnapshot != nil {
					m.releaseSecretPins[w.DeploymentID] = cloneReleaseSecrets(*w.SecretSnapshot)
				} else {
					rows := []releaseSecretVersion{}
					for _, row := range w.LiveSecrets {
						rows = append(rows, releaseSecretFromRow(w.AppID, row))
					}
					m.releaseSecretPins[w.DeploymentID] = rows
				}
			}
		}
		if _, found := m.releaseEnvironmentPins[w.DeploymentID]; !found {
			m.releaseEnvironmentPins[w.DeploymentID] = releaseVariableRows(w.Legacy)
		}
	}
	m.releaseConfigurationIntents[key] = detached
	return nil
}
func (m *MemStore) candidateReleaseConfigurationLocked(d Deployment) ([]AppEnv, error) {
	app := m.apps[d.AppID]
	capture, found := m.releaseConfigurationIntents[app.AccountID+"\x00"+d.releaseConfigAttempt]
	if !found {
		return nil, ErrConflict
	}
	current, err := m.releaseConfigCaptureLocked(app.AccountID, app.ProjectID, capture.BaselineID)
	if err != nil {
		return nil, err
	}
	m.loadReleaseSecretVersionsLocked(&current, app.AccountID, capture.Request)
	descriptor, err := current.prepare(app.AccountID, capture.Request, api.MustLimitsFor(m.accounts[app.AccountID].Plan))
	current.reuseSecretEnvironmentProof(capture, descriptor)
	if err != nil || !matchingReleaseConfiguration(descriptor, capture.Descriptor) {
		return nil, ErrConflict
	}
	variables, err := candidateReleaseVariables(capture, d)
	if err != nil {
		return nil, err
	}
	return releaseVariableRows(variables), nil
}
func (m *MemStore) guardReleaseConfigurationLocked(accountID, attempt, projectID string, safe *api.ProjectSafeRelease, rollback bool) (projectReleaseConfigCapture, error) {
	capture, ok := m.releaseConfigurationIntents[accountID+"\x00"+attempt]
	if !ok {
		return capture, ErrConflict
	}
	active := safe.PreviousReleaseID
	if rollback {
		active = safe.ReleaseID
	}
	current, err := m.releaseConfigCaptureLocked(accountID, projectID, active)
	if err != nil {
		return capture, err
	}
	descriptor, err := current.prepare(accountID, nil, api.MustLimitsFor(m.accounts[accountID].Plan))
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
func (m *MemStore) appendReleaseConfigurationLocked(accountID, projectID string, values json.RawMessage, hash string) int64 {
	key := projectEnvironmentConfigKey(projectID, "production")
	versions := m.projectEnvironmentConfigs[key]
	config := ProjectEnvironmentConfig{ID: newID(), AccountID: accountID, ProjectID: projectID, EnvironmentSlug: "production", Version: int64(len(versions) + 1), ConfigHash: hash, Values: append(json.RawMessage(nil), values...), CreatedAt: time.Now().UTC()}
	m.projectEnvironmentConfigs[key] = append(versions, config)
	return config.Version
}

func (m *MemStore) inheritedReleaseVariablesLocked(appID, scope string) []AppEnv {
	app := m.apps[appID]
	release := m.projectReleaseSets[m.activeProjectReleaseSets[releaseKey(app.ProjectID, normalizedDeploymentScope(scope))]]
	for _, member := range release.Members {
		if member.AppID == appID {
			if values, pinned := m.releaseEnvironmentPins[member.DeploymentID]; pinned {
				return append([]AppEnv{}, values...)
			}
		}
	}
	return nil
}
