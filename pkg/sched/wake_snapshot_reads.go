package sched

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// wakeSnapshotReads are the reads a wake's snapshot gate and snapshot choice
// start from. They are independent of each other, so a wake makes them
// concurrently (ADR-1741): one after another they cost four sequential
// round trips before admission. They are reads only; the gate and the
// choice, which can retire a snapshot, still run in order after them.
type wakeSnapshotReads struct {
	backoff       state.Deployment
	backoffActive bool
	backoffErr    error

	sidecarMBs []int
	sidecarErr error

	readWarm         bool
	warm, init       state.Snapshot
	warmErr, initErr error
}

// readWakeSnapshotInputs reads the backoff gate (when gated), the
// deployment's companion RAM and its latest warm (when the plan allows it)
// and init snapshots.
func (e *Engine) readWakeSnapshotInputs(ctx context.Context, deploymentID, plan string, gated bool) wakeSnapshotReads {
	reads := wakeSnapshotReads{readWarm: planAllowsWarm(plan)}
	fns := []func(){
		func() { reads.sidecarMBs, reads.sidecarErr = e.store.DeploymentSidecarRAMs(ctx, deploymentID) },
		func() {
			reads.init, reads.initErr = e.store.LatestSnapshotForTier(ctx, deploymentID, state.SnapshotTierInit)
		},
	}
	if gated {
		fns = append(fns, func() {
			reads.backoff, reads.backoffActive, reads.backoffErr = e.store.DeploymentSnapshotBackoffActive(ctx, deploymentID)
		})
	}
	if reads.readWarm {
		fns = append(fns, func() {
			reads.warm, reads.warmErr = e.store.LatestSnapshotForTier(ctx, deploymentID, state.SnapshotTierWarm)
		})
	}
	concurrently(fns...)
	return reads
}

// guestRAMMB is Engine.guestRAMMB over the prefetched companion RAM.
func (r wakeSnapshotReads) guestRAMMB(e *Engine, deploymentID string, appRAMMB int) int {
	if appRAMMB <= 0 {
		return appRAMMB
	}
	if r.sidecarErr != nil {
		e.log.Warn("wake: companion RAM unknown; deferring snapshot RAM check to vmmd",
			"deployment_id", deploymentID, "err", r.sidecarErr)
		return 0
	}
	return api.BillableRAMMBWithSidecars(appRAMMB, r.sidecarMBs) - api.PerVMOverheadMB
}

// chooseWakeSnapshotFrom is chooseWakeSnapshot over the prefetched
// snapshots. dep is the deployment being woken; the freshness check uses it
// instead of reading it again.
func (e *Engine) chooseWakeSnapshotFrom(ctx context.Context, r wakeSnapshotReads, dep state.Deployment, expectedRAMMB int, appProtocol string) wakeSnapshotChoice {
	// Each candidate's locality is read while the candidate is checked;
	// placement needs it only for the chosen one (ADR-1741).
	check := func(snap state.Snapshot) (string, snapshotReceipt, snapshotLocalityRead) {
		var (
			reason   string
			receipt  snapshotReceipt
			locality snapshotLocalityRead
		)
		concurrently(
			func() { reason = e.snapshotRejectionFor(ctx, snap, &dep, expectedRAMMB, appProtocol, &receipt) },
			func() { locality = e.readSnapshotLocality(ctx, snap.ID) },
		)
		return reason, receipt, locality
	}
	if r.readWarm && r.warmErr == nil {
		if reason, receipt, locality := check(r.warm); reason == "" {
			return wakeSnapshotChoice{snap: r.warm, ok: true, tier: wakeTierWarm, receipt: receipt, locality: locality}
		}
	}
	switch {
	case errors.Is(r.initErr, state.ErrNotFound):
		return wakeSnapshotChoice{tier: wakeTierColdBootFallback, coldReason: ColdReasonNoSnapshot}
	case r.initErr != nil:
		return wakeSnapshotChoice{tier: wakeTierColdBootFallback, coldReason: ColdReasonLookupFailed}
	}
	reason, receipt, locality := check(r.init)
	if reason != "" {
		return wakeSnapshotChoice{tier: wakeTierColdBootFallback, coldReason: reason, rejected: r.init}
	}
	return wakeSnapshotChoice{snap: r.init, ok: true, tier: wakeTierInit, receipt: receipt, locality: locality}
}
