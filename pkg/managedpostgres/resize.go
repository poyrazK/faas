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
	// ResizeFailed ends a major upgrade the provider rejected for good
	// (ErrorCode); the database is back on its source spec. Only upgrades
	// fail.
	ResizeFailed ResizeState = "failed"
)

// ResizeOperation journals a class, storage or idle-policy request and its
// receipt. The source snapshot is private control-plane evidence, never a
// public DTO.
type ResizeOperation struct {
	ID, AccountID, DatabaseID                                         string
	BackendID, BackendFingerprint, ProviderResourceID, DataResourceID string
	SourceSpec                                                        Spec
	TargetClass                                                       ServiceClass
	// TargetStorageLimitBytes grows the storage limit; zero keeps it.
	TargetStorageLimitBytes int64
	PolicyChange            bool
	TargetScaleToZero       bool
	// TargetPostgresMajor upgrades the major in place (upgrade.go); zero
	// keeps it. An upgrade changes nothing else.
	TargetPostgresMajor int
	// ErrorCode is why a failed upgrade failed (ResizeFailed).
	ErrorCode              string
	Generation             int64
	State                  ResizeState
	LastErrorCode          string
	CreatedAt, CompletedAt time.Time
}

func (r ResizeOperation) TargetSpec() Spec {
	target := r.SourceSpec
	target.Class = r.TargetClass
	if r.PolicyChange {
		target.ScaleToZero = r.TargetScaleToZero
	}
	if r.TargetStorageLimitBytes != 0 {
		target.StorageLimitBytes = r.TargetStorageLimitBytes
	}
	if r.TargetPostgresMajor != 0 {
		target.PostgresMajor = r.TargetPostgresMajor
	}
	return target
}

// MajorUpgrade reports whether the change is an in-place major upgrade.
func (r ResizeOperation) MajorUpgrade() bool { return r.TargetPostgresMajor != 0 }

// ResizeDatabaseRequest changes the class, the storage limit or both. An
// empty class keeps the class; a zero or unchanged storage limit keeps the
// limit. Storage only grows: a volume cannot shrink under its data.
type ResizeDatabaseRequest struct {
	AccountID, DatabaseID, RequestID string
	TargetClass                      ServiceClass
	TargetStorageLimitBytes          int64
	// TargetPostgresMajor is set only by UpgradeMajor.
	TargetPostgresMajor int
}

// resizeTarget resolves a resize request against the source spec: the
// class it lands on, and the storage target to journal (zero: unchanged).
func resizeTarget(source Spec, request ResizeDatabaseRequest) (ServiceClass, int64, error) {
	class, storage := request.TargetClass, request.TargetStorageLimitBytes
	if class == "" {
		class = source.Class
	}
	if storage == source.StorageLimitBytes {
		storage = 0
	}
	if request.TargetClass == "" && request.TargetStorageLimitBytes == 0 || storage < 0 ||
		storage > 0 && (source.StorageLimitBytes <= 0 || storage < source.StorageLimitBytes) {
		return "", 0, ErrInvalid
	}
	return class, storage, nil
}

// ResizeStore serializes intent admission with other catalogue mutations and
// commits the operation receipt with the observed database configuration.
type ResizeStore interface {
	ReserveResize(context.Context, Database, ResizeOperation, time.Time) (ResizeOperation, error)
	GetResize(context.Context, string, string) (ResizeOperation, error)
	ActiveResize(context.Context, string, string) (ResizeOperation, error)
	FinishResize(context.Context, Database, ResizeOperation, ObservedDatabase, time.Time) (Database, error)
	// FailResize ends the claimed pending major upgrade as failed with a
	// stable code and puts the database back to ready on its source spec.
	FailResize(context.Context, Database, ResizeOperation, string, time.Time) (Database, error)
}

