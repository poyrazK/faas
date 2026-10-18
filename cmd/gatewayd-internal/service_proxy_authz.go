package main

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/servicebinding"
	"github.com/onebox-faas/faas/pkg/state"
)

// Keep the gateway wiring and its regression tests on the shared authority.
func newServiceProxyAuthorizer(store state.Store) gateway.ServiceProxyAuthorizer {
	return servicebinding.NewAuthorizer(store)
}

func isAppID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil
}
