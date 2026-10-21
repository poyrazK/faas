package managedpostgres

import "context"

var _ RestoreVerificationStore = (*MemoryStore)(nil)

func (s *MemoryStore) RecordRestoreVerification(_ context.Context, database Database, v RestoreVerification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.databases[database.ID]
	if !ok || d.AccountID != database.AccountID {
		return ErrNotFound
	}
	if err := v.Validate(d.ProviderResourceID, RestoreRequest{SourceResourceID: d.RestoreSourceResourceID, PointInTime: d.RestorePointInTime}); err != nil {
		return err
	}
	if database.LeaseToken == "" || d.LeaseToken != database.LeaseToken || d.State != StateProvisioning || d.DesiredGeneration != database.DesiredGeneration {
		return ErrConflict
	}
	if old, ok := s.restoreVerifications[d.ID]; ok {
		if !sameRestoreVerification(old, v) {
			return ErrConflict
		}
		return nil
	}
	for id, old := range s.restoreVerifications {
		if other := s.databases[id]; old.ProviderResourceID == v.ProviderResourceID && other.BackendID == d.BackendID {
			return ErrConflict
		}
	}
	s.restoreVerifications[d.ID] = v
	return nil
}

func (s *MemoryStore) GetRestoreVerification(_ context.Context, accountID, databaseID string) (RestoreVerification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.databases[databaseID]
	v, found := s.restoreVerifications[databaseID]
	if !ok || !found || d.AccountID != accountID {
		return RestoreVerification{}, ErrNotFound
	}
	return v, nil
}
