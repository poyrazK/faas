package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// healthOutcomeLookup reads the app's most recently started instance for the
// edge health answer (ADR-641).
func healthOutcomeLookup(store state.Store) gateway.HealthOutcomeLookup {
	return func(ctx context.Context, appID string) (string, bool, error) {
		instances, err := store.ListLatestInstancesForApp(ctx, appID, 1)
		if err != nil || len(instances) == 0 {
			return "", false, err
		}
		return instances[0].State, true, nil
	}
}
