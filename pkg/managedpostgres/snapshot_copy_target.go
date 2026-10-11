package managedpostgres

import (
	"context"
	"time"
)

// Preparing an independent target is only the first part of materialization.
// It does not import data, qualify SQL isolation or authorize publication.
// Capture must identify an independently observed, adopted native fork.
type SnapshotCopyTargetRequest struct {
	ResourceID, ExpectedProviderResourceID string
	Capture                                SnapshotRestoreRequest
	SnapshotCreatedAt, CaptureCreatedAt    time.Time
	ExpectedCreatedAt                      time.Time
}

type SnapshotCopyTargetObservation struct {
	ProviderResourceID string
	CreatedAt          time.Time
	Spec               Spec
	Prepared           bool
}

type SnapshotCopyTargetProvider interface {
	PrepareSnapshotCopyTarget(context.Context, RestoreSourceDefinition, SnapshotCopyTargetRequest) (SnapshotCopyTargetObservation, error)
	// Discovery never repeats a non-idempotent project creation request.
	FindSnapshotCopyTarget(context.Context, RestoreSourceDefinition, SnapshotCopyTargetRequest) (SnapshotCopyTargetObservation, error)
}

// This is an independent database charge. Admission does not depend on the
// live source's PITR window or account for target usage as source branch usage.
func (s *Service) AdmitSnapshotCopyTargetReservation(ctx context.Context, accountID string, definition RestoreSourceDefinition) (int, error) {
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
	backend, err := s.checkpointBackend(definition)
	if err != nil {
		return 0, err
	}
	if _, ok := backend.Provider.(SnapshotCopyTargetProvider); !ok {
		return 0, ErrUnsupported
	}
	return s.reservationLimit(ctx, accountID)
}

func (s *Service) PrepareSnapshotCopyTarget(ctx context.Context, accountID string, definition RestoreSourceDefinition, request SnapshotCopyTargetRequest) (SnapshotCopyTargetObservation, error) {
	if s == nil || !s.provisioningEnabled() || !s.provisioningAllowed(ctx, accountID) {
		return SnapshotCopyTargetObservation{}, ErrUnavailable
	}
	if accountID == "" {
		return SnapshotCopyTargetObservation{}, ErrInvalid
	}
	return s.snapshotCopyTarget(ctx, definition, request, true)
}

func (s *Service) FindSnapshotCopyTarget(ctx context.Context, definition RestoreSourceDefinition, request SnapshotCopyTargetRequest) (SnapshotCopyTargetObservation, error) {
	return s.snapshotCopyTarget(ctx, definition, request, false)
}

func (s *Service) snapshotCopyTarget(ctx context.Context, definition RestoreSourceDefinition, request SnapshotCopyTargetRequest, create bool) (SnapshotCopyTargetObservation, error) {
	if request.Validate() != nil || request.Capture.Snapshot.SourceResourceID != definition.DataResourceID ||
		request.ExpectedProviderResourceID != "" && request.ExpectedProviderResourceID == definition.ProviderResourceID {
		return SnapshotCopyTargetObservation{}, ErrInvalid
	}
	backend, err := s.checkpointBackend(definition)
	if err != nil {
		return SnapshotCopyTargetObservation{}, err
	}
	provider, ok := backend.Provider.(SnapshotCopyTargetProvider)
	if !ok {
		return SnapshotCopyTargetObservation{}, ErrUnsupported
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	var actual SnapshotCopyTargetObservation
	if create {
		actual, err = provider.PrepareSnapshotCopyTarget(providerCtx, definition, request)
	} else {
		actual, err = provider.FindSnapshotCopyTarget(providerCtx, definition, request)
	}
	if err != nil {
		return SnapshotCopyTargetObservation{}, err
	}
	if !validDataResourceID(actual.ProviderResourceID) || actual.ProviderResourceID == definition.ProviderResourceID ||
		actual.ProviderResourceID == definition.DataResourceID || actual.ProviderResourceID == request.Capture.ExpectedTargetResourceID ||
		actual.CreatedAt.Before(request.CaptureCreatedAt) || actual.CreatedAt.After(time.Now()) || actual.CreatedAt.Nanosecond()%1000 != 0 ||
		actual.Spec != definition.Spec || request.ExpectedProviderResourceID != "" &&
		(actual.ProviderResourceID != request.ExpectedProviderResourceID || !actual.CreatedAt.Equal(request.ExpectedCreatedAt)) {
		return SnapshotCopyTargetObservation{}, ErrConflict
	}
	return actual, nil
}

func (r SnapshotCopyTargetRequest) Validate() error {
	if !validOpaqueID(r.ResourceID) || r.ResourceID == r.Capture.ResourceID || r.Capture.Validate() != nil || r.Capture.ExpectedTargetResourceID == "" ||
		r.SnapshotCreatedAt.Before(r.Capture.Snapshot.PointInTime) || r.CaptureCreatedAt.Before(r.SnapshotCreatedAt) ||
		r.CaptureCreatedAt.After(time.Now()) || r.SnapshotCreatedAt.Nanosecond()%1000 != 0 || r.CaptureCreatedAt.Nanosecond()%1000 != 0 ||
		(r.ExpectedProviderResourceID == "") != r.ExpectedCreatedAt.IsZero() || r.ExpectedProviderResourceID != "" &&
		(!validDataResourceID(r.ExpectedProviderResourceID) || r.ExpectedProviderResourceID == r.Capture.ExpectedTargetResourceID ||
			r.ExpectedProviderResourceID == r.Capture.Snapshot.SourceResourceID || r.ExpectedCreatedAt.Before(r.CaptureCreatedAt) ||
			r.ExpectedCreatedAt.After(time.Now()) || r.ExpectedCreatedAt.Nanosecond()%1000 != 0) {
		return ErrInvalid
	}
	return nil
}
