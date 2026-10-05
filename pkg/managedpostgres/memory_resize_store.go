package managedpostgres

import (
	"context"
	"time"
)

var _ ResizeStore = (*MemoryStore)(nil)

func (s *MemoryStore) ReserveResize(_ context.Context, expected Database, operation ResizeOperation, now time.Time) (ResizeOperation, error) {
	if !validResizeOperation(operation) || now.IsZero() || !resizeSourceMatches(expected, operation) {
		return ResizeOperation{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.resizes[operation.ID]; ok {
		if existing.AccountID != operation.AccountID || existing.DatabaseID != operation.DatabaseID || existing.TargetClass != operation.TargetClass {
			return ResizeOperation{}, ErrConflict
		}
		return s.resizeView(existing), nil
	}
	database, ok := s.databases[operation.DatabaseID]
	if !ok || database.AccountID != operation.AccountID {
		return ResizeOperation{}, ErrNotFound
	}
	if !resizeSourceMatches(database, operation) || database.State != StateReady || database.DeletedAt != nil ||
		database.DesiredGeneration != expected.DesiredGeneration || database.ObservedGeneration != database.DesiredGeneration ||
		operation.Generation != database.DesiredGeneration+1 || database.LeaseUntil.After(now) || s.databaseCutoverPinned(database.ID) {
		return ResizeOperation{}, ErrConflict
	}
	for _, child := range s.databases {
		if child.RestoreSourceDatabaseID == database.ID && child.State != StateReady && child.State != StateDeleted {
			return ResizeOperation{}, ErrConflict
		}
	}
	for _, binding := range s.bindings {
		if binding.DatabaseID == database.ID && binding.State != BindingStateDeleted &&
			(binding.State != BindingStateReady || binding.RotationPreviousGeneration != 0 || binding.LeaseToken != "") {
			return ResizeOperation{}, ErrConflict
		}
	}
	database.State, database.DesiredGeneration = StateUpdating, operation.Generation
	database.LastErrorCode, database.LeaseToken, database.AttemptCount = "", "", 0
	database.LeaseUntil, database.RetryAt, database.UpdatedAt = time.Time{}, now, now
	s.databases[database.ID], s.resizes[operation.ID] = database, operation
	return operation, nil
}

func (s *MemoryStore) resizeView(operation ResizeOperation) ResizeOperation {
	if operation.State == ResizePending {
		operation.LastErrorCode = s.databases[operation.DatabaseID].LastErrorCode
	}
	return operation
}

func (s *MemoryStore) GetResize(_ context.Context, account, id string) (ResizeOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.resizes[id]
	if !ok || operation.AccountID != account {
		return ResizeOperation{}, ErrNotFound
	}
	return s.resizeView(operation), nil
}

func (s *MemoryStore) ActiveResize(_ context.Context, account, database string) (ResizeOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, operation := range s.resizes {
		if operation.AccountID == account && operation.DatabaseID == database && operation.State == ResizePending {
			return s.resizeView(operation), nil
		}
	}
	return ResizeOperation{}, ErrNotFound
}

func (s *MemoryStore) FinishResize(_ context.Context, expected Database, operation ResizeOperation, observed ObservedDatabase, now time.Time) (Database, error) {
	if validateResizeObservation(operation, observed) != nil || observed.Status != ProviderStatusReady || observed.Spec != operation.TargetSpec() {
		return Database{}, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	database, ok := s.databases[expected.ID]
	actual, found := s.resizes[operation.ID]
	if !ok || !found || validateResizeObservation(actual, observed) != nil || actual.State != ResizePending || actual.Generation != operation.Generation || actual.TargetSpec() != operation.TargetSpec() ||
		!resizeSourceMatches(database, actual) || !resizeSourceMatches(expected, actual) || database.State != StateUpdating ||
		database.DesiredGeneration != actual.Generation || expected.DesiredGeneration != actual.Generation ||
		database.ObservedGeneration != actual.Generation-1 || database.LeaseToken == "" || database.LeaseToken != expected.LeaseToken ||
		now.IsZero() || !database.LeaseUntil.After(now) {
		return Database{}, ErrConflict
	}
	database.Spec, database.State, database.ObservedGeneration = observed.Spec, StateReady, database.DesiredGeneration
	database.LastErrorCode, database.LeaseToken, database.AttemptCount = "", "", 0
	database.LeaseUntil, database.RetryAt, database.UpdatedAt = time.Time{}, now, now
	actual.State, actual.CompletedAt = ResizeSucceeded, now
	s.databases[database.ID], s.resizes[actual.ID] = database, actual
	return cloneDatabase(database), nil
}
