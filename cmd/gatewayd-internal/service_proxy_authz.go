package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// newServiceProxyAuthorizer enforces the same-account boundary between a
// calling workload and the service it names (ADR-168).
//
// Ids are shape-checked before they reach the store. `apps.id` is a uuid
// column, so a malformed id can never name an app — but passing one through
// makes Postgres raise 22P02, which the proxy could only report as
// "service authorization is unavailable" (503). That blames the platform for
// what is a caller error, and costs a round-trip that is guaranteed to fail.
// Guarding here keeps a genuine store failure — the case that really is a
// platform fault — as the only path that still returns 503.
//
// Extracted from run() so the boundary is unit-testable without the daemon.
func newServiceProxyAuthorizer(store state.Store) gateway.ServiceProxyAuthorizer {
	return func(ctx context.Context, callerAppID, targetAppID string) (gateway.ServiceCaller, error) {
		if !isAppID(callerAppID) || !isAppID(targetAppID) {
			return gateway.ServiceCaller{}, gateway.ErrServiceProxyDenied
		}
		caller, err := store.AppByID(ctx, callerAppID)
		if errors.Is(err, state.ErrNotFound) {
			return gateway.ServiceCaller{}, gateway.ErrServiceProxyDenied
		}
		if err != nil {
			return gateway.ServiceCaller{}, fmt.Errorf("load caller app: %w", err)
		}
		target, err := store.AppByID(ctx, targetAppID)
		if errors.Is(err, state.ErrNotFound) {
			return gateway.ServiceCaller{}, gateway.ErrServiceProxyDenied
		}
		if err != nil {
			return gateway.ServiceCaller{}, fmt.Errorf("load target app: %w", err)
		}
		// An empty account on either side is a broken row rather than a
		// match; fail closed instead of letting "" == "" authorize the call.
		if caller.AccountID == "" || caller.AccountID != target.AccountID {
			return gateway.ServiceCaller{}, gateway.ErrServiceProxyDenied
		}
		// The caller row is already loaded; carrying its preview identity out
		// saves the hop a third store read for a fact we have in hand.
		return gateway.ServiceCaller{
			AppID:         caller.ID,
			PreviewOfSlug: caller.PreviewOfSlug,
			AccountID:     caller.AccountID,
		}, nil
	}
}

// isAppID reports whether the id could name a row in apps.id (a uuid column).
func isAppID(id string) bool {
	if id == "" {
		return false
	}
	_, err := uuid.Parse(id)
	return err == nil
}
