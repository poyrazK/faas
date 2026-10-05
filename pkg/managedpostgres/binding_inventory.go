package managedpostgres

import "context"

// AppBindingInventory is metadata from the binding and database catalog;
// provider identities and credential references are intentionally not read.
type AppBindingInventory struct {
	BindingID                                          string // Internal identity; never serialized in the inventory API.
	DatabaseName, Scope, EnvironmentKey, Access, State string
	CredentialGeneration                               int64
	RotationPending                                    bool
	RotationWakeID                                     string
}

type AppBindingInventoryStore interface {
	ListBindingsForApp(context.Context, string, string, string) ([]AppBindingInventory, error)
}

func (s *BindingService) ListForApp(ctx context.Context, accountID, appID, scope string) ([]AppBindingInventory, error) {
	if accountID == "" || appID == "" {
		return nil, ErrInvalid
	}
	store, ok := s.bindings.(AppBindingInventoryStore)
	if !ok {
		return nil, ErrUnsupported
	}
	return store.ListBindingsForApp(ctx, accountID, appID, scope)
}
