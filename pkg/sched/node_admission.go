package sched

import (
	"context"
	"errors"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type freshNodePlacementKey struct{}

func freshNodePlacement(ctx context.Context) bool {
	fresh, _ := ctx.Value(freshNodePlacementKey{}).(bool)
	return fresh
}

// nodePlacementContext binds the placement's node generation (ADR-1528) and
// the reservation allowance this scheduler may claim on that node: a measured
// admission may book past the bare ceiling up to ADR-909's bound (ADR-909
// amendment 1).
func (e *Engine) nodePlacementContext(ctx context.Context, p Placement) context.Context {
	admission := state.NodeAdmission{NodeID: p.NodeID, Generation: p.LifecycleGeneration}
	if e.ledger != nil {
		admission.ReservationPercent = e.ledger.ReservationPercent(p.NodeID)
	}
	return state.WithNodeAdmission(ctx, admission)
}

// Only a definite refusal can be retried: a stale placement re-places on a
// fresh fleet read, and a node the durable reservation found full (ADR-909
// amendment 1) is excluded from the next choice. Database/transport errors
// may hide a committed insert, so they never cause another instance or VM
// dispatch.
func (e *Engine) createWithNodePlacement(ctx context.Context, request Request, restore bool, create func(context.Context, Placement) (state.Instance, error)) (state.Instance, Placement, func(), error) {
	var nodeFull error
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
			// No other node fits: report the node that refused, as before.
			if nodeFull != nil && isCapacityProblem(err) {
				return state.Instance{}, Placement{}, nil, nodeFull
			}
			return state.Instance{}, Placement{}, nil, err
		}
		var instance state.Instance
		if p.LifecycleGeneration <= 0 {
			err = state.ErrNodeAdmissionStale
		} else {
			instance, err = create(e.nodePlacementContext(ctx, p), p)
		}
		if err == nil {
			return instance, p, release, nil
		}
		if release != nil {
			release()
		}
		switch {
		case errors.Is(err, state.ErrNodeCapacity):
			// The scheduler's view missed reservations the durable guard
			// counts: a peer's admission, or a node it judged by measurement.
			nodeFull = err
			request.ExcludeNodeIDs = append(slices.Clone(request.ExcludeNodeIDs), p.NodeID)
		case errors.Is(err, state.ErrNodeAdmissionStale):
			ctx = context.WithValue(ctx, freshNodePlacementKey{}, true)
		default:
			return state.Instance{}, Placement{}, nil, err
		}
	}
	if nodeFull != nil {
		return state.Instance{}, Placement{}, nil, nodeFull
	}
	return state.Instance{}, Placement{}, nil, explainAdmission(api.ErrCapacity("The node fleet changed during placement; retry admission"),
		api.WorkerAdmissionBlocker{Code: "placement_changed"})
}
