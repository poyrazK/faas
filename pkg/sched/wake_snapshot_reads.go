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
	// warmAll and initAll are every live row of the tier, one per CPU
	// platform, newest first; warm and init are their first rows
	// (ADR-20261024225612 amendment 1).
	warmAll, initAll []state.Snapshot
}

// liveTier lists a tier's live rows and reports the newest as the single row
// callers before per-platform slots expected, with ErrNotFound when none.
func (e *Engine) liveTier(ctx context.Context, deploymentID, tier string) ([]state.Snapshot, state.Snapshot, error) {
	rows, err := state.LiveSnapshotsForTier(ctx, e.store, deploymentID, tier)
	if err != nil {
		return nil, state.Snapshot{}, err
	}
	if len(rows) == 0 {
		return nil, state.Snapshot{}, state.ErrNotFound
	}
	return rows, rows[0], nil
}

// readWakeSnapshotInputs reads the backoff gate (when gated), the
// deployment's companion RAM and its latest warm (when the plan allows it)
// and init snapshots.
func (e *Engine) readWakeSnapshotInputs(ctx context.Context, deploymentID, plan string, gated bool) wakeSnapshotReads {
	reads := wakeSnapshotReads{readWarm: planAllowsWarm(plan)}
	fns := []func(){
		func() { reads.sidecarMBs, reads.sidecarErr = e.store.DeploymentSidecarRAMs(ctx, deploymentID) },
		func() {
			reads.initAll, reads.init, reads.initErr = e.liveTier(ctx, deploymentID, state.SnapshotTierInit)
		},
	}
	if gated {
		fns = append(fns, func() {
			reads.backoff, reads.backoffActive, reads.backoffErr = e.store.DeploymentSnapshotBackoffActive(ctx, deploymentID)
		})
	}
	if reads.readWarm {
		fns = append(fns, func() {
			reads.warmAll, reads.warm, reads.warmErr = e.liveTier(ctx, deploymentID, state.SnapshotTierWarm)
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
	// ADR-20261024225612 amendment 1: each tier may hold one live row per CPU
	// platform. Take the newest row that passes; the rows not taken stay as
	// fallbacks for when every node that can restore the chosen one is full.
	var warmRows []state.Snapshot
	if r.readWarm && r.warmErr == nil {
		warmRows = r.warmAll
		for i, snap := range warmRows {
			if reason, receipt, locality := check(snap); reason == "" {
				return wakeSnapshotChoice{snap: snap, ok: true, tier: wakeTierWarm, receipt: receipt, locality: locality,
					fallbacks: snapshotFallbacks(warmRows, i, r.initAll)}
			}
		}
	}
	switch {
	case errors.Is(r.initErr, state.ErrNotFound):
		return wakeSnapshotChoice{tier: wakeTierColdBootFallback, coldReason: ColdReasonNoSnapshot}
	case r.initErr != nil:
		return wakeSnapshotChoice{tier: wakeTierColdBootFallback, coldReason: ColdReasonLookupFailed}
	}
	rejected, firstReason := state.Snapshot{}, ""
	for i, snap := range r.initAll {
		reason, receipt, locality := check(snap)
		if reason == "" {
			return wakeSnapshotChoice{snap: snap, ok: true, tier: wakeTierInit, receipt: receipt, locality: locality,
				fallbacks: snapshotFallbacks(nil, -1, r.initAll[i+1:])}
		}
		if i == 0 {
			rejected, firstReason = snap, reason
		}
	}
	return wakeSnapshotChoice{tier: wakeTierColdBootFallback, coldReason: firstReason, rejected: rejected}
}

// snapshotFallbacks is the rows a wake may restore instead of the chosen
// one, in preference order: the tier's other rows, then the init rows.
func snapshotFallbacks(tierRows []state.Snapshot, chosen int, initRows []state.Snapshot) []state.Snapshot {
	out := make([]state.Snapshot, 0, len(tierRows)+len(initRows))
	for i, snap := range tierRows {
		if i != chosen {
			out = append(out, snap)
		}
	}
	return append(out, initRows...)
}