func validResizeRequestID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func validResizeOperation(r ResizeOperation) bool {
	return validResizeRequestID(r.ID) && r.AccountID != "" && r.DatabaseID != "" &&
		r.BackendID != "" && validSHA256(r.BackendFingerprint) && validOpaqueID(r.ProviderResourceID) &&
		validDataResourceID(r.DataResourceID) && r.SourceSpec.Validate() == nil && r.TargetSpec().Validate() == nil &&
		(r.PolicyChange && r.TargetClass == r.SourceSpec.Class && r.TargetStorageLimitBytes == 0 || !r.PolicyChange && !r.TargetScaleToZero) &&
		(r.TargetStorageLimitBytes == 0 || r.SourceSpec.StorageLimitBytes > 0 && r.TargetStorageLimitBytes > r.SourceSpec.StorageLimitBytes) &&
		(!r.MajorUpgrade() || !r.PolicyChange && r.TargetClass == r.SourceSpec.Class && r.TargetStorageLimitBytes == 0 &&
			r.TargetPostgresMajor > r.SourceSpec.PostgresMajor) &&
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
	if request.TargetPostgresMajor != 0 {
		return ResizeOperation{}, ErrInvalid
	}
	return s.reserveComputeChange(ctx, request, nil)
}

func (s *Service) reserveComputeChange(ctx context.Context, request ResizeDatabaseRequest, scaleToZero *bool) (ResizeOperation, error) {
	if request.AccountID == "" || request.DatabaseID == "" || !validResizeRequestID(request.RequestID) {
		return ResizeOperation{}, ErrInvalid
	}
	store, ok := s.store.(ResizeStore)
	if !ok {
		return ResizeOperation{}, ErrUnsupported
	}
	existing, err := store.GetResize(ctx, request.AccountID, request.RequestID)
	if err == nil {
		if !sameComputeChangeIntent(existing, request, scaleToZero) {
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
	now := s.now()
	operation := ResizeOperation{ID: request.RequestID, AccountID: request.AccountID, DatabaseID: database.ID,
		BackendID: database.BackendID, BackendFingerprint: database.BackendFingerprint, ProviderResourceID: database.ProviderResourceID,
		DataResourceID: database.DataResourceID, SourceSpec: database.Spec, TargetClass: database.Spec.Class,
		Generation: database.DesiredGeneration + 1, State: ResizePending, CreatedAt: now}
	switch {
	case scaleToZero == nil && request.TargetPostgresMajor != 0:
		if request.TargetClass != "" || request.TargetStorageLimitBytes != 0 || request.TargetPostgresMajor <= database.Spec.PostgresMajor {
			return ResizeOperation{}, ErrInvalid
		}
		operation.TargetPostgresMajor = request.TargetPostgresMajor
	case scaleToZero == nil:
		if operation.TargetClass, operation.TargetStorageLimitBytes, err = resizeTarget(database.Spec, request); err != nil {
			return ResizeOperation{}, err
		}
	default:
		operation.PolicyChange, operation.TargetScaleToZero = true, *scaleToZero
	}
	target := operation.TargetSpec()
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
		return ResizeOperation{}, ErrProvisioningClosed
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return ResizeOperation{}, err
	}
	if !computeChangeSupported(backend.Capabilities, operation) {
		return ResizeOperation{}, ErrUnsupported
	}
	if err := backend.Capabilities.Supports(target); err != nil {
		return ResizeOperation{}, err
	}
	if s.admitResize != nil {
		if err := s.admitResize(ctx, PolicySubject(ctx, request.AccountID), target); err != nil {
			return ResizeOperation{}, err
		}
	}
	if s.admit != nil && computeChangeRaisesCost(database.Spec, target) {
		if err := s.admit(ctx, PolicySubject(ctx, request.AccountID)); err != nil {
			return ResizeOperation{}, err
		}
	}
	return store.ReserveResize(ctx, database, operation, now)
}

// computeChangeRaisesCost reports whether target can consume more than source.
// Usage admission guards spend, so a smaller or equal class and turning
// scale-to-zero on stay available while usage is stale or at its guardrail:
// those are the changes that stop spend (ADR-623 amendment, hunt #9 H9-P201).
func computeChangeRaisesCost(source, target Spec) bool {
	return serviceClassRank(target.Class) > serviceClassRank(source.Class) || source.ScaleToZero && !target.ScaleToZero
}

// serviceClassRank orders the product classes by their compute promise; an
// unknown class ranks highest so it never skips admission.
func serviceClassRank(class ServiceClass) int {
	switch class {
	case ClassDevelopment:
		return 0
	case ClassBurstable:
		return 1
	case ClassProduction:
		return 2
	default:
		return 3
	}
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
	if err == nil && (operation.DatabaseID != database || operation.PolicyChange || operation.MajorUpgrade()) {
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
	if !computeChangeSupported(backend.Capabilities, operation) || backend.Capabilities.Supports(operation.TargetSpec()) != nil {
		return Database{}, s.releaseKnownError(ctx, claimed, StateUpdating, "unsupported", ErrUnsupported, time.Hour)
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	observed, err := backend.Provider.Update(providerCtx, UpdateRequest{ResourceID: operation.ProviderResourceID,
		DataResourceID: operation.DataResourceID, PreviousSpec: operation.SourceSpec, Spec: operation.TargetSpec(),
		Generation: operation.Generation, IdempotencyKey: "resize-" + operation.ID})
	if err != nil {
		var rejected *ChangeRejectedError
		if operation.MajorUpgrade() && errors.As(err, &rejected) && validRejectionCode(rejected.Code) {
			return s.failResize(ctx, store, claimed, operation, rejected.Code)
		}
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

// failResize ends a major upgrade the provider rejected for good: the
// database is back to ready on its source spec, and the change records why.
func (s *Service) failResize(ctx context.Context, store ResizeStore, claimed Database, operation ResizeOperation, code string) (Database, error) {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	result, err := store.FailResize(finishCtx, claimed, operation, code, s.now())
	if err != nil {
		return Database{}, s.releaseProviderError(ctx, claimed, StateUpdating, err)
	}
	return result, nil
}

// validRejectionCode matches a failed change's stable reason (the journal's CHECK).
func validRejectionCode(code string) bool {
	if code == "" || len(code) > 64 {
		return false
	}
	for _, c := range code {
		if (c < 'a' || c > 'z') && c != '_' {
			return false
		}
	}
	return true
}

// computeChangeSupported: an upgrade needs MajorUpgrade, a storage target
// needs StorageResize, and anything but a storage-only change needs
// ClassResize.
func computeChangeSupported(c Capabilities, operation ResizeOperation) bool {
	if operation.PolicyChange {
		return c.ScaleToZeroUpdate
	}
	if operation.MajorUpgrade() {
		return c.MajorUpgrade
	}
	storage := operation.TargetStorageLimitBytes != 0
	if storage && !c.StorageResize {
		return false
	}
	return c.ClassResize || storage && operation.TargetClass == operation.SourceSpec.Class
}

func sameComputeChangeRequest(a, b ResizeOperation) bool {
	if a.DatabaseID != b.DatabaseID || a.PolicyChange != b.PolicyChange {
		return false
	}
	if a.PolicyChange {
		return a.TargetScaleToZero == b.TargetScaleToZero
	}
	return a.TargetClass == b.TargetClass && a.TargetStorageLimitBytes == b.TargetStorageLimitBytes && a.TargetPostgresMajor == b.TargetPostgresMajor
}

// sameComputeChangeIntent reports whether a replayed request asks for what
// the journaled operation already holds.
func sameComputeChangeIntent(existing ResizeOperation, request ResizeDatabaseRequest, scaleToZero *bool) bool {
	if existing.DatabaseID != request.DatabaseID || existing.PolicyChange != (scaleToZero != nil) {
		return false
	}
	if scaleToZero != nil {
		return existing.TargetScaleToZero == *scaleToZero
	}
	if existing.MajorUpgrade() || request.TargetPostgresMajor != 0 {
		return existing.TargetPostgresMajor == request.TargetPostgresMajor && request.TargetClass == "" && request.TargetStorageLimitBytes == 0
	}
	class, storage, err := resizeTarget(existing.SourceSpec, request)
	return err == nil && existing.TargetClass == class && existing.TargetStorageLimitBytes == storage
}
