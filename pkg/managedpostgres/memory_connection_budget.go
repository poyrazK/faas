package managedpostgres

import (
	"context"
	"sort"
	"time"
)

// The standalone binding store has no runtime owner. It preserves binding
// visibility but cannot manufacture runtime counts from another memory store.
func (s *MemoryStore) ReadConnectionBudget(_ context.Context, accountID, databaseID string) (ConnectionBudgetSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.databases[databaseID]
	if !ok || d.AccountID != accountID || d.EnvironmentCloneOperationID != "" {
		return ConnectionBudgetSnapshot{}, ErrNotFound
	}
	out := ConnectionBudgetSnapshot{DatabaseID: d.ID, DatabaseName: d.Name, DatabaseState: string(d.State),
		BackendFingerprint: d.BackendFingerprint, Generation: d.ObservedGeneration, CheckedAt: time.Now().UTC(), InventoryUnavailable: true}
	for _, b := range s.bindings {
		if b.DatabaseID == databaseID && b.AccountID == accountID && b.State != BindingStateDeleted {
			out.Bindings = append(out.Bindings, connectionBudgetBinding{AppID: b.AppID, Scope: b.Scope, Access: string(b.Access), State: string(b.State), Rotation: b.RotationPreviousGeneration > 0})
		}
	}
	return out, nil
}

func (s *MemoryStore) AppConnectionBudgetDatabaseIDs(_ context.Context, accountID, appID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := map[string]bool{}
	for _, b := range s.bindings {
		d, ok := s.databases[b.DatabaseID]
		if ok && d.AccountID == accountID && d.EnvironmentCloneOperationID == "" && b.AccountID == accountID && b.AppID == appID && b.Access == CredentialDataAPI && b.State != BindingStateDeleted {
			ids[d.ID] = true
		}
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
