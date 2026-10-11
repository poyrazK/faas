package managedpostgres

import (
	"context"
	"time"
)

// ResourceID is the durably reserved target owner. Snapshot carries the
// operation-owned capture identity; ExpectedTargetResourceID is pinned once
// the native target is observed. Neither selector can be replaced on retry.
type SnapshotRestoreRequest struct {
	ResourceID, ProviderSnapshotID, ExpectedTargetResourceID string
	Snapshot                                                 SnapshotCaptureRequest
}

// Restored describes completion of the native storage fork only. It is not
// application readiness or proof that copied maintenance resources, credentials,
// database admission flags and target configuration have been isolated.
type SnapshotRestoreObservation struct {
	ProviderResourceID, ProviderSnapshotID, SourceDataResourceID string
	PointInTime, SnapshotCreatedAt, TargetCreatedAt              time.Time
	Restored                                                     bool
}

type SnapshotRestoreProvider interface {
	RestoreSnapshot(context.Context, RestoreSourceDefinition, SnapshotRestoreRequest) (SnapshotRestoreObservation, error)
	// Discovery never creates a replacement after an uncertain POST outcome.
	FindSnapshotRestore(context.Context, RestoreSourceDefinition, SnapshotRestoreRequest) (SnapshotRestoreObservation, error)
}

// The caller must persist target ownership and first-dispatch intent under its
// live clone lease before this internal operation. A failed acknowledgement
// retains that intent; the replacement worker uses FindSnapshotRestore.
func (s *Service) RestoreSnapshot(ctx context.Context, accountID string, definition RestoreSourceDefinition, request SnapshotRestoreRequest) (SnapshotRestoreObservation, error) {
	if s == nil || !s.provisioningEnabled() || !s.provisioningAllowed(ctx, accountID) {
		return SnapshotRestoreObservation{}, ErrUnavailable
	}
	if accountID == "" {
		return SnapshotRestoreObservation{}, ErrInvalid
	}
	return s.restoreSnapshot(ctx, definition, request, true)
}

func (s *Service) FindSnapshotRestore(ctx context.Context, definition RestoreSourceDefinition, request SnapshotRestoreRequest) (SnapshotRestoreObservation, error) {
	return s.restoreSnapshot(ctx, definition, request, false)
}

func (s *Service) restoreSnapshot(ctx context.Context, definition RestoreSourceDefinition, request SnapshotRestoreRequest, create bool) (SnapshotRestoreObservation, error) {
	if err := request.Validate(); err != nil || request.Snapshot.SourceResourceID != definition.DataResourceID {
		return SnapshotRestoreObservation{}, ErrInvalid
	}
	backend, err := s.checkpointBackend(definition)
	if err != nil {
		return SnapshotRestoreObservation{}, err
	}
	provider, ok := backend.Provider.(SnapshotRestoreProvider)
	if !ok {
		return SnapshotRestoreObservation{}, ErrUnsupported
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	var actual SnapshotRestoreObservation
	if create {
		actual, err = provider.RestoreSnapshot(providerCtx, definition, request)
	} else {
		actual, err = provider.FindSnapshotRestore(providerCtx, definition, request)
	}
	if err != nil {
		return SnapshotRestoreObservation{}, err
	}
	if !validOpaqueID(actual.ProviderResourceID) || actual.ProviderResourceID == definition.DataResourceID ||
		actual.ProviderSnapshotID != request.ProviderSnapshotID || actual.SourceDataResourceID != definition.DataResourceID ||
		!actual.PointInTime.Equal(request.Snapshot.PointInTime) || actual.SnapshotCreatedAt.IsZero() ||
		actual.SnapshotCreatedAt.Before(actual.PointInTime) || actual.TargetCreatedAt.Before(actual.SnapshotCreatedAt) ||
		actual.TargetCreatedAt.After(time.Now()) ||
		request.ExpectedTargetResourceID != "" && request.ExpectedTargetResourceID != actual.ProviderResourceID {
		return SnapshotRestoreObservation{}, ErrConflict
	}
	return actual, nil
}

func (r SnapshotRestoreRequest) Validate() error {
	if !validOpaqueID(r.ResourceID) || !validOpaqueID(r.ProviderSnapshotID) || !validOpaqueID(r.Snapshot.ResourceID) ||
		!validOpaqueID(r.Snapshot.IdempotencyKey) || !validDataResourceID(r.Snapshot.SourceResourceID) ||
		r.Snapshot.PointInTime.IsZero() || r.Snapshot.PointInTime.Nanosecond()%1000 != 0 || !r.Snapshot.PointInTime.Before(time.Now()) ||
		r.ExpectedTargetResourceID != "" && (!validDataResourceID(r.ExpectedTargetResourceID) || r.ExpectedTargetResourceID == r.Snapshot.SourceResourceID) {
		return ErrInvalid
	}
	return nil
}
