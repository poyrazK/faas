package managedpostgres

import (
	"context"
	"errors"
)

// These internal operations consume an already reserved, leased clone intent.
// They resolve only its frozen backend; they never select today's default or
// acquire new customer authority. The caller must persist observations before
// allowing the coordinated capture to release its writer barrier.
func (s *Service) CaptureSnapshot(ctx context.Context, accountID string, definition RestoreSourceDefinition, request SnapshotCaptureRequest) (DatabaseSnapshot, error) {
	if s == nil || !s.provisioningEnabled() || !s.provisioningAllowed(ctx, accountID) {
		return DatabaseSnapshot{}, ErrUnavailable
	}
	if accountID == "" || request.SourceResourceID != definition.DataResourceID || definition.Spec.Validate() != nil {
		return DatabaseSnapshot{}, ErrInvalid
	}
	provider, err := s.snapshotProvider(definition)
	if err != nil {
		return DatabaseSnapshot{}, err
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	return provider.CaptureSnapshot(providerCtx, request)
}

func (s *Service) FindSnapshot(ctx context.Context, definition RestoreSourceDefinition, request SnapshotCaptureRequest) (DatabaseSnapshot, error) {
	if request.SourceResourceID != definition.DataResourceID {
		return DatabaseSnapshot{}, ErrInvalid
	}
	provider, err := s.snapshotProvider(definition)
	if err != nil {
		return DatabaseSnapshot{}, err
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	return provider.FindSnapshot(providerCtx, request)
}

func (s *Service) RetainSnapshot(ctx context.Context, definition RestoreSourceDefinition, request SnapshotCaptureRequest, id string) (DatabaseSnapshot, error) {
	if request.SourceResourceID != definition.DataResourceID {
		return DatabaseSnapshot{}, ErrInvalid
	}
	provider, err := s.snapshotProvider(definition)
	if err != nil {
		return DatabaseSnapshot{}, err
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	return provider.RetainSnapshot(providerCtx, request, id)
}

// Deletion continues when provisioning is disabled. Discovery never creates a
// snapshot, including when creation committed but its acknowledgement was lost.
func (s *Service) DeleteSnapshot(ctx context.Context, definition RestoreSourceDefinition, request SnapshotCaptureRequest, expectedID string) (DeleteResult, error) {
	if request.SourceResourceID != definition.DataResourceID {
		return DeleteResult{}, ErrInvalid
	}
	provider, err := s.snapshotProvider(definition)
	if err != nil {
		return DeleteResult{}, err
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	if expectedID == "" {
		actual, err := provider.FindSnapshot(providerCtx, request)
		if errors.Is(err, ErrNotFound) {
			return DeleteResult{Done: true}, nil
		}
		if err != nil {
			return DeleteResult{}, err
		}
		expectedID = actual.ProviderSnapshotID
	}
	// A missing owner-name match cannot prove absence of a known ID. The
	// adapter observes that exact ID and reauthenticates it before deletion.
	return provider.DeleteSnapshot(providerCtx, SnapshotDeleteRequest{ResourceID: request.ResourceID, ProviderSnapshotID: expectedID,
		SourceResourceID: request.SourceResourceID, PointInTime: request.PointInTime})
}

func (s *Service) snapshotProvider(definition RestoreSourceDefinition) (SnapshotProvider, error) {
	if s == nil || s.registry == nil {
		return nil, ErrUnavailable
	}
	if definition.BackendID == "" || definition.BackendFingerprint == "" || !validDataResourceID(definition.DataResourceID) {
		return nil, ErrInvalid
	}
	backend, err := s.registry.Resolve(definition.BackendID, definition.BackendFingerprint)
	if err != nil {
		return nil, err
	}
	provider, ok := backend.Provider.(SnapshotProvider)
	if !ok {
		return nil, ErrUnsupported
	}
	return provider, nil
}
