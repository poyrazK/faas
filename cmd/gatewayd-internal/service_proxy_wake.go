package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// newServiceProxyWaker builds the ADR-196 wake seam for the node-local
// service proxy: a call to a parked internal service is held while the
// scheduler restores it, rather than answering 503.
//
// `ensure` is production-wired to Handler.EnsureServiceCapacity, which routes
// through the same WakeGate the public edge uses. That is what makes a public
// request and an internal call for one parked app coalesce into a single
// restore instead of racing to admit two instances.
//
// This is a package-level constructor rather than a closure inside run() so
// the store projection and the deleted-app edge can be exercised without
// standing up the whole daemon.
func newServiceProxyWaker(store state.Store, ensure func(context.Context, gateway.App) error) gateway.ServiceProxyWaker {
	return func(ctx context.Context, appID string) error {
		app, err := store.AppByID(ctx, appID)
		if err != nil {
			return fmt.Errorf("service wake: load app %q: %w", appID, err)
		}
		resolved, ok, err := (pgRouter{store: store}).toApp(ctx, app)
		if err != nil {
			return fmt.Errorf("service wake: project app %q: %w", appID, err)
		}
		if !ok {
			// Deleted between resolution and wake. Report no error: the proxy
			// re-reads the registry, finds nothing, and answers "no healthy
			// replicas" rather than a 503 that blames the platform for a
			// customer deletion.
			return nil
		}
		return ensure(ctx, resolved)
	}
}
