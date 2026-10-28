// adr: 20261028100000
package state

import (
	"encoding/json"
	"sort"
	"time"
)

// PreviewValuesScope is the app scope that holds the variables and secrets a
// production app's PR previews receive. A PR preview inherits no project
// environment and none of production's own values (ADR-310); these are the
// values the owner chose for unreviewed code.
const PreviewValuesScope = "preview"

type runtimePreviewValue struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// previewInheritedEnv turns the production app's preview-scope variables
// into the PR preview's inherited variables, below the preview's own.
func previewInheritedEnv(snapshot RuntimeAppEnvSnapshot, values []runtimePreviewValue) []AppEnv {
	own := make(map[string]bool, len(snapshot.Values))
	for _, row := range snapshot.Values {
		own[row.Key] = true
	}
	rows := make([]AppEnv, 0, len(values))
	for _, value := range values {
		if own[value.Key] {
			continue
		}
		rows = append(rows, AppEnv{AccountID: snapshot.AccountID, AppID: snapshot.AppID, Scope: snapshot.Scope,
			Key: value.Key, Value: value.Value, CreatedAt: value.CreatedAt.UTC(), UpdatedAt: value.UpdatedAt.UTC()})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows
}

// decodePreviewInheritedEnv reads the preview_values column of the runtime
// snapshot queries; it is empty unless the deployment is a PR preview's.
func decodePreviewInheritedEnv(snapshot RuntimeAppEnvSnapshot, raw []byte) ([]AppEnv, error) {
	var values []runtimePreviewValue
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, ErrConflict
		}
	}
	return previewInheritedEnv(snapshot, values), nil
}

// previewInheritedSecrets turns the production app's preview-scope secrets
// into the PR preview's inherited secrets. Each is sealed to the account and
// its key, so the preview's VM opens it like an inherited project secret.
func previewInheritedSecrets(snapshot RuntimeAppValuesSnapshot, secrets []AppSecret) []ProjectEnvironmentSecret {
	rows := make([]ProjectEnvironmentSecret, 0, len(secrets))
	for _, secret := range secrets {
		rows = append(rows, ProjectEnvironmentSecret{AccountID: snapshot.AccountID, EnvironmentSlug: PreviewValuesScope,
			Key: secret.Key, Ciphertext: secret.Ciphertext, Kid: secret.Kid, ValueHash: secret.ValueHash,
			SecretVersion: secret.SecretVersion, CreatedAt: secret.CreatedAt.UTC(), UpdatedAt: secret.UpdatedAt.UTC()})
	}
	return inheritedProjectSecrets(snapshot, rows)
}

// decodePreviewInheritedSecrets reads the preview_secrets column of
// ReadRuntimeAppValuesForDeployment.
func decodePreviewInheritedSecrets(snapshot RuntimeAppValuesSnapshot, raw []byte) ([]ProjectEnvironmentSecret, error) {
	var values []struct {
		Key           string    `json:"key"`
		Ciphertext    []byte    `json:"ciphertext"`
		Kid           string    `json:"kid"`
		ValueHash     string    `json:"value_hash"`
		SecretVersion int64     `json:"secret_version"`
		CreatedAt     time.Time `json:"created_at"`
		UpdatedAt     time.Time `json:"updated_at"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, ErrConflict
		}
	}
	secrets := make([]AppSecret, 0, len(values))
	for _, value := range values {
		secrets = append(secrets, AppSecret{Key: value.Key, Ciphertext: value.Ciphertext, Kid: value.Kid,
			ValueHash: value.ValueHash, SecretVersion: value.SecretVersion, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt})
	}
	return previewInheritedSecrets(snapshot, secrets), nil
}
