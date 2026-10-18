// adr: 20261017201005
package main

import (
	"context"
	"github.com/onebox-faas/faas/pkg/servicebinding"
	"github.com/onebox-faas/faas/pkg/state"
)

func authorizeRehearsalService(ctx context.Context, store state.Store, caller, target state.App) (string, error) {
	return servicebinding.AuthorizeRehearsalService(ctx, store, caller, target)
}
