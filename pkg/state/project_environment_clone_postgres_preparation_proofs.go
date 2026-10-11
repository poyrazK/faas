package state

import (
	"errors"
	"fmt"
	"sort"
)

func capturedCloneManagedPostgresBindings(captured map[string]projectCloneWorkloadValues) []string {
	ids := map[string]bool{}
	for _, values := range captured {
		for _, secret := range values.Secrets {
			if secret.ManagedPostgresBindingID != "" {
				ids[secret.ManagedPostgresBindingID] = true
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

func validateClonePreparedPostgresIdentity(op ProjectEnvironmentCloneOperation, sourceID, appID string, prepared ProjectEnvironmentClonePostgresBindingPreparation) error {
	b := prepared.Binding
	if b.OperationID != op.ID || b.SourceBindingID != sourceID || b.AccountID != op.AccountID || b.AppID != appID || b.Scope != op.TargetEnvironment ||
		b.ID == "" || b.ID == sourceID || b.DatabaseID == "" || b.State != "ready" || b.ProviderIdentityID == "" || b.CredentialGeneration != 1 ||
		b.CredentialRef != clonePostgresCredentialRef(b.ID, 1) || validateClonePostgresPreparationSecret(b, prepared.Secret) != nil {
		return ErrProjectEnvironmentCloneManagedValueProof
	}
	return nil
}

func clonePostgresPreparationProofError(sourceID string, cause ...error) error {
	if len(cause) > 0 && cause[0] != nil && !errors.Is(cause[0], ErrNotFound) && !errors.Is(cause[0], ErrConflict) && !errors.Is(cause[0], ErrInvalidArgument) {
		return cause[0]
	}
	// A source catalogue identity is safe to report; credential material is not.
	return fmt.Errorf("clone PostgreSQL binding %q lacks an authentic target preparation: %w", sourceID, ErrProjectEnvironmentCloneManagedValueProof)
}

// MemStore cannot capture a durable PostgreSQL catalogue or prove a provider
// restore. Do not accept caller-supplied IDs as independent preparation evidence.
func (m *MemStore) checkCapturedClonePostgresPreparationsLocked(clone ProjectEnvironmentClone) error {
	if ids := capturedCloneManagedPostgresBindings(clone.capturedValues); len(ids) > 0 {
		return clonePostgresPreparationProofError(ids[0])
	}
	return nil
}
