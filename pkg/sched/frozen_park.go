package sched

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/state"
)

// ParkFrozen parks a frozen instance: the VM is destroyed and the row moves
// FROZEN -> PARKED. It is a no-op for a row that is no longer frozen.
func (e *Engine) ParkFrozen(ctx context.Context, instanceID, reason string) error {
	ins, err := e.lockedInStates(ctx, instanceID, state.StateFrozen)
	if err != nil || ins == nil {
		return err
	}
	defer e.unlockApp(ins.AppID)
	e.parkFrozenLocked(ctx, *ins, reason)
	return nil
}

// parkFrozenLocked destroys a frozen VM and parks its row; the caller holds
// the app lock. The deployment's snapshot is untouched, so the next request
// restores from it.
func (e *Engine) parkFrozenLocked(ctx context.Context, frozen state.Instance, reason string) {
	e.thawTickets.drop(frozen.ID)
	cleanupCtx := context.WithoutCancel(ctx)
	if e.frozenNodeUnreachable(cleanupCtx, frozen.NodeID) {
		// The VM went with its node or vmmd. Parking the row is enough:
		// vmmd's live-VM reaper destroys a VM whose row is no longer live.
		e.log.Info("sched: frozen park: node unreachable; leaving the VM to vmmd's reaper",
			"instance", frozen.ID, "node", frozen.NodeID, "reason", reason)
	} else if err := e.timedDestroy(cleanupCtx, frozen.NodeID, frozen.ID, DestroyTimeout); err != nil {
		e.log.Warn("sched: frozen park: destroy", "instance", frozen.ID, "reason", reason, "err", err)
	}
	e.ledger.Release(frozen.ID)
	// A worker has no snapshot to wake from (ADR-138): it stops, and the
	// pool cold-boots the next one (ADR-20261019103000).
	next := state.StateParked
	if !instanceModeUsesSnapshots(frozen.Mode) {
		next = state.StateStopped
	}
	if err := updateInstanceStateCAS(cleanupCtx, e.store, frozen.ID, string(state.StateFrozen), string(next)); err != nil {
		if !errors.Is(err, state.ErrConflict) {
			e.log.Warn("sched: frozen park: park row", "instance", frozen.ID, "reason", reason, "err", err)
		}
		return
	}
	e.recordCommittedInstanceTransition(cleanupCtx, frozen, state.StateFrozen, next, frozen.AppID, "frozen_park", reason)
	e.observeFrozenPark(reason)
}

// frozenNodeUnreachable reports whether a frozen VM's node is unavailable
// or stopped heartbeating (the dead-node reconciler's staleness), so a thaw
// or destroy there would only wait out its RPC timeout.
func (e *Engine) frozenNodeUnreachable(ctx context.Context, nodeID string) bool {
	if nodeID == "" {
		return false
	}
	node, err := e.store.ComputeNodeByID(ctx, nodeID)
	if err != nil {
		return errors.Is(err, state.ErrNotFound)
	}
	return e.frozenNodeRowUnreachable(node)
}

// frozenNodeRowUnreachable is frozenNodeUnreachable for a node row the
// caller already read.
func (e *Engine) frozenNodeRowUnreachable(node state.ComputeNode) bool {
	if node.Lifecycle == state.NodeLifecycleUnavailable {
		return true
	}
	staleness := time.Duration(api.DeadNodeReconcilerStalenessSeconds) * time.Second
	if e.deadNodeReconcilerStalenessSeconds > 0 {
		staleness = time.Duration(e.deadNodeReconcilerStalenessSeconds) * time.Second
	}
	return !node.LastHeartbeatAt.IsZero() && time.Since(node.LastHeartbeatAt) > staleness
}

