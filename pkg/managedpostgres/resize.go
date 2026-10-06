package managedpostgres

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
)

type ResizeState string

const (
	ResizePending   ResizeState = "pending"
	ResizeSucceeded ResizeState = "succeeded"
)

// ResizeOperation is an immutable request plus its completion receipt. The
// source snapshot is private control-plane evidence, never a public DTO.
type ResizeOperation struct {
	ID, AccountID, DatabaseID                                         string
	BackendID, BackendFingerprint, ProviderResourceID, DataResourceID string
	SourceSpec                                                        Spec
	TargetClass                                                       ServiceClass
	Generation                                                        int64
	State                                                             ResizeState
	LastErrorCode                                                     string
	CreatedAt, CompletedAt                                            time.Time
}

func (r ResizeOperation) TargetSpec() Spec {
	target := r.SourceSpec
	target.Class = r.TargetClass
	return target
}

type ResizeDatabaseRequest struct {
	AccountID, DatabaseID, RequestID string
	TargetClass                      ServiceClass
}

// ResizeStore serializes intent admission with other catalogue mutations and
// commits the operation receipt with the observed database configuration.
type ResizeStore interface {
	ReserveResize(context.Context, Database, ResizeOperation, time.Time) (ResizeOperation, error)
	GetResize(context.Context, string, string) (ResizeOperation, error)
	ActiveResize(context.Context, string, string) (ResizeOperation, error)
	FinishResize(context.Context, Database, ResizeOperation, ObservedDatabase, time.Time) (Database, error)
}

func validResizeRequestID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func validResizeOperation(r ResizeOperation) bool {
	return validResizeRequestID(r.ID) && r.AccountID != "" && r.DatabaseID != "" &&
		r.BackendID != "" && validSHA256(r.BackendFingerprint) && validOpaqueID(r.ProviderResourceID) &&
		validDataResourceID(r.DataResourceID) && r.SourceSpec.Validate() == nil && r.TargetSpec().Validate() == nil &&
		r.Generation > 1 && r.State == ResizePending && !r.CreatedAt.IsZero() && r.CompletedAt.IsZero()
}

func resizeSourceMatches(d Database, r ResizeOperation) bool {
	return d.ID == r.DatabaseID && d.AccountID == r.AccountID && d.BackendID == r.BackendID &&
		d.BackendFingerprint == r.BackendFingerprint && d.ProviderResourceID == r.ProviderResourceID &&
		d.DataResourceID == r.DataResourceID && d.Spec == r.SourceSpec && d.EnvironmentCloneOperationID == ""
}

func validateResizeObservation(r ResizeOperation, observed ObservedDatabase) error {
	if observed.ProviderResourceID != r.ProviderResourceID || observed.DataResourceID != r.DataResourceID {
		return ErrConflict
	}
	if observed.Spec != r.SourceSpec && observed.Spec != r.TargetSpec() {
		return ErrConflict
	}
	return nil
}

