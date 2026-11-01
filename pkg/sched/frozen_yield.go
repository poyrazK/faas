package sched

// Frozen VMs yield their memory to admission (ADR 20261031190030).
//
// A frozen VM (ADR-1151) is a disposable cache of a paused instance: it is
// not billed, and parking it only costs its app a restore instead of a thaw on
// the next request. Admission counts its memory, though, and nothing gave
// that memory back to a wake: compression ran only above the ceiling or on
// zswap nodes, and park-to-admit (ADR-643) parked another customer's running
// instance while frozen VMs stayed put until their TTL. Here the reaper parks
// frozen VMs on nodes compression cannot bring under the low-water mark, and
// a wake refused for RAM parks frozen VMs before any running instance.

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	// frozenParkAdmission is the park reason of a frozen VM the reaper
	// parked for a node's admission headroom.
	frozenParkAdmission = "admission"
	// frozenParkRefusedWake is the park reason of a frozen VM parked for a
	// wake refused for RAM.
	frozenParkRefusedWake = "refused_wake"
)

// yieldNodeFrozen parks a node's frozen VMs, longest frozen first, at most
// FrozenYieldPerNodePerTick, until used is under target. ids are the node's
// frozen VMs old enough to yield (FrozenOldestFirst).
func (e *Engine) yieldNodeFrozen(ctx context.Context, ids []string, used, target int) {
	for i, id := range ids {
		if i == api.FrozenYieldPerNodePerTick || used < target {
			return
		}
		charge, ok := e.ledger.FrozenChargeOf(id)
		if !ok {
			continue
		}
		if e.parkFrozenToYield(ctx, id, frozenParkAdmission) {
			used -= charge
		}
	}
}

// parkFrozenToYield parks one frozen VM for admission and reports whether it
// left the frozen state. A VM thawed or parked meanwhile is left alone.
func (e *Engine) parkFrozenToYield(ctx context.Context, instanceID, reason string) bool {
	if e.frozenYieldOne != nil {
		return e.frozenYieldOne(ctx, instanceID, reason)
	}
	// ParkFrozen is a no-op for a row no longer frozen; only a VM this call
	// took out of the frozen state counts as freed.
	if !e.ledger.IsFrozen(instanceID) {
		return false
	}
	parkCtx, cancel := context.WithTimeout(ctx, reaperParkTimeout)
	defer cancel()
	if err := e.ParkFrozen(parkCtx, instanceID, reason); err != nil {
		e.log.Debug("sched: frozen yield: park", "instance", instanceID, "reason", reason, "err", err)
		return false
	}
	return !e.ledger.IsFrozen(instanceID)
}

// parkFrozenForRefusedWake parks frozen VMs for a wake refused for RAM, on
// the node where the fewest of them free what it lacks, and waits for the
// node's measurement and capacity report to show the freed memory, so the
// wake's one retry is decided on it. It reports whether it parked any; a
// refusal for another resource, or frozen VMs that cannot cover the
// request, leave the decision to park-to-admit's running candidates.
func (e *Engine) parkFrozenForRefusedWake(ctx context.Context, ramMB int, refusal error) bool {
	need, isRAM := ramRefusalNeed(refusal, ramMB)
	if !isRAM || need <= 0 {
		return false
	}
	nodeID, ids, ok := e.ledger.FrozenToYield(need, api.FrozenYieldPerRefusedWake)
	if !ok {
		return false
	}
	parked := 0
	for _, id := range ids {
		if e.parkFrozenToYield(ctx, id, frozenParkRefusedWake) {
			parked++
		}
	}
	if parked == 0 {
		return false
	}
	e.log.Info("sched: parked frozen instances for a refused wake", "node", nodeID, "parked", parked)
	// Both views stamp wall-clock time: the poller's tick and the report's
	// receipt.
	e.awaitFreshNodeView(ctx, nodeID, time.Now())
	return true
}

// ramRefusalNeed reports whether a capacity refusal names node RAM among its
// blockers, and the MB the refused request books: the blocker's request when
// it carries one (placement and the ledger include sidecars), else the app's
// plan RAM + 8 MB.
func ramRefusalNeed(err error, ramMB int) (needMB int, isRAM bool) {
	for _, b := range admissionBlockers(err) {
		if b.Code != "node_ram" {
			continue
		}
		if b.Requested != nil && *b.Requested > 0 {
			return int(*b.Requested), true
		}
		if ramMB > 0 {
			return api.BillableRAMMB(ramMB), true
		}
		return 0, true
	}
	return 0, false
}

// awaitFreshNodeView waits, at most FrozenYieldFreshViewMillis, until the
// node's memory measurement (measured admission) and vmmd's capacity report
// (placement) were both taken after since. Either view the scheduler does not
// have is not waited for.
func (e *Engine) awaitFreshNodeView(ctx context.Context, nodeID string, since time.Time) {
	deadline := time.NewTimer(time.Duration(api.FrozenYieldFreshViewMillis) * time.Millisecond)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for !e.nodeViewFreshSince(nodeID, since) {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-tick.C:
		}
	}
}

func (e *Engine) nodeViewFreshSince(nodeID string, since time.Time) bool {
	if asOf, ok := e.ledger.MeasuredAsOf(nodeID); ok && !asOf.After(since) {
		return false
	}
	if e.capacityTable != nil {
		if seen, ok := e.capacityTable.LastSeen()[nodeID]; ok && !seen.After(since) {
			return false
		}
	}
	return true
}
