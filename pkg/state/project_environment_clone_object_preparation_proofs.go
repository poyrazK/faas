package state

import (
	"errors"
	"fmt"
	"sort"
)

func capturedCloneManagedObjectCredentials(clone ProjectEnvironmentClone) []string {
	ids := map[string]bool{}
	for _, values := range clone.capturedValues {
		for _, secret := range values.Secrets {
			if secret.ManagedObjectStorageCredentialID != "" {
				ids[secret.ManagedObjectStorageCredentialID] = true
			}
		}
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func validateClonePreparedObjectIdentity(clone ProjectEnvironmentClone, sourceID string, prepared ProjectEnvironmentCloneObjectCredentialPreparation) error {
	if prepared.OperationID != clone.CloneOperationID || prepared.SourceCredentialID != sourceID || prepared.Credential.ManagedAppID != prepared.AppID ||
		prepared.Credential.ManagedScope != clone.TargetSlug || len(prepared.Secrets) != 6 {
		return ErrProjectEnvironmentCloneManagedValueProof
	}
	for _, id := range clone.PreparedManagedBindingIDs {
		if id == prepared.Credential.ID {
			return nil
		}
	}
	return ErrProjectEnvironmentCloneManagedValueProof
}

func cloneObjectPreparationProofError(sourceID string, cause ...error) error {
	if len(cause) > 0 && cause[0] != nil && !errors.Is(cause[0], ErrNotFound) && !errors.Is(cause[0], ErrConflict) && !errors.Is(cause[0], ErrInvalidArgument) {
		return cause[0]
	}
	// This identity is a source catalogue ID, never credential material.
	return fmt.Errorf("clone object binding %q lacks an authentic target preparation: %w", sourceID, ErrProjectEnvironmentCloneManagedValueProof)
}

func (m *MemStore) checkCapturedCloneObjectPreparationsLocked(clone ProjectEnvironmentClone) error {
	ids := capturedCloneManagedObjectCredentials(clone)
	if len(ids) == 0 {
		return nil
	}
	op := m.projectEnvironmentCloneOperations[clone.CloneOperationID]
	records := make([]projectCloneWorkloadRecord, 0, len(m.projectEnvironmentCloneWorkloads[op.ID]))
	for _, record := range m.projectEnvironmentCloneWorkloads[op.ID] {
		records = append(records, record)
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return err
	}
	for _, sourceID := range ids {
		prepared, exists := m.projectEnvironmentCloneObjectCredentials[cloneObjectManifestKey(op.ID, sourceID)]
		if !exists || validateClonePreparedObjectIdentity(clone, sourceID, prepared) != nil {
			return cloneObjectPreparationProofError(sourceID)
		}
		request := cloneObjectCredentialReplayRequest(prepared)
		if err := validateCloneObjectCredentialRequest(op, views, request); err != nil {
			return cloneObjectPreparationProofError(sourceID)
		}
		if err := m.validateCloneCredentialBucketLocked(op, views, request); err != nil {
			return cloneObjectPreparationProofError(sourceID)
		}
		if _, err := m.verifyPreparedCloneObjectCredentialLocked(prepared); err != nil {
			return cloneObjectPreparationProofError(sourceID)
		}
	}
	return nil
}