// Resize commits customer intent only. Recovery is owned by the existing
// reconciler and never depends on the lifetime of the HTTP request.
func (s *Service) Resize(ctx context.Context, request ResizeDatabaseRequest) (ResizeOperation, error) {
	if request.AccountID == "" || request.DatabaseID == "" || !validResizeRequestID(request.RequestID) {
		return ResizeOperation{}, ErrInvalid
	}
	store, ok := s.store.(ResizeStore)
	if !ok {
		return ResizeOperation{}, ErrUnsupported
	}
	existing, err := store.GetResize(ctx, request.AccountID, request.RequestID)
	if err == nil {
		if existing.DatabaseID != request.DatabaseID || existing.TargetClass != request.TargetClass {
			return ResizeOperation{}, ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return ResizeOperation{}, err
	}
	database, err := s.Get(ctx, request.AccountID, request.DatabaseID)
	if err != nil {
		return ResizeOperation{}, err
	}
	target := database.Spec
	target.Class = request.TargetClass
	if err := target.Validate(); err != nil {
		return ResizeOperation{}, err
	}
	if database.State != StateReady || database.DesiredGeneration != database.ObservedGeneration || database.DesiredGeneration == math.MaxInt64 {
		return ResizeOperation{}, ErrConflict
	}
	if database.EnvironmentCloneOperationID != "" || !validDataResourceID(database.DataResourceID) {
		return ResizeOperation{}, ErrUnsupported
	}
	if !s.provisioningEnabled() || !s.provisioningAllowed(ctx, request.AccountID) {
		return ResizeOperation{}, ErrUnavailable
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return ResizeOperation{}, err
	}
	if !backend.Capabilities.ClassResize {
		return ResizeOperation{}, ErrUnsupported
	}
	if err := backend.Capabilities.Supports(target); err != nil {
		return ResizeOperation{}, err
	}
	if s.admitResize != nil {
		if err := s.admitResize(ctx, request.AccountID, target); err != nil {
			return ResizeOperation{}, err
		}
	}
	if s.admit != nil {
		if err := s.admit(ctx, request.AccountID); err != nil {
			return ResizeOperation{}, err
		}
	}
	now := s.now()
	operation := ResizeOperation{ID: request.RequestID, AccountID: request.AccountID, DatabaseID: database.ID,
		BackendID: database.BackendID, BackendFingerprint: database.BackendFingerprint, ProviderResourceID: database.ProviderResourceID,
		DataResourceID: database.DataResourceID, SourceSpec: database.Spec, TargetClass: request.TargetClass,
		Generation: database.DesiredGeneration + 1, State: ResizePending, CreatedAt: now}
	return store.ReserveResize(ctx, database, operation, now)
}

func (s *Service) GetResize(ctx context.Context, account, database, id string) (ResizeOperation, error) {
	store, ok := s.store.(ResizeStore)
	if !ok {
		return ResizeOperation{}, ErrUnsupported
	}
	if !validResizeRequestID(id) {
		return ResizeOperation{}, ErrInvalid
	}
	operation, err := store.GetResize(ctx, account, id)
	if err == nil && operation.DatabaseID != database {
		return ResizeOperation{}, ErrNotFound
	}
	return operation, err
}

func (s *Service) reconcileResize(ctx context.Context, database Database) (Database, error) {
	store, ok := s.store.(ResizeStore)
	if !ok {
		return Database{}, ErrUnsupported
	}
	now := s.now()
	claimed, err := s.store.Claim(ctx, database.AccountID, database.ID, s.newLeaseToken(), StateUpdating, now, now.Add(s.leaseDuration))
	if err != nil {
		return Database{}, err
	}
	operation, err := store.ActiveResize(ctx, claimed.AccountID, claimed.ID)
	if err != nil || !resizeSourceMatches(claimed, operation) || claimed.DesiredGeneration != operation.Generation {
		return Database{}, s.releaseKnownError(ctx, claimed, StateUpdating, "resize_intent_mismatch", ErrConflict, time.Hour)
	}
	backend, err := s.registry.Resolve(operation.BackendID, operation.BackendFingerprint)
	if err != nil {
		return Database{}, s.releaseKnownError(ctx, claimed, StateUpdating, "backend_unavailable", ErrUnavailable, time.Hour)
	}
	if !backend.Capabilities.ClassResize || backend.Capabilities.Supports(operation.TargetSpec()) != nil {
		return Database{}, s.releaseKnownError(ctx, claimed, StateUpdating, "unsupported", ErrUnsupported, time.Hour)
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	observed, err := backend.Provider.Update(providerCtx, UpdateRequest{ResourceID: operation.ProviderResourceID,
		DataResourceID: operation.DataResourceID, PreviousSpec: operation.SourceSpec, Spec: operation.TargetSpec(),
		Generation: operation.Generation, IdempotencyKey: "resize-" + operation.ID})
	if err != nil {
		return Database{}, s.releaseProviderError(ctx, claimed, StateUpdating, err)
	}
	if err := validateResizeObservation(operation, observed); err != nil {
		return Database{}, s.releaseKnownError(ctx, claimed, StateUpdating, "resize_observation_mismatch", err, time.Hour)
	}
	if observed.Status == ProviderStatusReady && observed.Spec == operation.TargetSpec() {
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
		defer finishCancel()
		result, err := store.FinishResize(finishCtx, claimed, operation, observed, s.now())
		if err != nil {
			return Database{}, s.releaseProviderError(ctx, claimed, StateUpdating, err)
		}
		return result, nil
	}
	if observed.Status != ProviderStatusPending {
		return Database{}, s.releaseKnownError(ctx, claimed, StateUpdating, "resize_not_ready", ErrUnavailable, time.Hour)
	}
	if err := s.release(ctx, claimed.ID, claimed.LeaseToken, StateUpdating, "", s.pollInterval); err != nil {
		return Database{}, err
	}
	return s.store.Get(ctx, claimed.AccountID, claimed.ID)
}
