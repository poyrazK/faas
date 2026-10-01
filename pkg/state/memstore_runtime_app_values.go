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
			ManagedPostgresBindingID: row.ManagedPostgresBindingID, ManagedCredentialRef: row.ManagedCredentialRef,
			ManagedCredentialGeneration: row.ManagedCredentialGeneration, ManagedObjectStorageCredentialID: row.ManagedObjectStorageCredentialID,
		})
	}
	sort.Slice(result.Secrets, func(i, j int) bool { return result.Secrets[i].Key < result.Secrets[j].Key })
	return result, nil
}
