package managedpostgres

import (
	"context"
	"errors"
)

// restoreVerification returns the target's recorded verification, if the
// store keeps them and one exists (ADR-1259).
func (s *Service) restoreVerification(ctx context.Context, database Database) (*RestoreVerification, error) {
	store, ok := s.store.(RestoreVerificationStore)
	if !ok || database.RestoreSourceResourceID == "" || database.ProviderResourceID == "" {
		return nil, nil
	}
	recorded, err := store.GetRestoreVerification(ctx, database.AccountID, database.ID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &recorded, nil
}

// recordRestoreVerification persists the first independent verification of a
// restore target before readiness can be published, so later observations
// confirm it instead of remapping the requested time (ADR-1259). Replaying
// the same record is a no-op; a different one is a conflict.
func (s *Service) recordRestoreVerification(ctx context.Context, database Database, observed ObservedDatabase) error {
	store, ok := s.store.(RestoreVerificationStore)
	if !ok || observed.RestoreVerification == nil || database.RestoreSourceResourceID == "" {
		return nil
	}
	verification := *observed.RestoreVerification
	if err := verification.Validate(database.ProviderResourceID, RestoreRequest{SourceResourceID: database.RestoreSourceResourceID, PointInTime: database.RestorePointInTime}); err != nil {
		return Reasonf(err, "observed restore verification does not describe this restore")
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	return store.RecordRestoreVerification(writeCtx, database, verification)
}

func sameRestoreVerification(a, b RestoreVerification) bool {
	return a.ProviderResourceID == b.ProviderResourceID && a.SourceResourceID == b.SourceResourceID &&
		a.PointInTime.Equal(b.PointInTime) && a.Evidence == b.Evidence
}
