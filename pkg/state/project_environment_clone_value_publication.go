package state

import (
	"encoding/hex"
	"fmt"
)

// Fresh managed credentials need a proof of their independent resource and
// prepared envelope. A customer-value equality check cannot establish that.
var ErrProjectEnvironmentCloneManagedValueProof = fmt.Errorf("managed credential publication proof is unavailable: %w", ErrConflict)

type projectCloneManagedValuePreparations struct {
	Postgres map[string]ProjectEnvironmentClonePostgresBindingPreparation
	Objects  map[string]ProjectEnvironmentCloneObjectCredentialPreparation
}

func validateCloneValuePublication(op ProjectEnvironmentCloneOperation, resources []ProjectEnvironmentCloneResource, records []projectCloneWorkloadRecord, targets map[string]projectCloneWorkloadValues, preparations projectCloneManagedValuePreparations) error {
	if len(records) == 0 {
		return nil
	}
	captured, err := capturedCloneValues(records)
	if err != nil || len(targets) != len(records) {
		return ErrConflict
	}
	receipts := map[string]ProjectEnvironmentCloneResource{}
	for _, resource := range resources {
		if resource.Kind != "variables" && resource.Kind != "secrets" {
			continue
		}
		key := cloneResourceKey(resource)
		if _, duplicate := receipts[key]; duplicate {
			return ErrConflict
		}
		receipts[key] = resource
	}
	if len(receipts) != len(records)*2 {
		return ErrConflict
	}
	resourceProofWorkload := ""
	for _, record := range records {
		for _, kind := range []string{"variables", "secrets"} {
			r := receipts[kind+"\x00"+record.WorkloadSlug]
			if r.SourceID != record.AppID || r.TargetID != record.AppID || r.SourceVersion != record.SourceValuesHash || r.Status != "ready" {
				return fmt.Errorf("clone workload %q lacks a captured %s receipt: %w", record.WorkloadSlug, kind, ErrConflict)
			}
		}
		if err := validateCloneWorkloadTargetValues(op, record, captured[record.AppID], targets[record.AppID], preparations); err != nil {
			return err
		}
		// Standalone buckets are part of the capture even without managed
		// application secrets. A workload/value proof cannot prove their copy.
		if definitions := record.snapshot.Bindings; definitions != nil && (len(definitions.Postgres) > 0 || len(definitions.Buckets) > 0) {
			resourceProofWorkload = record.WorkloadSlug
		}
	}
	if resourceProofWorkload != "" {
		return fmt.Errorf("clone workload %q: %w", resourceProofWorkload, ErrProjectEnvironmentCloneResourcePublicationProof)
	}
	return nil
}

func validateCloneWorkloadTargetValues(op ProjectEnvironmentCloneOperation, record projectCloneWorkloadRecord, captured, target projectCloneWorkloadValues, preparations projectCloneManagedValuePreparations) error {
	capturedHash, err := projectCloneValuesHash(map[string]string{record.AppID: record.SourceScope}, captured.Variables, captured.Secrets)
	if err != nil || capturedHash != record.SourceValuesHash {
		return ErrConflict
	}
	expected, err := expectedCloneWorkloadTargetValues(op, record, captured, preparations)
	if err != nil {
		return fmt.Errorf("clone workload %q: %w", record.WorkloadSlug, err)
	}
	target, err = normalizeCloneWorkloadValues(record.AppID, op.TargetEnvironment, target)
	if err != nil {
		return fmt.Errorf("clone workload %q target values are invalid: %w", record.WorkloadSlug, ErrConflict)
	}
	for i := range target.Secrets {
		if cloneSecretManagedID(target.Secrets[i]) != "" && target.Secrets[i].SecretVersion == 0 {
			// Preparation defines the default first version as 1 in both stores.
			target.Secrets[i].SecretVersion = 1
		}
	}
	scopes := map[string]string{record.AppID: op.TargetEnvironment}
	expectedHash, err := projectCloneValuesHash(scopes, expected.Variables, expected.Secrets)
	if err != nil {
		return err
	}
	targetHash, err := projectCloneValuesHash(scopes, target.Variables, target.Secrets)
	if err != nil || targetHash != expectedHash {
		// Configuration values and encrypted envelopes must stay out of errors.
		return fmt.Errorf("clone workload %q target values differ from captured configuration or prepared bindings: %w", record.WorkloadSlug, ErrConflict)
	}
	return nil
}

