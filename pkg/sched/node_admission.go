package sched

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type freshNodePlacementKey struct{}

func freshNodePlacement(ctx context.Context) bool {
	fresh, _ := ctx.Value(freshNodePlacementKey{}).(bool)
	return fresh
}

func nodePlacementContext(ctx context.Context, p Placement) context.Context {
	return state.WithNodeAdmission(ctx, state.NodeAdmission{NodeID: p.NodeID, Generation: p.LifecycleGeneration})
}

// Only a definite stale placement can be retried. Database/transport errors may
// hide a committed insert, so they never cause another instance or VM dispatch.
func (e *Engine) createWithNodePlacement(ctx context.Context, request Request, restore bool, create func(context.Context, Placement) (state.Instance, error)) (state.Instance, Placement, func(), error) {
	for range api.NodeAdmissionPlacementAttempts {
		var p Placement
		var release func()
		var err error
		if restore {
			p, release, err = e.chooseRestorePlacementLocked(ctx, request)
		} else {
			p, err = e.choosePlacementLocked(ctx, request)
		}
		if err != nil {
			return state.Instance{}, Placement{}, nil, err
		}
		var instance state.Instance
		if p.LifecycleGeneration <= 0 {
			err = state.ErrNodeAdmissionStale
		} else {
			instance, err = create(nodePlacementContext(ctx, p), p)
		}
		if err == nil {
			return instance, p, release, nil
		}
		if release != nil {
			release()
		}
		if !errors.Is(err, state.ErrNodeAdmissionStale) {
			return state.Instance{}, Placement{}, nil, err
		}
		ctx = context.WithValue(ctx, freshNodePlacementKey{}, true)
	}
	return state.Instance{}, Placement{}, nil, api.ErrCapacity("The node fleet changed during placement; retry admission")
}
