package managedpostgres

import "context"

type PromotionSelection struct {
	AccountID, AppID string
	Fence            PromotionFence
}

type promotionBatchStore interface {
	GuardPromotions(context.Context, []PromotionSelection, any, func(context.Context) error) error
}

func (s *BindingService) GuardPromotions(ctx context.Context, selections []PromotionSelection, backend any, fn func(context.Context) error) error {
	store, ok := s.bindings.(promotionBatchStore)
	if !ok {
		return ErrUnsupported
	}
	return store.GuardPromotions(ctx, selections, backend, fn)
}

func (s *PostgresStore) GuardPromotions(ctx context.Context, selections []PromotionSelection, backend any, fn func(context.Context) error) error {
	if backend != s.pool {
		return ErrUnsupported
	}
	for _, selection := range selections {
		if selection.Fence.backend != s.pool {
			return ErrUnsupported
		}
	}
	return fn(ctx)
}

func (s *MemoryStore) GuardPromotions(ctx context.Context, selections []PromotionSelection, _ any, fn func(context.Context) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, selection := range selections {
		revision, err := s.promotionRevisionLocked(selection.AccountID, selection.AppID)
		if err != nil {
			return err
		}
		if selection.Fence.backend != s || revision != selection.Fence.revision {
			return ErrConflict
		}
	}
	return fn(ctx)
}
