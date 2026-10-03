package managedpostgres

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A reader is temporary compute on the exact owned native capture. Its owner
// and first dispatch must be committed under the clone lease before creation.
// It is neither an independent stage database nor a complete-copy receipt.
type SnapshotCopyReaderRequest struct {
	ResourceID, ExpectedEndpointID                   string
	Capture                                          SnapshotRestoreRequest
	SnapshotCreatedAt, CaptureCreatedAt, RequestedAt time.Time
	ExpectedCreatedAt                                time.Time
}

type SnapshotCopyReaderObservation struct {
	EndpointID string
	CreatedAt  time.Time
	Available  bool
}

type SnapshotCopyReaderProvider interface {
	PrepareSnapshotCopyReader(context.Context, RestoreSourceDefinition, SnapshotCopyReaderRequest) (SnapshotCopyReaderObservation, error)
	// Discovery must never repeat an uncertain non-idempotent creation.
	FindSnapshotCopyReader(context.Context, RestoreSourceDefinition, SnapshotCopyReaderRequest) (SnapshotCopyReaderObservation, error)
}

// This bounds temporary compute ownership; the native capture retains its
// database charge. Plan billing and compute metering are separate concerns.
func (s *Service) AdmitSnapshotCopyReaderReservation(ctx context.Context, accountID string, d RestoreSourceDefinition) (int, error) {
	if s == nil || !s.provisioningEnabled() || !s.provisioningAllowed(ctx, accountID) {
		return 0, ErrUnavailable
	}
	if accountID == "" {
		return 0, ErrInvalid
	}
	if s.admit != nil {
		if err := s.admit(ctx, accountID); err != nil {
			return 0, err
		}
	}
	b, err := s.checkpointBackend(d)
	if err != nil {
		return 0, err
	}
	if _, ok := b.Provider.(SnapshotCopyReaderProvider); !ok {
		return 0, ErrUnsupported
	}
	if _, ok := b.Provider.(SnapshotCopyReaderDeletionProvider); !ok {
		return 0, ErrUnsupported
	}
	return api.PostgresCopyReadersPerAccountMax, nil
}

func (r SnapshotCopyReaderRequest) Validate() error {
	if !validOpaqueID(r.ResourceID) || r.Capture.Validate() != nil || r.Capture.ExpectedTargetResourceID == "" ||
		r.SnapshotCreatedAt.IsZero() || r.SnapshotCreatedAt.Before(r.Capture.Snapshot.PointInTime) || r.CaptureCreatedAt.Before(r.SnapshotCreatedAt) ||
		r.RequestedAt.IsZero() || r.RequestedAt.Before(r.CaptureCreatedAt) || r.RequestedAt.After(time.Now()) ||
		r.SnapshotCreatedAt.Nanosecond()%1000 != 0 || r.CaptureCreatedAt.Nanosecond()%1000 != 0 || r.RequestedAt.Nanosecond()%1000 != 0 ||
		(r.ExpectedEndpointID == "") != r.ExpectedCreatedAt.IsZero() || r.ExpectedEndpointID != "" &&
		(!validOpaqueID(r.ExpectedEndpointID) || r.ExpectedEndpointID == r.Capture.ExpectedTargetResourceID ||
			r.ExpectedEndpointID == r.Capture.Snapshot.SourceResourceID || r.ExpectedCreatedAt.Before(r.RequestedAt) ||
			r.ExpectedCreatedAt.After(time.Now()) || r.ExpectedCreatedAt.Nanosecond()%1000 != 0) {
		return ErrInvalid
	}
	return nil
}

func (s *Service) PrepareSnapshotCopyReader(ctx context.Context, accountID string, d RestoreSourceDefinition, r SnapshotCopyReaderRequest) (SnapshotCopyReaderObservation, error) {
	if s == nil || !s.provisioningEnabled() || !s.provisioningAllowed(ctx, accountID) {
		return SnapshotCopyReaderObservation{}, ErrUnavailable
	}
	if accountID == "" {
		return SnapshotCopyReaderObservation{}, ErrInvalid
	}
	if s.admit != nil {
		if err := s.admit(ctx, accountID); err != nil {
			return SnapshotCopyReaderObservation{}, err
		}
	}
	return s.snapshotCopyReader(ctx, d, r, true)
}

func (s *Service) FindSnapshotCopyReader(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderRequest) (SnapshotCopyReaderObservation, error) {
	return s.snapshotCopyReader(ctx, d, r, false)
}

func (s *Service) snapshotCopyReader(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderRequest, create bool) (SnapshotCopyReaderObservation, error) {
	if r.Validate() != nil || r.Capture.Snapshot.SourceResourceID != d.DataResourceID || r.ExpectedEndpointID == d.ProviderResourceID {
		return SnapshotCopyReaderObservation{}, ErrInvalid
	}
	backend, err := s.checkpointBackend(d)
	if err != nil {
		return SnapshotCopyReaderObservation{}, err
	}
	provider, ok := backend.Provider.(SnapshotCopyReaderProvider)
	if !ok {
		return SnapshotCopyReaderObservation{}, ErrUnsupported
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	var actual SnapshotCopyReaderObservation
	if create {
		actual, err = provider.PrepareSnapshotCopyReader(providerCtx, d, r)
	} else {
		actual, err = provider.FindSnapshotCopyReader(providerCtx, d, r)
	}
	if err != nil {
		return SnapshotCopyReaderObservation{}, err
	}
	if !validOpaqueID(actual.EndpointID) || actual.EndpointID == d.ProviderResourceID || actual.EndpointID == d.DataResourceID ||
		actual.EndpointID == r.Capture.ExpectedTargetResourceID || actual.CreatedAt.IsZero() || actual.CreatedAt.Before(r.RequestedAt) ||
		actual.CreatedAt.After(time.Now()) || actual.CreatedAt.Nanosecond()%1000 != 0 || r.ExpectedEndpointID != "" &&
		(actual.EndpointID != r.ExpectedEndpointID || !actual.CreatedAt.Equal(r.ExpectedCreatedAt)) {
		return SnapshotCopyReaderObservation{}, ErrConflict
	}
	return actual, nil
}
