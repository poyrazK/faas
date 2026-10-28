package state

import (
	"context"
	"sort"
	"strings"
)

var _ RuntimeAppValuesStore = (*MemStore)(nil)

func (m *MemStore) RuntimeAppValuesForDeployment(_ context.Context, accountID, appID, deploymentID string) (RuntimeAppValuesSnapshot, error) {
	if err := validateRuntimeAppEnvIDs(accountID, appID, deploymentID); err != nil {
		return RuntimeAppValuesSnapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runtimeAppValuesLocked(accountID, appID, deploymentID)
}

func (m *MemStore) runtimeAppValuesLocked(accountID, appID, deploymentID string) (RuntimeAppValuesSnapshot, error) {
	owner, err := m.runtimeAppValueOwnerLocked(accountID, appID, deploymentID)
	if err != nil {
		return RuntimeAppValuesSnapshot{}, err
	}
	owner.Values = m.runtimeAppEnvRowsLocked(owner)
	if owner.Inherited, err = m.inheritedProjectEnvLocked(owner); err != nil {
		return RuntimeAppValuesSnapshot{}, err
	}
	d := m.deployments[deploymentID]
	result := RuntimeAppValuesSnapshot{RuntimeAppEnvSnapshot: owner, Secrets: []AppSecret{}, SecretGrants: RuntimeAppSecretGrants{
		OverrideEnvSecrets: append([]byte(nil), d.OverrideEnvSecrets...), Sidecars: append([]byte(nil), d.Sidecars...),
		ReloadSignal: d.SecretReloadSignal, SidecarReloadSignals: map[string]string{},
	}}
	for key, signal := range m.sidecarSecretReloadSignals {
		if name, ok := strings.CutPrefix(key, deploymentID+"\x00"); ok {
			result.SecretGrants.SidecarReloadSignals[name] = signal
		}
	}
	for _, row := range m.secrets {
		if row.AccountID != accountID || row.AppID != appID || row.Scope != owner.Scope {
			continue
		}
		result.Secrets = append(result.Secrets, AppSecret{AccountID: accountID, AppID: appID, Scope: owner.Scope, Key: row.Key,
			Ciphertext: append([]byte(nil), row.Ciphertext...), SecretClass: row.SecretClass, Kid: row.Kid, ValueHash: row.ValueHash,
			SecretVersion: row.SecretVersion, DeliveryVersion: row.DeliveryVersion, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			ManagedPostgresBindingID: row.ManagedPostgresBindingID, ManagedPostgresAccess: row.ManagedPostgresAccess, ManagedCredentialRef: row.ManagedCredentialRef,
			ManagedCredentialGeneration: row.ManagedCredentialGeneration, ManagedObjectStorageCredentialID: row.ManagedObjectStorageCredentialID,
		})
	}
	if rows, pinned := m.releaseSecretPins[deploymentID]; pinned {
		if err := applyReleaseSecretSnapshot(&result, rows); err != nil {
			return RuntimeAppValuesSnapshot{}, err
		}
	}
	sort.Slice(result.Secrets, func(i, j int) bool { return result.Secrets[i].Key < result.Secrets[j].Key })
	result.InheritedSecrets = m.inheritedProjectSecretsLocked(result)
	runtimeApp, err := AppForDeploymentRuntime(m.apps[appID], d)
	if err != nil {
		return RuntimeAppValuesSnapshot{}, err
	}
	settings, err := WorkloadSettingsFromApp(runtimeApp)
	if err != nil {
		return RuntimeAppValuesSnapshot{}, err
	}
	specID := m.projectEnvironmentWorkloadDeploymentSpecs[deploymentID]
	if specID != "" {
		spec, ok := m.projectEnvironmentWorkloadSpecs[specID]
		hash, hashErr := WorkloadSettingsHash(spec.Settings)
		if !ok || hashErr != nil || hash != spec.Hash {
			return RuntimeAppValuesSnapshot{}, ErrConflict
		}
		settings = spec.Settings
	}
	result.SidecarLayers = []DeploymentSidecarLayer{}
	for _, layer := range m.deploymentSidecarLayers {
		if layer.DeploymentID == deploymentID {
			result.SidecarLayers = append(result.SidecarLayers, layer)
		}
	}
	sort.Slice(result.SidecarLayers, func(i, j int) bool { return result.SidecarLayers[i].SidecarName < result.SidecarLayers[j].SidecarName })
	result.Configuration, err = runtimeAppConfiguration(settings, specID, projectCloneArtifactFromDeployment(d), result.SidecarLayers)
	if err != nil {
		return RuntimeAppValuesSnapshot{}, err
	}
	return result, nil
}

// inheritsProjectEnvironment mirrors the runtime queries'
// inherits_project_environment column: a PR preview runs unreviewed code and
// never inherits the project environment its scope maps to (the default
// scope maps to production).
func inheritsProjectEnvironment(app App) bool {
	return app.ProjectID != "" && app.PreviewOfSlug == ""
}

// previewParentLocked mirrors the snapshot queries' preview_parent_id: the
// live production app whose preview values a PR preview inherits
// (ADR-20261028100000).
func (m *MemStore) previewParentLocked(app App) (App, bool) {
	if app.PreviewOfSlug == "" || app.PreviewPrNumber <= 0 {
		return App{}, false
	}
	for _, parent := range m.apps {
		if parent.AccountID == app.AccountID && parent.Slug == app.PreviewOfSlug && parent.PreviewOfSlug == "" && parent.Status != AppDeleted {
			return parent, true
		}
	}
	return App{}, false
}

// inheritedProjectEnvLocked reads the latest configuration of the project
// environment the owner's scope maps to (ADR-1136).
func (m *MemStore) inheritedProjectEnvLocked(owner RuntimeAppEnvSnapshot) ([]AppEnv, error) {
	app := m.apps[owner.AppID]
	if parent, ok := m.previewParentLocked(app); ok {
		var values []runtimePreviewValue
		for _, row := range m.runtimeAppEnvRowsLocked(RuntimeAppEnvSnapshot{AccountID: owner.AccountID, AppID: parent.ID, Scope: PreviewValuesScope}) {
			values = append(values, runtimePreviewValue{Key: row.Key, Value: row.Value, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
		}
		return previewInheritedEnv(owner, values), nil
	}
	if !inheritsProjectEnvironment(app) {
		return []AppEnv{}, nil
	}
	versions := m.projectEnvironmentConfigs[projectEnvironmentConfigKey(app.ProjectID, workloadEnvironmentSlug(owner.Scope))]
	if len(versions) == 0 {
		return []AppEnv{}, nil
	}
	latest := versions[len(versions)-1]
	return inheritedProjectEnv(owner.AccountID, owner.AppID, owner.Scope, owner.Values, latest.Values, latest.CreatedAt.UTC())
}

// inheritedProjectSecretsLocked mirrors ReadRuntimeAppValuesForDeployment's
// project_secrets column (ADR-1139).
func (m *MemStore) inheritedProjectSecretsLocked(snapshot RuntimeAppValuesSnapshot) []ProjectEnvironmentSecret {
	app := m.apps[snapshot.AppID]
	if parent, ok := m.previewParentLocked(app); ok {
		var secrets []AppSecret
		for _, row := range m.secrets {
			if row.AccountID == snapshot.AccountID && row.AppID == parent.ID && row.Scope == PreviewValuesScope &&
				row.SecretClass != SecretClassEphemeral && row.ManagedPostgresBindingID == "" &&
				row.ManagedObjectStorageCredentialID == "" && row.ManagedCredentialRef == "" {
				secrets = append(secrets, row)
			}
		}
		return previewInheritedSecrets(snapshot, secrets)
	}
	if !inheritsProjectEnvironment(app) {
		return []ProjectEnvironmentSecret{}
	}
	var rows []ProjectEnvironmentSecret
	for _, secret := range m.projectEnvironmentSecretsLocked(app.ProjectID, workloadEnvironmentSlug(snapshot.Scope)) {
		if secret.AccountID == snapshot.AccountID {
			rows = append(rows, secret)
		}
	}
	return inheritedProjectSecrets(snapshot, rows)
}

// PostgreSQL cascades observation rows when their scoped secret is deleted.
// Every MemStore deletion must preserve that row-lifetime boundary as well.
func (m *MemStore) deleteRuntimeAppSecretLocked(key secretKey) {
	delete(m.secrets, key)
	for observation := range m.secretRuntimeReloadObservations {
		if observation.AppID == key.AppID && observation.Scope == key.Scope && observation.Key == key.Key {
			delete(m.secretRuntimeReloadObservations, observation)
		}
	}
}
