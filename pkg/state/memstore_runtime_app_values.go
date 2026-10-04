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
	sort.Slice(result.Secrets, func(i, j int) bool { return result.Secrets[i].Key < result.Secrets[j].Key })
	settings, err := WorkloadSettingsFromApp(m.apps[appID])
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
