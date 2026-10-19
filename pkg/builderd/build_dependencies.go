package builderd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func (b *Builderd) admitBuildDependencies(ctx context.Context, build state.Build, dep state.Deployment) (*state.BuildDependencyGate, bool, error) {
	var stages state.StageState
	if len(dep.StageState) > 0 {
		if err := json.Unmarshal(dep.StageState, &stages); err != nil {
			b.recoverClaimAfterLookupFailure(ctx, build, "read build dependency intent", err)
			return nil, true, err
		}
	}
	if stages.BuildDependencyGate == nil {
		return nil, false, nil
	}
	if b.stopIfBuildCancelled(ctx, build) {
		return nil, true, nil
	}
	store, ok := b.store.(state.BuildDependencyGateStore)
	if !ok {
		err := errors.New("build dependency readiness is unavailable")
		b.markFailed(ctx, build, state.FailureInfra, err.Error(), time.Now())
		return nil, true, err
	}
	gate, err := store.CheckBuildDependencies(ctx, build, time.Now().UTC())
	if err != nil {
		b.recoverClaimAfterLookupFailure(ctx, build, "check build dependencies", err)
		return nil, true, fmt.Errorf("builderd: check build dependencies: %w", err)
	}
	if gate == nil {
		err := errors.New("saved build dependency gate is unavailable")
		b.markFailed(ctx, build, state.FailureInfra, err.Error(), time.Now())
		return nil, true, err
	}
	switch gate.Status {
	case "waiting":
		return gate, true, nil
	case "failed":
		b.markFailedEx(ctx, build, state.FailureUserError, gate.Code, "", gate.Blocker, time.Now())
		return gate, true, nil
	case "ready":
		return gate, false, nil
	default:
		return nil, true, errors.New("invalid saved build dependency state")
	}
}
