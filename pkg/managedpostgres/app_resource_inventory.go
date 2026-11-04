// adr: 20261103222927
package managedpostgres

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ResourceInventory reads catalog metadata only; no provider calls or credentials.
func (s *BindingService) ResourceInventory(ctx context.Context, q state.AppResourceInventoryRequest) (state.AppResourceInventoryPage, error) {
	reader, ok := s.bindings.(state.AppResourceInventoryReader)
	if !ok {
		return state.AppResourceInventoryPage{}, ErrUnsupported
	}
	return reader.ListAppResourceInventory(ctx, q)
}

func (s *PostgresStore) ListAppResourceInventory(ctx context.Context, q state.AppResourceInventoryRequest) (state.AppResourceInventoryPage, error) {
	if q.Kind != "databases" {
		return state.AppResourceInventoryPage{}, ErrInvalid
	}
	return state.NewPgStore(s.pool).ListAppResourceInventory(ctx, q)
}

func (s *MemoryStore) ListAppResourceInventory(ctx context.Context, q state.AppResourceInventoryRequest) (state.AppResourceInventoryPage, error) {
	if err := q.Validate(); err != nil {
		return state.AppResourceInventoryPage{}, err
	}
	if q.Kind != "databases" {
		return state.AppResourceInventoryPage{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return state.AppResourceInventoryPage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []api.AppResourceInventoryItem{}
	for _, b := range s.bindings {
		d, ok := s.databases[b.DatabaseID]
		if !ok || d.AccountID != b.DatabaseNamespace() || d.State == StateDeleted || b.AccountID != q.AccountID || b.AppID != q.AppID || b.State == BindingStateDeleted || b.Scope != q.Scope {
			continue
		}
		items = append(items, api.AppResourceInventoryItem{ID: d.ID, AppID: q.AppID, Scope: q.Scope, Name: d.Name, Region: d.Spec.Region, State: string(d.State), PostgresMajor: d.Spec.PostgresMajor,
			Binding: &api.AppResourceInventoryBinding{ID: b.ID, DatabaseID: d.ID, AppID: q.AppID, Scope: q.Scope, Access: string(b.Access), State: string(b.State), EnvironmentKey: b.EnvironmentKey, RotationPending: b.RotationPreviousGeneration > 0},
		})
	}
	return state.PageAppResourceInventory(items, q), nil
}
