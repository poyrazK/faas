package runtimeupgrade

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/freeze"
	"github.com/onebox-faas/faas/pkg/state"
)

// ReleaseFreezeHold reports whether a release freeze covers a claimed
// upgrade's app. A runtime upgrade is a production release: staging refuses a
// frozen app (ADR-20261019071733 amendment 1), and a freeze that starts while
// the candidate builds holds its cutover until lifted (amendment 2).
func ReleaseFreezeHold(store any) func(context.Context, state.RuntimeUpgradeOperationClaim) (bool, error) {
	deployments, _ := store.(interface {
		DeploymentByID(context.Context, string) (state.Deployment, error)
	})
	return func(ctx context.Context, c state.RuntimeUpgradeOperationClaim) (bool, error) {
		if deployments == nil {
			return false, errors.New("deployment reads unavailable")
		}
		serving, err := deployments.DeploymentByID(ctx, c.ServingDeploymentID)
		if err != nil {
			return false, fmt.Errorf("read serving deployment: %w", err)
		}
		active, err := freeze.ActiveFor(ctx, store, c.AccountID, c.AppID, serving.Scope, time.Now().UTC())
		if err != nil {
			return false, err
		}
		return len(active) > 0, nil
	}
}