// freezeRefusal names why an idle RUNNING instance must park instead of
// freezing, or returns "" when it may freeze. The caller holds the app lock.
func (e *Engine) freezeRefusal(ctx context.Context, ins state.Instance, early bool) (string, error) {
	app, err := e.store.AppByID(ctx, ins.AppID)
	if err != nil {
		return "", err
	}
	if app.Status != state.AppActive || !e.frozenTierAllows(app, ins.Mode, e.privateNetworkProjection(ctx, app).NetworkID) {
		return "ineligible", nil
	}
	// An early freeze (ADR-20261021215353) stops timers before the idle
	// timeout, which only between_requests workloads have agreed to.
	if early && api.EffectivePause(app.Manifest.Pause, app.Type == state.AppTypeFunction) != api.PauseBetweenRequests {
		return "ineligible", nil
	}
	if ins.RAMMB != app.RAMMB || e.runtimeConfigStale(ctx, ins) {
		return "ineligible", nil
	}
	if app.Manifest.Profiling != nil && app.Manifest.Profiling.Enabled {
		return "ineligible", nil
	}
	if ephemeral, err := e.hasEphemeralSecretForInstance(ctx, ins, app); err != nil || ephemeral {
		return "ineligible", err
	}
	dep, err := e.store.DeploymentByID(ctx, ins.DeploymentID)
	if errors.Is(err, state.ErrNotFound) {
		return "ineligible", nil
	}
	if err != nil {
		return "", err
	}
	if dep.Status != state.DeployLive {
		return "ineligible", nil
	}
	// A frozen VM is disposable only while the deployment has a snapshot
	// to wake from (ADR-005); the first idle period after a deploy parks.
	if _, ok := e.reusableSnapshot(ctx, dep.ID, state.SnapshotTierInit); !ok {
		return "no_snapshot", nil
	}
	if e.warmCapturePending(ctx, app, ins, dep) {
		return "no_snapshot", nil
	}
	if e.ledger.FrozenCountForNode(ins.NodeID) >= api.FrozenMaxPerNode {
		return "node_full", nil
	}
	return "", nil
}

// warmCapturePending reports whether parking this instance would capture
// the deployment's warm-tier snapshot (ADR-070), which only a RUNNING VM
// can provide. Freezing it would starve that capture.
//
// It applies every gate captureWarmSnapshotLocked does. It used to skip the
// minimum age and request count, so a function instance that served fewer
// than warm_snapshot_min_requests (5) requests parked "to capture" a warm
// snapshot the capture then refused, on every idle period: low-traffic
// functions never froze (Hunt 12 H12-52).
func (e *Engine) warmCapturePending(ctx context.Context, app state.App, ins state.Instance, dep state.Deployment) bool {
	if !app.WarmSnapshotEnabled || !warmCaptureInstanceGatesOpen(app, ins, time.Now()) {
		return false
	}
	acct, err := state.AppResourceAccount(ctx, e.store, app)
	if err != nil || !acct.Plan.WarmSnapshotAllowed() {
		return false
	}
	_, ok := e.reusableSnapshot(ctx, dep.ID, state.SnapshotTierWarm)
	return !ok
}

// warmCaptureInstanceGatesOpen is the per-instance half of the warm-capture
// gates (ADR-070, ADR-098 C10) shared by captureWarmSnapshotLocked and
// warmCapturePending: no before-checkpoint hook (except on a qualified
// native Node function) and no profiling, a framework-ready stamp at least
// warm_snapshot_min_ms old, and at least warm_snapshot_min_requests
// requests served.
func warmCaptureInstanceGatesOpen(app state.App, ins state.Instance, now time.Time) bool {
	qualifiedFunction := app.Type == "function" && functionlifetime.NativeNode(app.Runtime)
	if app.Manifest.BeforeCheckpoint != nil && !qualifiedFunction || (app.Manifest.Profiling != nil && app.Manifest.Profiling.Enabled) {
		return false
	}
	if ins.FrameworkReadyAt == nil {
		return false
	}
	if app.WarmSnapshotMinMs > 0 && now.Sub(*ins.FrameworkReadyAt) < time.Duration(app.WarmSnapshotMinMs)*time.Millisecond {
		return false
	}
	return app.WarmSnapshotMinRequests <= 0 || ins.RequestCount >= int64(app.WarmSnapshotMinRequests)
}

// frozenExpired reports whether a frozen instance has outlived FrozenTTL.
// Frozen time is measured from the end of the idle timeout that froze it:
// the instance's last activity (or start) plus that timeout.
func frozenExpired(now time.Time, in InstanceInfo, idleTimeout time.Duration) bool {
	since := in.Started
	if in.LastRequest.After(since) {
		since = in.LastRequest
	}
	return now.Sub(since) > idleTimeout+time.Duration(api.FrozenTTLSeconds)*time.Second
}

// problemCode is the stable code of an API problem error, or "".
func problemCode(err error) string {
	var prob *api.Problem
	if errors.As(err, &prob) {
		return prob.Code
	}
	return ""
}

func (e *Engine) observeFreeze(outcome string) {
	if e != nil && e.ops != nil {
		e.ops.FrozenFreezeTotal(outcome).Inc()
	}
}

func (e *Engine) observeThaw(outcome string) {
	if e != nil && e.ops != nil {
		e.ops.FrozenThawTotal(outcome).Inc()
	}
}

func (e *Engine) observeFrozenPark(reason string) {
	if e != nil && e.ops != nil {
		e.ops.FrozenParksTotal(reason).Inc()
	}
}
