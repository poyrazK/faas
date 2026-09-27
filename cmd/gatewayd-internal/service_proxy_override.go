package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// newServiceProxyDeploymentValidator is called only after the service name
// has resolved and the caller has passed binding authorization. The live set
// includes 0%-traffic deployments, but never superseded or foreign-app rows.
func newServiceProxyDeploymentValidator(store state.Store) gateway.ServiceProxyDeploymentValidator {
	return func(ctx context.Context, appID, deploymentID string) (bool, error) {
		deployments, err := store.LiveDeployments(ctx, appID)
		if err != nil {
			return false, fmt.Errorf("service deployment override: list live deployments for app %q: %w", appID, err)
		}
		for _, deployment := range deployments {
			if deployment.AppID == appID && deployment.ID == deploymentID && deployment.Status == state.DeployLive {
				// Manual dark deployments remain valid one-hop overrides.
				// Retained stable revisions require an unexpired direct pin;
				// status alone remains live until the minute-level sweep.
				if deployment.TrafficPercent > 0 || deployment.TrafficPercentExplicit {
					return true, nil
				}
				resolver, ok := store.(state.RevisionPinStore)
				if !ok {
					return false, fmt.Errorf("service deployment override: revision pin resolver unavailable")
				}
				_, pinErr := resolver.ResolveRevisionPin(ctx, appID, deployment.Scope, deploymentID)
				if errors.Is(pinErr, state.ErrNotFound) {
					return false, nil
				}
				return pinErr == nil, pinErr
			}
		}
		return false, nil
	}
}
