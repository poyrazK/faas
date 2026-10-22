package main

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/servicebinding"
	"github.com/onebox-faas/faas/pkg/state"
)

// Keep the gateway wiring and its regression tests on the shared authority.
// A strict compute hold or preview stop on the target refuses service traffic
// the same way it refuses edge traffic.
func newServiceProxyAuthorizer(store state.Store) gateway.ServiceProxyAuthorizer {
	authorize := servicebinding.NewAuthorizer(store)
	return func(ctx context.Context, callerAppID, targetAppID string) (gateway.ServiceCaller, error) {
		caller, err := authorize(ctx, callerAppID, targetAppID)
		if err != nil {
			return caller, err
		}
		target, err := store.AppByID(ctx, targetAppID)
		if err != nil {
			return gateway.ServiceCaller{}, fmt.Errorf("load target app: %w", err)
		}
		if err := state.CheckFinancialBudgetAdmission(ctx, store, target, "", time.Now().UTC()); err != nil {
			return gateway.ServiceCaller{}, err
		}
		return caller, nil
	}
}

func isAppID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil
}
