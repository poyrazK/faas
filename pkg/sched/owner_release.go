// adr: 20261110210000 — under control-plane owner authority a pressured
// node asks the control plane to move a parked app it owns; the control
// plane checks the request against apps.node_id and chooses the destination.
package sched

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// ownerReleaseBatch bounds one ProcessOwnerReleaseRequests pass.
const ownerReleaseBatch = 50

// RequestPressureRelease is the pressure rebalancer's handler on a compute
// node under control-plane owner authority (ADR-20261110210000 §4). The
// node cannot write apps.node_id, so instead of moving appID it records a
// release request for the control plane. It asks only for apps this node
// owns; the control plane re-checks ownership, residency and cooldown.
func (e *Engine) RequestPressureRelease(ctx context.Context, appID string) error {
	if e.ownerNodeID == "" || appID == "" {
		return nil
	}
	releases, ok := e.store.(state.AppOwnerReleaseStore)
	if !ok {
		return errors.New("sched: pressure release: store has no release requests")
	}
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return fmt.Errorf("sched: pressure release: lookup app: %w", err)
	}
	if app.NodeID != e.ownerNodeID {
		e.observePressure("no_eligibility")
		return nil
	}
	if err := releases.RequestAppOwnerRelease(ctx, appID, e.ownerNodeID); err != nil {
		return fmt.Errorf("sched: pressure release: %w", err)
	}
	e.observePressure("release_requested")
	return nil
}

// ProcessOwnerReleaseRequests is the control plane's side: each open request
// moves its app off the requesting node through the same checks and
// destination choice as an owner's own pressure rebalance. A request whose
// app is no longer the requester's, is resident or cooling down is dropped;
// the node asks again while the pressure lasts. It returns how many requests
// it consumed.
func (e *Engine) ProcessOwnerReleaseRequests(ctx context.Context) (int, error) {
	releases, ok := e.store.(state.AppOwnerReleaseStore)
	if !ok {
		return 0, errors.New("sched: owner release: store has no release requests")
	}
	reqs, err := releases.ListAppOwnerReleaseRequests(ctx, ownerReleaseBatch)
	if err != nil {
		return 0, fmt.Errorf("sched: owner release: list: %w", err)
	}
	done := 0
	for _, r := range reqs {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		if err := e.rebalancePressured(ctx, r.AppID, r.FromNodeID); err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				e.log.Warn("sched: owner release: request kept for retry", "app_id", r.AppID,
					"from_node", r.FromNodeID, "err", err)
				continue
			}
		}
		if err := releases.DeleteAppOwnerReleaseRequest(ctx, r.AppID, r.RequestedAt); err != nil {
			e.log.Warn("sched: owner release: delete request", "app_id", r.AppID, "err", err)
			continue
		}
		done++
	}
	return done, nil
}
