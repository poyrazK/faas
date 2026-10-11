package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/chaos"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func newServiceTCPChaosResolver(store state.Store) gateway.ServiceTCPChaosResolver {
	authorize := newServiceProxyAuthorizer(store)
	return func(ctx context.Context, callerID, targetID string) (chaos.Lease, error) {
		if _, err := authorize(ctx, callerID, targetID); err != nil {
			return chaos.Lease{}, fmt.Errorf("authorize scenario TCP session: %w", err)
		}
		caller, err := store.ScenarioTestMemberByApp(ctx, callerID)
		if err != nil {
			return chaos.Lease{}, fmt.Errorf("load scenario TCP caller: %w", err)
		}
		target, err := store.ScenarioTestMemberByApp(ctx, targetID)
		if err != nil {
			return chaos.Lease{}, fmt.Errorf("load scenario TCP target: %w", err)
		}
		if caller.RunID != target.RunID || caller.AccountID != target.AccountID {
			return chaos.Lease{}, gateway.ErrServiceProxyDenied
		}
		return store.ScenarioTestChaosForCall(ctx, caller.RunID, callerID, target.Workload)
	}
}
