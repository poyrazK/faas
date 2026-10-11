package managedpostgres

import (
	"context"
	"time"
)

// Identity discovery tolerates unfinished preparation and mutable configuration
// drift. Deleted is an independently observed provider deletion state plus
// absence of the exact active identity, never an acknowledgement or missing name.
type SnapshotCopyTargetIdentity struct {
	ProviderResourceID string
	CreatedAt          time.Time
	Deleted            bool
}

type SnapshotCopyTargetCleanupProvider interface {
	DiscoverSnapshotCopyTargetForCleanup(context.Context, RestoreSourceDefinition, SnapshotCopyTargetRequest) (SnapshotCopyTargetIdentity, error)
	DeleteSnapshotCopyTarget(context.Context, RestoreSourceDefinition, SnapshotCopyTargetRequest) (SnapshotCopyTargetIdentity, error)
	ObserveSnapshotCopyTargetDeletion(context.Context, RestoreSourceDefinition, SnapshotCopyTargetRequest) (SnapshotCopyTargetIdentity, error)
}

func (s *Service) DiscoverSnapshotCopyTargetForCleanup(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyTargetRequest) (SnapshotCopyTargetIdentity, error) {
	return s.snapshotCopyTargetCleanup(ctx, d, r, "discover")
}

// Cleanup remains available with new provisioning disabled. The worker must
// persist compensation intent and exact target identity before this mutation.
func (s *Service) DeleteSnapshotCopyTarget(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyTargetRequest) (SnapshotCopyTargetIdentity, error) {
	return s.snapshotCopyTargetCleanup(ctx, d, r, "delete")
}

func (s *Service) ObserveSnapshotCopyTargetDeletion(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyTargetRequest) (SnapshotCopyTargetIdentity, error) {
	return s.snapshotCopyTargetCleanup(ctx, d, r, "observe")
}

func (s *Service) snapshotCopyTargetCleanup(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyTargetRequest, action string) (SnapshotCopyTargetIdentity, error) {
	if r.Validate() != nil || r.Capture.Snapshot.SourceResourceID != d.DataResourceID ||
		r.ExpectedProviderResourceID != "" && r.ExpectedProviderResourceID == d.ProviderResourceID || action != "discover" && r.ExpectedProviderResourceID == "" {
		return SnapshotCopyTargetIdentity{}, ErrInvalid
	}
	b, err := s.checkpointBackend(d)
	if err != nil {
		return SnapshotCopyTargetIdentity{}, err
	}
	p, ok := b.Provider.(SnapshotCopyTargetCleanupProvider)
	if !ok {
		return SnapshotCopyTargetIdentity{}, ErrUnsupported
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	var actual SnapshotCopyTargetIdentity
	switch action {
	case "discover":
		actual, err = p.DiscoverSnapshotCopyTargetForCleanup(providerCtx, d, r)
	case "delete":
		actual, err = p.DeleteSnapshotCopyTarget(providerCtx, d, r)
	case "observe":
		actual, err = p.ObserveSnapshotCopyTargetDeletion(providerCtx, d, r)
	default:
		return SnapshotCopyTargetIdentity{}, ErrInvalid
	}
	if err != nil {
		return SnapshotCopyTargetIdentity{}, err
	}
	if !validDataResourceID(actual.ProviderResourceID) || actual.ProviderResourceID == d.ProviderResourceID || actual.ProviderResourceID == d.DataResourceID ||
		actual.ProviderResourceID == r.Capture.ExpectedTargetResourceID || actual.CreatedAt.Before(r.CaptureCreatedAt) || actual.CreatedAt.After(time.Now()) ||
		actual.CreatedAt.Nanosecond()%1000 != 0 || r.ExpectedProviderResourceID != "" &&
		(actual.ProviderResourceID != r.ExpectedProviderResourceID || !actual.CreatedAt.Equal(r.ExpectedCreatedAt)) {
		return SnapshotCopyTargetIdentity{}, ErrConflict
	}
	return actual, nil
}
