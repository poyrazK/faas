package managedpostgres

import (
	"context"
	"slices"
	"time"
)

// Selectors and times come from the durable capture/fork receipts. Cleanup
// requires a known physical target; absence of an unknown name is not proof.
type SnapshotRestoreDeletionRequest struct {
	Restore                            SnapshotRestoreRequest
	SnapshotCreatedAt, TargetCreatedAt time.Time
	OperationIDs                       []string
}

type SnapshotRestoreDeletionObservation struct {
	ProviderResourceID string
	OperationIDs       []string
	Done               bool
}

type SnapshotRestoreDeletionProvider interface {
	DeleteSnapshotRestore(context.Context, RestoreSourceDefinition, SnapshotRestoreDeletionRequest) (SnapshotRestoreDeletionObservation, error)
	ObserveSnapshotRestoreDeletion(context.Context, RestoreSourceDefinition, SnapshotRestoreDeletionRequest) (SnapshotRestoreDeletionObservation, error)
}

// Owned cleanup remains available when new provisioning is disabled. The
// worker must commit compensation intent before mutation and independently
// observe terminal operations and physical absence before retiring its receipt.
func (s *Service) DeleteSnapshotRestore(ctx context.Context, d RestoreSourceDefinition, r SnapshotRestoreDeletionRequest) (SnapshotRestoreDeletionObservation, error) {
	return s.snapshotRestoreDeletion(ctx, d, r, true)
}

func (s *Service) ObserveSnapshotRestoreDeletion(ctx context.Context, d RestoreSourceDefinition, r SnapshotRestoreDeletionRequest) (SnapshotRestoreDeletionObservation, error) {
	return s.snapshotRestoreDeletion(ctx, d, r, false)
}

func (r SnapshotRestoreDeletionRequest) Validate() error {
	if r.Restore.Validate() != nil || r.Restore.ExpectedTargetResourceID == "" || r.SnapshotCreatedAt.IsZero() ||
		r.SnapshotCreatedAt.Before(r.Restore.Snapshot.PointInTime) || r.TargetCreatedAt.Before(r.SnapshotCreatedAt) ||
		r.TargetCreatedAt.After(time.Now()) || r.SnapshotCreatedAt.Nanosecond()%1000 != 0 || r.TargetCreatedAt.Nanosecond()%1000 != 0 {
		return ErrInvalid
	}
	_, err := snapshotDeletionOperationIDs(r.OperationIDs)
	return err
}

func snapshotDeletionOperationIDs(ids []string) ([]string, error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	for i, id := range ids {
		if !validOpaqueID(id) || i > 0 && ids[i-1] == id {
			return nil, ErrInvalid
		}
	}
	return ids, nil
}

func (s *Service) snapshotRestoreDeletion(ctx context.Context, d RestoreSourceDefinition, r SnapshotRestoreDeletionRequest, mutate bool) (SnapshotRestoreDeletionObservation, error) {
	if r.Validate() != nil || r.Restore.Snapshot.SourceResourceID != d.DataResourceID {
		return SnapshotRestoreDeletionObservation{}, ErrInvalid
	}
	b, err := s.checkpointBackend(d)
	if err != nil {
		return SnapshotRestoreDeletionObservation{}, err
	}
	p, ok := b.Provider.(SnapshotRestoreDeletionProvider)
	if !ok {
		return SnapshotRestoreDeletionObservation{}, ErrUnsupported
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	var actual SnapshotRestoreDeletionObservation
	if mutate {
		actual, err = p.DeleteSnapshotRestore(providerCtx, d, r)
	} else {
		actual, err = p.ObserveSnapshotRestoreDeletion(providerCtx, d, r)
	}
	if err != nil {
		return SnapshotRestoreDeletionObservation{}, err
	}
	ids, err := snapshotDeletionOperationIDs(actual.OperationIDs)
	expected, _ := snapshotDeletionOperationIDs(r.OperationIDs)
	if err != nil || actual.ProviderResourceID != r.Restore.ExpectedTargetResourceID || actual.Done && len(ids) == 0 || len(expected) > 0 && !slices.Equal(ids, expected) {
		return SnapshotRestoreDeletionObservation{}, ErrConflict
	}
	actual.OperationIDs = ids
	return actual, nil
}