// Customer values are copied from the capture. Managed envelopes must
// instead match their authenticated independent target preparation, including
// its fresh credential owner, ciphertext, key identity, class and first version.
func expectedCloneWorkloadTargetValues(op ProjectEnvironmentCloneOperation, record projectCloneWorkloadRecord, captured projectCloneWorkloadValues, preparations projectCloneManagedValuePreparations) (projectCloneWorkloadValues, error) {
	expected := projectCloneWorkloadValues{Variables: append([]projectCloneVariable{}, captured.Variables...), Secrets: append([]projectCloneSecret{}, captured.Secrets...)}
	for i := range expected.Variables {
		expected.Variables[i].Scope = op.TargetEnvironment
	}
	for i, source := range expected.Secrets {
		expected.Secrets[i].Scope = op.TargetEnvironment
		if source.ManagedPostgresBindingID != "" {
			prepared, ok := preparations.Postgres[source.ManagedPostgresBindingID]
			if !ok || validateClonePreparedPostgresIdentity(op, source.ManagedPostgresBindingID, record.AppID, prepared) != nil ||
				prepared.Binding.EnvironmentKey != source.Key || prepared.Binding.CredentialRef == source.ManagedCredentialRef {
				return expected, ErrProjectEnvironmentCloneManagedValueProof
			}
			secret := prepared.Secret
			expected.Secrets[i] = projectCloneSecret{AppID: secret.AppID, Scope: secret.Scope, Key: secret.Key, Ciphertext: "\\x" + hex.EncodeToString(secret.Ciphertext),
				Kid: secret.Kid, ValueHash: secret.ValueHash, SecretClass: secret.SecretClass, SecretVersion: secret.SecretVersion,
				ManagedPostgresBindingID: secret.ManagedPostgresBindingID, ManagedCredentialRef: secret.ManagedCredentialRef, ManagedCredentialGeneration: secret.ManagedCredentialGeneration}
			continue
		}
		if source.ManagedObjectStorageCredentialID == "" {
			continue
		}
		prepared, ok := preparations.Objects[source.ManagedObjectStorageCredentialID]
		if !ok || prepared.OperationID != op.ID || prepared.SourceCredentialID != source.ManagedObjectStorageCredentialID || prepared.AppID != record.AppID ||
			prepared.Credential.AccountID != op.AccountID || prepared.Credential.ManagedAppID != record.AppID || prepared.Credential.ManagedScope != op.TargetEnvironment ||
			prepared.Credential.ID == source.ManagedObjectStorageCredentialID || !cloneObjectPreparationSecretsHaveOneOwner(prepared) {
			return expected, ErrProjectEnvironmentCloneManagedValueProof
		}
		matches := 0
		for _, secret := range prepared.Secrets {
			if secret.Key != source.Key {
				continue
			}
			if secret.AppID != record.AppID || secret.AccountID != op.AccountID || secret.Scope != op.TargetEnvironment {
				return expected, ErrProjectEnvironmentCloneManagedValueProof
			}
			matches++
			expected.Secrets[i] = projectCloneSecret{AppID: secret.AppID, Scope: secret.Scope, Key: secret.Key, Ciphertext: "\\x" + hex.EncodeToString(secret.Ciphertext),
				Kid: secret.Kid, ValueHash: secret.ValueHash, SecretClass: secret.SecretClass, SecretVersion: secret.SecretVersion, ManagedObjectStorageCredentialID: secret.ManagedObjectStorageCredentialID}
		}
		if matches != 1 {
			return expected, ErrProjectEnvironmentCloneManagedValueProof
		}
	}
	return expected, nil
}
