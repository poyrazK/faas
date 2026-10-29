package main

import (
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/servicebinding"
)

func newServiceAliasAllowed(store servicePolicyApps) gateway.ServiceAliasAllowed {
	return servicebinding.NewAliasAllowed(store)
}
