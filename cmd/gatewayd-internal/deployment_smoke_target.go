// adr: 570
package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type deploymentSmokeTargetStore interface {
	RunningDeploymentSmokeTarget(context.Context, string, string) (state.TrafficPlacement, bool, error)
}

func newDeploymentSmokeTargetLoader(store deploymentSmokeTargetStore) func(context.Context, string, string) (gateway.Target, bool, error) {
	return func(ctx context.Context, app, deployment string) (gateway.Target, bool, error) {
		placement, found, err := store.RunningDeploymentSmokeTarget(ctx, app, deployment)
		if err != nil || !found {
			return gateway.Target{}, false, err
		}
		target := trafficPlacementTarget(placement)
		target.AddedAt = time.Now()
		return target, true, nil
	}
}
