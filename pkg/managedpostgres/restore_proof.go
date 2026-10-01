package managedpostgres

import (
	"context"
	"time"
)

func newCloneRestoreProof(database Database, observed ObservedDatabase, now time.Time) (RestoreProof, error) {
	if database.EnvironmentCloneOperationID == "" || database.DesiredGeneration < 1 || now.IsZero() ||
		observed.Status != ProviderStatusReady || observed.Spec != database.Spec || observed.ProviderResourceID != database.ProviderResourceID {
		return RestoreProof{}, ErrConflict
	}
	if err := validateCloneRestoreObservation(database, observed); err != nil {
		return RestoreProof{}, err
	}
	return RestoreProof{DatabaseID: database.ID, AccountID: database.AccountID, OperationID: database.EnvironmentCloneOperationID,
		BackendID: database.BackendID, BackendFingerprint: database.BackendFingerprint, ProviderResourceID: observed.ProviderResourceID,
		SourceDatabaseID: database.RestoreSourceDatabaseID, Lineage: *observed.RestoreLineage, Spec: observed.Spec,
		Generation: database.DesiredGeneration, ObservedAt: now}, nil
}

func validateCloneRestoreProof(database Database, proof RestoreProof) error {
	if database.EnvironmentCloneOperationID == "" || database.State != StateReady || database.LeaseToken != "" || database.DeletedAt != nil ||
		proof.DatabaseID != database.ID || proof.AccountID != database.AccountID || proof.OperationID != database.EnvironmentCloneOperationID ||
		proof.BackendID != database.BackendID || proof.BackendFingerprint != database.BackendFingerprint || proof.Spec != database.Spec ||
		proof.SourceDatabaseID != database.RestoreSourceDatabaseID || proof.ProviderResourceID != database.ProviderResourceID ||
		proof.Generation != database.DesiredGeneration || proof.Generation != database.ObservedGeneration || proof.ObservedAt.IsZero() {
		return ErrConflict
	}
	return validateCloneRestoreObservation(database, ObservedDatabase{ProviderResourceID: proof.ProviderResourceID, RestoreLineage: &proof.Lineage})
}

var _ CloneRestoreProofStore = (*MemoryStore)(nil)

func (s *MemoryStore) FinishCloneRestoreProvision(_ context.Context, expected Database, observed ObservedDatabase, now time.Time) (Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, ok := s.databases[expected.ID]
	if !ok {
		return Database{}, ErrNotFound
	}
	if database.AccountID != expected.AccountID || database.State != StateProvisioning || database.LeaseToken == "" ||
		database.LeaseToken != expected.LeaseToken || !database.LeaseUntil.After(now) ||
		database.EnvironmentCloneOperationID != expected.EnvironmentCloneOperationID || database.BackendID != expected.BackendID ||
		database.BackendFingerprint != expected.BackendFingerprint || database.DesiredGeneration != expected.DesiredGeneration {
		return Database{}, ErrConflict
	}
	proof, err := newCloneRestoreProof(database, observed, now)
	if err != nil {
		return Database{}, err
	}
	if _, exists := s.restoreProofs[database.ID]; exists {
		return Database{}, ErrConflict
	}
	database.State, database.ObservedGeneration = StateReady, database.DesiredGeneration
	database.LastErrorCode, database.LeaseToken, database.AttemptCount = "", "", 0
	database.LeaseUntil, database.RetryAt, database.UpdatedAt = time.Time{}, now, now
	s.databases[database.ID], s.restoreProofs[database.ID] = database, proof
	return cloneDatabase(database), nil
}

func (s *MemoryStore) GetCloneRestoreProof(_ context.Context, accountID, databaseID string) (RestoreProof, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, ok := s.databases[databaseID]
	if !ok || database.AccountID != accountID {
		return RestoreProof{}, ErrNotFound
	}
	proof, ok := s.restoreProofs[databaseID]
	if !ok {
		return RestoreProof{}, ErrConflict
	}
	if err := validateCloneRestoreProof(database, proof); err != nil {
		return RestoreProof{}, err
	}
	return proof, nil
}
