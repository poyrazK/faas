package apidserver

import (
	"github.com/onebox-faas/faas/pkg/apid/staticegress"
	"github.com/onebox-faas/faas/pkg/state"
)

// staticEgressInventoryStore joins the main store with its optional static
// egress inventory surface (ADR-20261128090000).
type staticEgressInventoryStore struct {
	state.Store
	state.StaticEgressAddressStore
}

// staticEgressInventory returns nil when the store keeps no inventory; the
// inventory routes then answer 503 and pins fall back to the legacy grants.
func staticEgressInventory(store state.Store) staticegress.Store {
	inventory, ok := store.(state.StaticEgressAddressStore)
	if !ok {
		return nil
	}
	return staticEgressInventoryStore{Store: store, StaticEgressAddressStore: inventory}
}
