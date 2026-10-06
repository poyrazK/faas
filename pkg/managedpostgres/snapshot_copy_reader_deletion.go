package managedpostgres

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Deletion consumes the committed reader pin and cleanup dispatch time. A
// lost endpoint acknowledgement can recover only through qualified capture
// deletion and exact endpoint absence; autosuspend is not deletion evidence.
type SnapshotCopyReaderDeletionRequest struct {
	Reader              SnapshotCopyReaderRequest
	RequestedAt         time.Time
	OperationIDs        []string
	CaptureOperationIDs []string
}

type SnapshotCopyReaderDeletionObservation struct {
	EndpointID          string
	CreatedAt           time.Time
	OperationIDs        []string
	CaptureOperationIDs []string
	Done                bool
}

type SnapshotCopyReaderDeletionProvider interface {
	DeleteSnapshotCopyReader(context.Context, RestoreSourceDefinition, SnapshotCopyReaderDeletionRequest) (SnapshotCopyReaderDeletionObservation, error)
	ObserveSnapshotCopyReaderDeletion(context.Context, RestoreSourceDefinition, SnapshotCopyReaderDeletionRequest) (SnapshotCopyReaderDeletionObservation, error)
}

func (r SnapshotCopyReaderDeletionRequest) Validate() error {
	if r.Reader.Validate() != nil || r.Reader.ExpectedEndpointID == "" || r.RequestedAt.IsZero() ||
		r.RequestedAt.Before(r.Reader.ExpectedCreatedAt) || r.RequestedAt.After(time.Now()) || r.RequestedAt.Nanosecond()%1000 != 0 {
		return ErrInvalid
	}
	for _, ids := range [][]string{r.OperationIDs, r.CaptureOperationIDs} {
		if len(ids) > api.PostgresCopyReaderMaxOperations {
			return ErrQuotaExceeded
		}
		if _, err := snapshotDeletionOperationIDs(ids); err != nil {
			return err
		}
	}
	return nil
}

// Caller commits cleanup intent before dispatch. Recovery is read-only, and
// remains available when creation is disabled. This seam owns no state itself.
func (s *Service) DeleteSnapshotCopyReader(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderDeletionRequest) (SnapshotCopyReaderDeletionObservation, error) {
	return s.snapshotCopyReaderDeletion(ctx, d, r, true)
}

func (s *Service) ObserveSnapshotCopyReaderDeletion(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderDeletionRequest) (SnapshotCopyReaderDeletionObservation, error) {
	return s.snapshotCopyReaderDeletion(ctx, d, r, false)
}

func (s *Service) snapshotCopyReaderDeletion(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderDeletionRequest, mutate bool) (SnapshotCopyReaderDeletionObservation, error) {
	if err := r.Validate(); err != nil {
		return SnapshotCopyReaderDeletionObservation{}, err
	}
	if r.Reader.Capture.Snapshot.SourceResourceID != d.DataResourceID || r.Reader.ExpectedEndpointID == d.ProviderResourceID {
		return SnapshotCopyReaderDeletionObservation{}, ErrInvalid
	}
	b, err := s.checkpointBackend(d)
	if err != nil {
		return SnapshotCopyReaderDeletionObservation{}, err
	}
	p, ok := b.Provider.(SnapshotCopyReaderDeletionProvider)
	if !ok {
		return SnapshotCopyReaderDeletionObservation{}, ErrUnsupported
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	var actual SnapshotCopyReaderDeletionObservation
	if mutate {
		actual, err = p.DeleteSnapshotCopyReader(providerCtx, d, r)
	} else {
		actual, err = p.ObserveSnapshotCopyReaderDeletion(providerCtx, d, r)
	}
	if err != nil {
		return SnapshotCopyReaderDeletionObservation{}, err
	}
	if actual.EndpointID != r.Reader.ExpectedEndpointID || !actual.CreatedAt.Equal(r.Reader.ExpectedCreatedAt) ||
		len(actual.OperationIDs) > api.PostgresCopyReaderMaxOperations || len(actual.CaptureOperationIDs) > api.PostgresCopyReaderMaxOperations {
		return SnapshotCopyReaderDeletionObservation{}, ErrConflict
	}
	for _, pair := range [][2][]string{{r.OperationIDs, actual.OperationIDs}, {r.CaptureOperationIDs, actual.CaptureOperationIDs}} {
		got, err := snapshotDeletionOperationIDs(pair[1])
		want, _ := snapshotDeletionOperationIDs(pair[0])
		if err != nil || len(want) > 0 && !slices.Equal(got, want) {
			return SnapshotCopyReaderDeletionObservation{}, ErrConflict
		}
	}
	if actual.Done && len(actual.OperationIDs) == 0 && len(actual.CaptureOperationIDs) == 0 {
		return SnapshotCopyReaderDeletionObservation{}, ErrConflict
	}
	actual.OperationIDs, _ = snapshotDeletionOperationIDs(actual.OperationIDs)
	actual.CaptureOperationIDs, _ = snapshotDeletionOperationIDs(actual.CaptureOperationIDs)
	return actual, nil
}
