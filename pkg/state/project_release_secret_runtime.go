package state

import (
	"context"
	"encoding/json"
	"maps"

	"github.com/onebox-faas/faas/pkg/api"
)

func nonemptySecretJSON(raw []byte) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var values any
	if json.Unmarshal(raw, &values) != nil {
		return true
	}
	switch v := values.(type) {
	case map[string]any:
		return len(v) > 0
	case []any:
		return len(v) > 0
	default:
		return true
	}
}
func applyReleaseSecretSnapshot(result *RuntimeAppValuesSnapshot, rows []releaseSecretVersion) error {
	result.ReleaseSecretsPinned = true
	result.ReleaseSecretVersionIDs = map[string]string{}
	result.Secrets = []AppSecret{}
	for _, row := range rows {
		if row.AppID != result.AppID || !api.ValidReleaseSecretVersionID(row.ID) || api.ValidateSecretKey(row.Key) != nil || row.SecretClass != api.SecretClassPersistent || len(row.Ciphertext) == 0 {
			return ErrConflict
		}
		if _, duplicate := result.ReleaseSecretVersionIDs[row.Key]; duplicate {
			return ErrConflict
		}
		result.ReleaseSecretVersionIDs[row.Key] = row.ID
		result.Secrets = append(result.Secrets, row.secret(result.AccountID, result.Scope))
	}
	return nil
}

// Deployment context makes equal numeric delivery versions in separate
// immutable secret snapshots unambiguous. The publication fence also binds
// the immutable version IDs and encrypted material.
func ReleaseSecretRuntimeInputsFresh(ctx context.Context, store RuntimeAppValuesStore, account, app, deployment string, inputs RuntimeConfigInputs) (bool, bool, error) {
	snapshot, err := store.RuntimeAppValuesForDeployment(ctx, account, app, deployment)
	if err != nil {
		return false, false, err
	}
	if !snapshot.ReleaseSecretsPinned {
		return false, false, nil
	}
	vars := map[string]string{}
	for _, v := range snapshot.Values {
		vars[v.Key] = v.Value
	}
	versions := map[string]int64{}
	refs := map[string]string{}
	for _, row := range snapshot.Secrets {
		versions[snapshot.Scope+"/"+row.Key] = row.DeliveryVersion
		refs[row.Key] = api.SecretRefPrefix + row.Key
	}
	return true, inputs.Scope == snapshot.Scope && inputs.AllSecrets && len(inputs.SidecarSecretVersions) == 0 && maps.Equal(inputs.Variables, vars) && maps.Equal(inputs.SecretVersions, versions) && maps.Equal(inputs.SecretRefs, refs), nil
}
func (m *MemStore) candidateReleaseSecretsLocked(d Deployment) ([]releaseSecretVersion, bool, error) {
	app := m.apps[d.AppID]
	if d.releaseConfigAttempt != "" {
		capture, ok := m.releaseConfigurationIntents[app.AccountID+"\x00"+d.releaseConfigAttempt]
		if !ok {
			return nil, false, ErrConflict
		}
		if capture.Descriptor == nil || capture.Descriptor.Secrets == nil {
			return nil, false, nil
		}
		for _, w := range capture.Workloads {
			if w.AppID == d.AppID {
				return cloneReleaseSecrets(w.SecretTarget), true, nil
			}
		}
		return nil, false, ErrConflict
	}
	release := m.projectReleaseSets[m.activeProjectReleaseSets[releaseKey(app.ProjectID, normalizedDeploymentScope(d.Scope))]]
	for _, member := range release.Members {
		if member.AppID == app.ID {
			rows, ok := m.releaseSecretPins[member.DeploymentID]
			return cloneReleaseSecrets(rows), ok, nil
		}
	}
	return nil, false, nil
}
