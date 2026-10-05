package state

import (
	"encoding/hex"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Only the private capture contains values. Managed envelopes identify source
// bindings but are never inserted into the target as customer-owned secrets.
type projectCloneWorkloadValues struct {
	Variables []projectCloneVariable `json:"variables"`
	Secrets   []projectCloneSecret   `json:"secrets"`
}

func normalizeCloneWorkloadValues(appID, scope string, values projectCloneWorkloadValues) (projectCloneWorkloadValues, error) {
	values.Variables = append([]projectCloneVariable{}, values.Variables...)
	values.Secrets = append([]projectCloneSecret{}, values.Secrets...)
	variables, secrets := map[string]bool{}, map[string]bool{}
	for _, v := range values.Variables {
		if v.AppID != appID || v.Scope != scope || v.Key == "" || variables[v.Key] {
			return projectCloneWorkloadValues{}, ErrConflict
		}
		variables[v.Key] = true
	}
	for _, s := range values.Secrets {
		if s.AppID != appID || s.Scope != scope || s.Key == "" || secrets[s.Key] || s.SecretVersion < 0 ||
			(s.SecretClass != "" && s.SecretClass != SecretClassPersistent && s.SecretClass != SecretClassEphemeral) ||
			(s.ManagedPostgresBindingID != "" && s.ManagedObjectStorageCredentialID != "") {
			return projectCloneWorkloadValues{}, ErrConflict
		}
		if _, err := cloneSecretCiphertext(s); err != nil {
			return projectCloneWorkloadValues{}, err
		}
		secrets[s.Key] = true
	}
	_, err := projectCloneValuesHash(map[string]string{appID: scope}, values.Variables, values.Secrets)
	return values, err
}

func cloneSecretCiphertext(secret projectCloneSecret) ([]byte, error) {
	encoded, ok := strings.CutPrefix(secret.Ciphertext, "\\x")
	if !ok {
		return nil, ErrConflict
	}
	ciphertext, err := hex.DecodeString(encoded)
	if err != nil || len(ciphertext) == 0 {
		return nil, ErrConflict
	}
	return ciphertext, nil
}

func capturedCloneValues(records []projectCloneWorkloadRecord) (map[string]projectCloneWorkloadValues, error) {
	if len(records) == 0 {
		return nil, nil
	}
	values := make(map[string]projectCloneWorkloadValues, len(records))
	for _, record := range records {
		if record.snapshot.Values == nil || record.SourceValuesHash == "" {
			return nil, ErrConflict
		}
		v, err := normalizeCloneWorkloadValues(record.AppID, record.SourceScope, *record.snapshot.Values)
		if err != nil {
			return nil, err
		}
		values[record.AppID] = v
	}
	return values, nil
}

func (clone ProjectEnvironmentClone) capturedValuesHash(scopes map[string]string) (string, error) {
	var variables []projectCloneVariable
	var secrets []projectCloneSecret
	for _, values := range clone.capturedValues {
		variables = append(variables, values.Variables...)
		secrets = append(secrets, values.Secrets...)
	}
	return projectCloneValuesHash(scopes, variables, secrets)
}

func cloneSecretManagedID(secret projectCloneSecret) string {
	if secret.ManagedPostgresBindingID != "" {
		return secret.ManagedPostgresBindingID
	}
	return secret.ManagedObjectStorageCredentialID
}

func checkCapturedCloneQuota(slug string, values projectCloneWorkloadValues, totalSecrets, totalVariables int, prepared bool, limits api.Limits) error {
	addedSecrets := len(values.Secrets)
	if prepared {
		for _, secret := range values.Secrets {
			if cloneSecretManagedID(secret) != "" {
				addedSecrets--
			}
		}
	}
	if observed := totalSecrets + addedSecrets; limits.SecretCountMax > 0 && observed > limits.SecretCountMax {
		return &ProjectEnvironmentCloneQuotaError{WorkloadSlug: slug, Resource: "secrets", Limit: limits.SecretCountMax, Observed: observed}
	}
	if observed := totalVariables + len(values.Variables); limits.EnvVarsMax > 0 && observed > limits.EnvVarsMax {
		return &ProjectEnvironmentCloneQuotaError{WorkloadSlug: slug, Resource: "variables", Limit: limits.EnvVarsMax, Observed: observed}
	}
	return nil
}

func (m *MemStore) copyCapturedCloneValuesLocked(clone ProjectEnvironmentClone, now time.Time) (int, int) {
	variableCount, secretCount := 0, 0
	for appID, values := range clone.capturedValues {
		for _, value := range values.Variables {
			m.envs[envKey{AppID: appID, Scope: clone.TargetSlug, Key: value.Key}] = AppEnv{
				AccountID: clone.AccountID, AppID: appID, Scope: clone.TargetSlug, Key: value.Key, Value: value.Value, CreatedAt: now, UpdatedAt: now,
			}
			variableCount++
		}
		for _, value := range values.Secrets {
			if cloneSecretManagedID(value) != "" {
				continue
			}
			ciphertext, _ := cloneSecretCiphertext(value) // validated before any target writes
			m.secrets[secretKey{AppID: appID, Scope: clone.TargetSlug, Key: value.Key}] = AppSecret{
				AccountID: clone.AccountID, AppID: appID, Scope: clone.TargetSlug, Key: value.Key, Ciphertext: ciphertext,
				Kid: value.Kid, ValueHash: value.ValueHash, SecretVersion: value.SecretVersion, SecretClass: value.SecretClass,
				DeliveryVersion: 1, DeliveryStatus: SecretDeliveryPending, CreatedAt: now, UpdatedAt: now,
			}
			secretCount++
		}
	}
	return variableCount, secretCount
}
