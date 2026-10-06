package managedpostgres

import (
	"context"
	"sort"
)

var _ AppBindingInventoryStore = (*MemoryStore)(nil)

func (s *MemoryStore) ListBindingsForApp(_ context.Context, accountID, appID, scope string) ([]AppBindingInventory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]AppBindingInventory, 0)
	for _, b := range s.bindings {
		d, ok := s.databases[b.DatabaseID]
		if !ok || d.AccountID != accountID || d.State == StateDeleted || b.AccountID != accountID ||
			b.AppID != appID || b.State == BindingStateDeleted || scope != "" && b.Scope != scope {
			continue
		}
		items = append(items, AppBindingInventory{
			BindingID:    b.ID,
			DatabaseName: d.Name, Scope: b.Scope, EnvironmentKey: b.EnvironmentKey,
			Access: string(b.Access), State: string(b.State), CredentialGeneration: b.CredentialGeneration,
			RotationPending: b.RotationPreviousGeneration > 0,
			RotationWakeID:  b.RotationWakeID,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].DatabaseName+items[i].EnvironmentKey+items[i].Scope < items[j].DatabaseName+items[j].EnvironmentKey+items[j].Scope
	})
	return items, nil
}
