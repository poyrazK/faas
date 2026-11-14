package managedpostgres

import (
	"context"
	"errors"
)

// A negative response alone cannot settle a change: an earlier attempt may
// have applied, or a provider may still be processing it. Confirm the pinned
// resource and settled configuration before freeing the intent's pins.
func (s *Service) resolveResizeRejection(ctx, providerCtx context.Context, store ResizeStore, claimed Database, operation ResizeOperation, provider Provider, request UpdateRequest, code string, cause error) (Database, error) {
	observer, ok := provider.(UpdateObserver)
	if !ok {
		return Database{}, s.releaseProviderError(ctx, claimed, StateUpdating, cause)
	}
	observed, err := observer.ObserveUpdate(providerCtx, request)
	if err != nil {
		return Database{}, s.releaseProviderError(ctx, claimed, StateUpdating, errors.Join(cause, err))
	}
	if err := validateResizeObservation(operation, observed); err != nil || observed.Status != ProviderStatusReady {
		return Database{}, s.releaseProviderError(ctx, claimed, StateUpdating, errors.Join(cause, ErrUnavailable))
	}
	if observed.Spec == operation.TargetSpec() {
		return s.finishResize(ctx, store, claimed, operation, observed)
	}
	return s.failResize(ctx, store, claimed, operation, code)
}

func (s *Service) finishResize(ctx context.Context, store ResizeStore, claimed Database, operation ResizeOperation, observed ObservedDatabase) (Database, error) {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	result, err := store.FinishResize(finishCtx, claimed, operation, observed, s.now())
	if err != nil {
		return Database{}, s.releaseProviderError(ctx, claimed, StateUpdating, err)
	}
	return result, nil
}
