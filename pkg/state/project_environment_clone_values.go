package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// ProjectEnvironmentCloneValuesSnapshot fences preparation without exposing
// any source values. It is not a durable complete-environment capture receipt.
type ProjectEnvironmentCloneValuesSnapshot struct {
	ValueScopes map[string]string
	Hash        string
}

type ProjectEnvironmentCloneValuesStore interface {
	CaptureProjectEnvironmentCloneValues(context.Context, string, string, string) (ProjectEnvironmentCloneValuesSnapshot, error)
}

type projectCloneVariable struct {
	AppID string `json:"app_id"`
	Scope string `json:"scope"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Ciphertext is opaque encrypted content. Runtime delivery observations and
// timestamps are deliberately excluded from this configuration fingerprint.
type projectCloneSecret struct {
	AppID                            string `json:"app_id"`
	Scope                            string `json:"scope"`
	Key                              string `json:"key"`
	Ciphertext                       string `json:"ciphertext"`
	Kid                              string `json:"kid"`
	ValueHash                        string `json:"value_hash"`
	SecretClass                      string `json:"secret_class"`
	SecretVersion                    int64  `json:"secret_version"`
	ManagedPostgresBindingID         string `json:"managed_postgres_binding_id"`
	ManagedCredentialRef             string `json:"managed_credential_ref"`
	ManagedCredentialGeneration      int64  `json:"managed_credential_generation"`
	ManagedObjectStorageCredentialID string `json:"managed_object_storage_credential_id"`
}

func projectCloneValuesHash(scopes map[string]string, variables []projectCloneVariable, secrets []projectCloneSecret) (string, error) {
	sort.Slice(variables, func(i, j int) bool {
		if variables[i].AppID != variables[j].AppID {
			return variables[i].AppID < variables[j].AppID
		}
		return variables[i].Key < variables[j].Key
	})
	sort.Slice(secrets, func(i, j int) bool {
		if secrets[i].AppID != secrets[j].AppID {
			return secrets[i].AppID < secrets[j].AppID
		}
		return secrets[i].Key < secrets[j].Key
	})
	for i := range secrets {
		if secrets[i].SecretClass == "" {
			secrets[i].SecretClass = SecretClassPersistent
		}
	}
	// Empty lists have the same identity across both stores.
	if variables == nil {
		variables = []projectCloneVariable{}
	}
	if secrets == nil {
		secrets = []projectCloneSecret{}
	}
	encoded, err := json.Marshal(struct {
		Scopes    map[string]string      `json:"scopes"`
		Variables []projectCloneVariable `json:"variables"`
		Secrets   []projectCloneSecret   `json:"secrets"`
	}{scopes, variables, secrets})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func (m *MemStore) CaptureProjectEnvironmentCloneValues(_ context.Context, accountID, projectID, source string) (ProjectEnvironmentCloneValuesSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	project, ok := m.projects[projectID]
	if !ok || project.AccountID != accountID {
		return ProjectEnvironmentCloneValuesSnapshot{}, ErrNotFound
	}
	if _, err := m.projectEnvironmentBySlugLocked(projectID, source); err != nil {
		return ProjectEnvironmentCloneValuesSnapshot{}, err
	}
	scopes, err := m.projectCloneValueScopesLocked(m.projectCloneAppsLocked(projectID), ProjectEnvironmentClone{ProjectID: projectID, SourceSlug: source})
	if err != nil {
		return ProjectEnvironmentCloneValuesSnapshot{}, err
	}
	hash, err := m.projectCloneValuesHashLocked(scopes)
	return ProjectEnvironmentCloneValuesSnapshot{ValueScopes: scopes, Hash: hash}, err
}

func (m *MemStore) projectCloneValuesHashLocked(scopes map[string]string) (string, error) {
	var variables []projectCloneVariable
	var secrets []projectCloneSecret
	for _, value := range m.envs {
		if scope, ok := scopes[value.AppID]; ok && value.Scope == scope {
			variables = append(variables, projectCloneVariable{AppID: value.AppID, Scope: value.Scope, Key: value.Key, Value: value.Value})
		}
	}
	for _, secret := range m.secrets {
		if scope, ok := scopes[secret.AppID]; !ok || secret.Scope != scope {
			continue
		}
		secrets = append(secrets, projectCloneSecret{
			AppID: secret.AppID, Scope: secret.Scope, Key: secret.Key, Ciphertext: "\\x" + hex.EncodeToString(secret.Ciphertext),
			Kid: secret.Kid, ValueHash: secret.ValueHash, SecretClass: secret.SecretClass, SecretVersion: secret.SecretVersion,
			ManagedPostgresBindingID: secret.ManagedPostgresBindingID, ManagedCredentialRef: secret.ManagedCredentialRef,
			ManagedCredentialGeneration: secret.ManagedCredentialGeneration, ManagedObjectStorageCredentialID: secret.ManagedObjectStorageCredentialID,
		})
	}
	return projectCloneValuesHash(scopes, variables, secrets)
}
