package main

import (
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/servicebinding"
	"github.com/onebox-faas/faas/pkg/state"
)

func newServiceAliasAllowed(store state.Store) gateway.ServiceAliasAllowed {
	return servicebinding.NewAliasAllowed(store)
}
