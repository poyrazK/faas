package sched

// adr: 20261024085817

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// parkReasonNodeDraining labels a frozen or warm VM parked, or an idle
// instance parked instead of frozen, because its node no longer admits work.
const parkReasonNodeDraining = "node_draining"

// drainTransitionKind labels the instance transitions a drain park records.
const drainTransitionKind = "node_drain"

// ParkForDrain moves an instance the recovery arbiter could not hand off
// off its draining node (ADR-20261024085817). An instance that serves routed
// traffic is withdrawn from routing first: it moves to DRAINING (no longer
// routable, still accounted), every gateway acknowledges the new routes, its
// in-flight requests get a bounded wait, and only then is it destroyed and
// stopped. The next request wakes the app on an admitting node from the
// deployment's snapshot. DRAINING cannot be captured, so no snapshot is
// written; the request barrier is what a drain must not skip. WARM, FROZEN,
// worker, job and fork instances take the ordinary park. A row another path
// moved meanwhile is left alone.
func (e *Engine) ParkForDrain(ctx context.Context, instanceID string) error {
	ins, err := e.store.InstanceByID(ctx, instanceID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sched: node drain: load instance %s: %w", instanceID, err)
	}
	if !drainWithdrawsRoutes(ins) {
		return e.ParkWithReason(ctx, instanceID, drainTransitionKind)
	}
	return e.retireForDrain(ctx, ins)
}

// drainWithdrawsRoutes reports whether a drain park must take ins out of
// routing before it ends the VM: a RUNNING or already withdrawn instance of
// a mode the gateway routes requests to.
func drainWithdrawsRoutes(ins state.Instance) bool {
	switch state.State(ins.State) {
	case state.StateRunning, state.StateDraining:
	default:
		return false
	}
	switch state.InstanceMode(ins.Mode) {
	case state.InstanceModeWorker, state.InstanceModeJob:
		return false
	}
	return !state.IsFork(ins.Mode)
}

// retireForDrain is the runtime-config retirement (retireRuntimeConfigInstance)
// for a drain: withdraw, route barrier, request barrier, destroy, stop. A
// drain must finish, so a route round that does not converge or requests
// that outlast the bound are logged and the instance is retired anyway; a
// failed destroy leaves the row DRAINING for the next tick to resume.
func (e *Engine) retireForDrain(ctx context.Context, ins state.Instance) error {
	release := e.lockApp(ins.AppID)
	fresh, err := e.store.InstanceByID(ctx, ins.ID)
	if err == nil && state.State(fresh.State) == state.StateRunning {
		if _, err = e.transitionWithKindCAS(ctx, fresh.ID, fresh.AppID, state.StateDraining, drainTransitionKind, "routes_withdrawn"); err == nil {
			fresh, err = e.store.InstanceByID(ctx, ins.ID)
		}
	}
	release()
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sched: node drain: withdraw instance %s: %w", ins.ID, err)
	}
	if state.State(fresh.State) != state.StateDraining {
		return nil
	}
	acknowledgedAt, _, converged := e.waitForServiceRouteConvergenceForRollout(ctx, fresh.AppID, "", fresh.DeploymentID, drainTransitionKind)
	if !converged {
		e.log.Warn("sched: node drain: route convergence incomplete; waiting for in-flight requests anyway",
			"app", fresh.AppID, "instance", fresh.ID, "node_id", fresh.NodeID)
		acknowledgedAt = time.Now()
	}
	if err := e.waitForDrainRequests(ctx, fresh, acknowledgedAt); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		e.log.Warn("sched: node drain: in-flight requests outlasted the bound; retiring the instance",
			"app", fresh.AppID, "instance", fresh.ID, "node_id", fresh.NodeID, "err", err)
	}
	if err := e.timedDestroy(context.WithoutCancel(ctx), fresh.NodeID, fresh.ID, DestroyTimeout); err != nil {
		return fmt.Errorf("sched: node drain: destroy withdrawn instance %s: %w", fresh.ID, err)
	}
	changed, err := e.transitionWithKindCAS(ctx, fresh.ID, fresh.AppID, state.StateStopped, drainTransitionKind, "drain_park_point")
	if err != nil {
		return fmt.Errorf("sched: node drain: stop withdrawn instance %s: %w", fresh.ID, err)
	}
	if changed {
		e.ledger.Release(fresh.ID)
		if fresh.Mode == string(state.InstanceModeService) {
			e.scheduleServiceReconcile(ctx, fresh.DeploymentID)
		}
	}
	return nil
}

// waitForDrainRequests waits, within the drain's request bound, for the
// withdrawn instance to report no in-flight requests (the runtime-config
// request barrier).
func (e *Engine) waitForDrainRequests(ctx context.Context, ins state.Instance, acknowledgedAt time.Time) error {
	bound := e.drainRequestBound
	if bound <= 0 {
		bound = time.Duration(api.ServiceReplicaDrainTimeoutSeconds) * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	return e.waitForRuntimeConfigInstanceDrain(waitCtx, ins.AppID, ins.ID, acknowledgedAt)
}

// ObserveNodeLifecycles records which nodes no longer admit work. The
// recovery runner calls it with every node list it reads, about once a
// second, so the view lags a lifecycle change by about one recovery tick.
func (e *Engine) ObserveNodeLifecycles(nodes []state.ComputeNode) {
	if e == nil {
		return
	}
	leaving := make(map[string]struct{})
	for _, node := range nodes {
		if nodeLeaving(node) {
			leaving[node.ID] = struct{}{}
		}
	}
	e.leavingNodes.Store(&leaving)
}

// nodeLeavingByID reports whether the last observed node list showed
// nodeID no longer admitting work. A node the view has not seen admits.
func (e *Engine) nodeLeavingByID(nodeID string) bool {
	if e == nil || nodeID == "" {
		return false
	}
	leaving := e.leavingNodes.Load()
	if leaving == nil {
		return false
	}
	_, ok := (*leaving)[nodeID]
	return ok
}

// nodeLeaving is placement's admission predicate turned around: a node
// with a lifecycle that does not admit (draining, force_draining,
// maintenance, retired, unavailable) is leaving. A row without a
// lifecycle predates it and admits, as placement treats it.
func nodeLeaving(node state.ComputeNode) bool {
	return node.Lifecycle != "" && !node.Lifecycle.IsAdmitting()
}
