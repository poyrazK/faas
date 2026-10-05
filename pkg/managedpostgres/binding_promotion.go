// adr: 429 — memory catalogs hold their lock through the traffic write;
// production catalogs participate in the shared PostgreSQL revision fence.
package managedpostgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

type PromotionFence struct {
	revision string
	backend  any
}
type promotionFenceStore interface {
	ReadPromotionFence(context.Context, string, string) (PromotionFence, error)
	GuardPromotion(context.Context, string, string, PromotionFence, any, func(context.Context) error) error
}

func (s *BindingService) ReadPromotionFence(ctx context.Context, accountID, appID string) (PromotionFence, error) {
	store, ok := s.bindings.(promotionFenceStore)
	if !ok {
		return PromotionFence{}, ErrUnsupported
	}
	return store.ReadPromotionFence(ctx, accountID, appID)
}
func (s *BindingService) GuardPromotion(ctx context.Context, accountID, appID string, fence PromotionFence, backend any, fn func(context.Context) error) error {
	store, ok := s.bindings.(promotionFenceStore)
	if !ok {
		return ErrUnsupported
	}
	return store.GuardPromotion(ctx, accountID, appID, fence, backend, fn)
}
func (s *PostgresStore) ReadPromotionFence(context.Context, string, string) (PromotionFence, error) {
	return PromotionFence{backend: s.pool}, nil
}
func (s *PostgresStore) GuardPromotion(ctx context.Context, _, _ string, fence PromotionFence, backend any, fn func(context.Context) error) error {
	if fence.backend != s.pool || backend != s.pool {
		return ErrUnsupported
	}
	return fn(ctx)
}
func (s *MemoryStore) ReadPromotionFence(_ context.Context, accountID, appID string) (PromotionFence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	revision, err := s.promotionRevisionLocked(accountID, appID)
	return PromotionFence{revision: revision, backend: s}, err
}
func (s *MemoryStore) GuardPromotion(ctx context.Context, accountID, appID string, fence PromotionFence, _ any, fn func(context.Context) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	revision, err := s.promotionRevisionLocked(accountID, appID)
	if err != nil {
		return err
	}
	if fence.backend != s || revision != fence.revision {
		return ErrConflict
	}
	return fn(ctx)
}
func (s *MemoryStore) promotionRevisionLocked(accountID, appID string) (string, error) {
	facts := map[string]any{}
	for id, binding := range s.bindings {
		if binding.AccountID == accountID && binding.AppID == appID {
			facts["binding/"+id] = []any{binding.DatabaseID, binding.Scope, binding.EnvironmentKey, binding.Access, binding.State, binding.CredentialGeneration, binding.CredentialRef, binding.RotationPreviousGeneration, binding.RotationWakeID, binding.RotationCleanupReady}
			database := s.databases[binding.DatabaseID]
			facts["database/"+binding.DatabaseID] = []any{database.AccountID, database.Name, database.State}
		}
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		return "", fmt.Errorf("managedpostgres: capture promotion revision: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
