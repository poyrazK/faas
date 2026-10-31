// adr: 1139
package state

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// inheritedProjectSecrets returns the project environment secrets a deployment
// inherits (ADR-1139), sorted by key, without the keys the app sets itself in
// its scope as a variable or a secret: anything an app sets itself wins.
// Secret references and explicit secret selections are deployment intent;
// schedd applies them.
func inheritedProjectSecrets(snapshot RuntimeAppValuesSnapshot, rows []ProjectEnvironmentSecret) []ProjectEnvironmentSecret {
	own := make(map[string]bool, len(snapshot.Values)+len(snapshot.Secrets))
	for _, row := range snapshot.Values {
		own[row.Key] = true
	}
	for _, row := range snapshot.Secrets {
		own[row.Key] = true
	}
	inherited := make([]ProjectEnvironmentSecret, 0, len(rows))
	for _, row := range rows {
		if own[row.Key] {
			continue
		}
		row.Ciphertext = append([]byte(nil), row.Ciphertext...)
		inherited = append(inherited, row)
	}
	sort.Slice(inherited, func(i, j int) bool { return inherited[i].Key < inherited[j].Key })
	return inherited
}

// decodeInheritedProjectSecrets reads ReadRuntimeAppValuesForDeployment's
// project_secrets column.
func decodeInheritedProjectSecrets(snapshot RuntimeAppValuesSnapshot, projectID string, raw []byte) ([]ProjectEnvironmentSecret, error) {
	var values []struct {
		Key           string    `json:"key"`
		Ciphertext    []byte    `json:"ciphertext"`
		Kid           string    `json:"kid"`
		ValueHash     string    `json:"value_hash"`
		SecretVersion int64     `json:"secret_version"`
		CreatedAt     time.Time `json:"created_at"`
		UpdatedAt     time.Time `json:"updated_at"`
	}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, ErrConflict
	}
	rows := make([]ProjectEnvironmentSecret, 0, len(values))
	for _, value := range values {
		rows = append(rows, ProjectEnvironmentSecret{AccountID: snapshot.AccountID, ProjectID: projectID,
			EnvironmentSlug: workloadEnvironmentSlug(snapshot.Scope), Key: value.Key, Ciphertext: value.Ciphertext,
			Kid: value.Kid, ValueHash: value.ValueHash, SecretVersion: value.SecretVersion,
			CreatedAt: value.CreatedAt.UTC(), UpdatedAt: value.UpdatedAt.UTC()})
	}
	return inheritedProjectSecrets(snapshot, rows), nil
}

// WithProjectSecretRevision adds the environment's shared secrets to a
// qualification's secret revision hashes under api.ProjectSecretsRevisionKey,
// so a promotion refuses once one changed after qualification. An environment
// without project secrets adds nothing, which keeps qualifications recorded
// before ADR-1139 comparable.
func WithProjectSecretRevision(hashes map[string]string, secrets []ProjectEnvironmentSecret) (map[string]string, error) {
	if len(secrets) == 0 {
		return hashes, nil
	}
	revisions := make([]api.ProjectEnvironmentSecretRevision, 0, len(secrets))
	for _, secret := range secrets {
		revisions = append(revisions, api.ProjectEnvironmentSecretRevision{Key: secret.Key, Version: secret.SecretVersion})
	}
	hash, err := api.ProjectEnvironmentSecretRevisionHash(revisions)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(hashes)+1)
	for key, value := range hashes {
		out[key] = value
	}
	out[api.ProjectSecretsRevisionKey] = hash
	return out, nil
}

type inheritedSecret struct {
	Key       string
	Version   int64
	ValueHash string
}

// runtimeInheritedSecrets validates the inherited project secrets and returns
// them for the fence. Keys, versions and value fingerprints count; a master-key
// reseal, which changes only the envelope, keeps the fingerprint.
func runtimeInheritedSecrets(snapshot RuntimeAppValuesSnapshot, ownVariables map[string]bool) ([]inheritedSecret, error) {
	own := make(map[string]bool, len(snapshot.Secrets))
	for _, row := range snapshot.Secrets {
		own[row.Key] = true
	}
	inherited := make([]inheritedSecret, 0, len(snapshot.InheritedSecrets))
	seen := map[string]bool{}
	for _, row := range snapshot.InheritedSecrets {
		if row.AccountID != snapshot.AccountID || api.ValidateEnvKey(row.Key) != nil || ownVariables[row.Key] || own[row.Key] ||
			seen[row.Key] || row.SecretVersion < 1 || row.ValueHash == "" || len(row.Ciphertext) == 0 {
			return nil, ErrConflict
		}
		seen[row.Key] = true
		inherited = append(inherited, inheritedSecret{row.Key, row.SecretVersion, row.ValueHash})
	}
	sort.Slice(inherited, func(i, j int) bool { return inherited[i].Key < inherited[j].Key })
	return inherited, nil
}
